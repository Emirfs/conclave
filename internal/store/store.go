package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/provider"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order and tracked with PRAGMA user_version, so an
// existing database is upgraded in place rather than recreated. Never edit an
// applied migration; append a new one instead.
var migrations = []string{
	// 1: pipelines and the first, single-stream chat model.
	`
CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS stages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    command_json TEXT NOT NULL,
    status TEXT NOT NULL,
    exit_code INTEGER,
    output TEXT NOT NULL DEFAULT '',
    UNIQUE(run_id, position)
);
CREATE INDEX IF NOT EXISTS runs_status_idx ON runs(status, id);
CREATE TABLE IF NOT EXISTS chat_turns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    prompt TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_responses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id INTEGER NOT NULL REFERENCES chat_turns(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    status TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    UNIQUE(turn_id, provider)
);
CREATE INDEX IF NOT EXISTS chat_responses_status_idx ON chat_responses(status, id);
`,
	// 2: conversations own their turns, and the canvas owns node geometry.
	`
CREATE TABLE conversations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE conversation_providers (
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (conversation_id, provider)
);
CREATE TABLE canvas_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    conversation_id INTEGER REFERENCES conversations(id) ON DELETE CASCADE,
    x REAL NOT NULL,
    y REAL NOT NULL,
    width REAL NOT NULL,
    height REAL NOT NULL,
    z INTEGER NOT NULL DEFAULT 0,
    color TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);
CREATE INDEX canvas_nodes_conversation_idx ON canvas_nodes(conversation_id);
ALTER TABLE chat_turns ADD COLUMN conversation_id INTEGER REFERENCES conversations(id) ON DELETE CASCADE;
CREATE INDEX chat_turns_conversation_idx ON chat_turns(conversation_id, id);
`,
	// 3: the allowance a provider reports about itself, last value wins.
	`
CREATE TABLE provider_quota (
    provider TEXT PRIMARY KEY,
    short_label TEXT NOT NULL DEFAULT '',
    short_utilization REAL NOT NULL DEFAULT 0,
    short_resets_at INTEGER NOT NULL DEFAULT 0,
    long_label TEXT NOT NULL DEFAULT '',
    long_utilization REAL NOT NULL DEFAULT 0,
    long_resets_at INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
`,
	// 4: what a provider is busy with while a response is still running.
	`
ALTER TABLE chat_responses ADD COLUMN activity TEXT NOT NULL DEFAULT '';
`,
	// 5: links relay one card's answer into another as its next message, and
	// relay_depth is what stops two linked cards talking forever.
	`
CREATE TABLE canvas_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL REFERENCES canvas_nodes(id) ON DELETE CASCADE,
    target_id INTEGER NOT NULL REFERENCES canvas_nodes(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    UNIQUE(source_id, target_id)
);
ALTER TABLE chat_turns ADD COLUMN relay_depth INTEGER NOT NULL DEFAULT 0;
`,
	// 6: a card works inside a real project directory, at a chosen access
	// level, and remembers the provider-side session so a turn continues the
	// same conversation instead of starting a new one.
	`
ALTER TABLE conversations ADD COLUMN project_path TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN access TEXT NOT NULL DEFAULT 'edit';
CREATE TABLE provider_sessions (
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    session_id TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (conversation_id, provider)
);
`,
	// 7: links carry a working mode and their own round budget, and a card can
	// run a command after each turn and feed the failure back to itself.
	`
ALTER TABLE canvas_links ADD COLUMN mode TEXT NOT NULL DEFAULT 'relay';
ALTER TABLE canvas_links ADD COLUMN max_rounds INTEGER NOT NULL DEFAULT 3;
ALTER TABLE conversations ADD COLUMN test_command TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN test_rounds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chat_turns ADD COLUMN test_round INTEGER NOT NULL DEFAULT 0;
`,
	// 8: a card runs an ordered list of steps rather than one command, and the
	// loop can keep going instead of stopping at the first success. This is
	// what a hardware cycle needs: flash, listen, check, repeat.
	`
CREATE TABLE card_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    timeout_seconds INTEGER NOT NULL DEFAULT 0,
    UNIQUE(conversation_id, position)
);
ALTER TABLE conversations ADD COLUMN loop_mode TEXT NOT NULL DEFAULT 'off';
ALTER TABLE conversations ADD COLUMN loop_interval_seconds INTEGER NOT NULL DEFAULT 5;
ALTER TABLE conversations ADD COLUMN loop_running INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversations ADD COLUMN loop_due_at TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN loop_last_signature TEXT NOT NULL DEFAULT '';
CREATE TABLE card_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    step_name TEXT NOT NULL DEFAULT '',
    exit_code INTEGER NOT NULL DEFAULT 0,
    output TEXT NOT NULL DEFAULT ''
);
CREATE INDEX card_runs_conversation_idx ON card_runs(conversation_id, id DESC);
`,
	// 9: dialogue links can continue until tests or a provider signal completion.
	// Existing links remain bounded unless this is explicitly enabled.
	`
ALTER TABLE canvas_links ADD COLUMN until_done INTEGER NOT NULL DEFAULT 0;
`,
	// 10: linked cards are briefed once instead of being reminded of the
	// arrangement on every hop. kind separates an ordinary turn from a relayed
	// one and from the nudge that answers a card asking for a human decision;
	// dialogue_state is how a stalled exchange is told apart from a finished one.
	`
ALTER TABLE chat_turns ADD COLUMN kind TEXT NOT NULL DEFAULT 'user';
ALTER TABLE conversations ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN briefed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversations ADD COLUMN dialogue_state TEXT NOT NULL DEFAULT '';
ALTER TABLE canvas_links ADD COLUMN briefing TEXT NOT NULL DEFAULT '';
`,
	// 11: how full a provider's context window was on its last turn. A session
	// that fills up drifts from its role and eventually cannot be resumed at
	// all, so the number is what decides when to restate the role and when to
	// start the session over.
	`
ALTER TABLE provider_sessions ADD COLUMN context_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_sessions ADD COLUMN resets INTEGER NOT NULL DEFAULT 0;
`,
	// 12: the model a card runs a provider on. It is the user's choice and
	// belongs to the card, so it survives the session being recycled — unlike
	// provider_sessions.model, which only records what the last run reported.
	`
CREATE TABLE conversation_models (
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (conversation_id, provider)
);
`,
	// 13: a person can stop a turn that is already under way. The request is
	// written here rather than handed to the worker directly, because the
	// daemon owns runtime state and a client only ever writes to the database.
	`
ALTER TABLE chat_responses ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0;
`,
	// 14: a pipeline is a card on the board rather than something only a
	// terminal can queue. The definition lives here; a queued execution still
	// goes through runs and stages, which the daemon already knows how to run.
	`
CREATE TABLE pipelines (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL DEFAULT '',
    project_path TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE TABLE pipeline_stages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pipeline_id INTEGER NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    UNIQUE(pipeline_id, position)
);
ALTER TABLE canvas_nodes ADD COLUMN pipeline_id INTEGER REFERENCES pipelines(id) ON DELETE CASCADE;
ALTER TABLE runs ADD COLUMN pipeline_id INTEGER REFERENCES pipelines(id) ON DELETE SET NULL;
CREATE INDEX canvas_nodes_pipeline_idx ON canvas_nodes(pipeline_id);
CREATE INDEX runs_pipeline_idx ON runs(pipeline_id, id DESC);
`,
	// 15: what one turn cost, as the provider reported it. Unlike
	// provider_sessions.context_tokens, which grows with a session and is
	// last-value-wins, these are per-response and therefore addable: a week of
	// turns sums to a week of usage.
	`
ALTER TABLE chat_responses ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chat_responses ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
`,
	// 16: a message and everything that follows from it are one run. Turns
	// carry the run they belong to, which is what lets a spreading exchange be
	// counted and followed as one thing rather than as unrelated turns.
	//
	// A join node is a waiting point in that run: it holds what each incoming
	// line said until they have all spoken, then hands the lot on as one
	// message. Its inputs are per-run, because two runs may sit at the same
	// join at once and must not be mixed.
	`
CREATE TABLE flow_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    origin_conversation_id INTEGER REFERENCES conversations(id) ON DELETE SET NULL,
    status TEXT NOT NULL,
    steps INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL DEFAULT ''
);
ALTER TABLE chat_turns ADD COLUMN flow_run_id INTEGER REFERENCES flow_runs(id) ON DELETE SET NULL;
CREATE INDEX chat_turns_flow_idx ON chat_turns(flow_run_id, id);
CREATE TABLE join_inputs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Not a foreign key: a turn from before runs existed carries no run, and
    -- an answer relayed from one must still be able to reach a join. Runs clear
    -- their own parked inputs when they end.
    flow_run_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL REFERENCES canvas_nodes(id) ON DELETE CASCADE,
    source_node_id INTEGER NOT NULL REFERENCES canvas_nodes(id) ON DELETE CASCADE,
    source_title TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    arrived_at TEXT NOT NULL,
    UNIQUE(flow_run_id, node_id, source_node_id)
);
CREATE INDEX join_inputs_node_idx ON join_inputs(node_id, flow_run_id);
`,
	// 17: a trigger starts a flow by itself — on a timer, at a time of day, or
	// when someone presses it. What it runs is whatever the board links to it,
	// so there is no separate description of a flow to keep in step with the
	// canvas: the flow is the subgraph reachable from the trigger.
	`
CREATE TABLE triggers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'manual',
    interval_seconds INTEGER NOT NULL DEFAULT 3600,
    at_time TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 0,
    due_at TEXT NOT NULL DEFAULT '',
    last_fired_at TEXT NOT NULL DEFAULT '',
    last_run_id INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
ALTER TABLE canvas_nodes ADD COLUMN trigger_id INTEGER REFERENCES triggers(id) ON DELETE CASCADE;
CREATE INDEX triggers_due_idx ON triggers(enabled, due_at);
`,
}

func (s *Store) init() error {
	if _, err := s.db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}
	if err := s.migrate(); err != nil {
		return err
	}
	// Anything the previous process left mid-flight is owned by nobody now, so
	// it goes back on the queue.
	_, err := s.db.Exec(`
UPDATE stages SET status = 'queued' WHERE status = 'running';
UPDATE runs SET status = 'queued', updated_at = CURRENT_TIMESTAMP WHERE status = 'running';
UPDATE chat_responses SET status = 'queued', updated_at = CURRENT_TIMESTAMP WHERE status = 'running';
`)
	return err
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", version, len(migrations))
	}
	for index := version; index < len(migrations); index++ {
		if err := s.applyMigration(index + 1); err != nil {
			return fmt.Errorf("migration %d: %w", index+1, err)
		}
	}
	return nil
}

// applyMigration runs one migration and its version bump atomically. PRAGMA
// user_version does not accept a bound parameter, hence the formatted integer.
func (s *Store) applyMigration(version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(migrations[version-1]); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateConversationTurn records a prompt against a conversation and queues one
// response per provider the conversation targets.
func (s *Store) CreateConversationTurn(ctx context.Context, conversationID int64, prompt string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	providers, err := conversationProviders(ctx, tx, conversationID)
	if err != nil {
		return 0, err
	}
	if len(providers) == 0 {
		return 0, fmt.Errorf("conversation %d has no providers", conversationID)
	}
	turnID, err := insertTurn(ctx, tx, conversationID, prompt, providers, domain.TurnUser)
	if err != nil {
		return 0, err
	}
	// A person speaking starts a run: this message and everything that follows
	// from it across the board are one thing, which is what makes the spread
	// something you can watch and stop as a whole.
	runID, err := s.StartFlowRun(ctx, tx, conversationID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE chat_turns SET flow_run_id = ? WHERE id = ?", runID, turnID); err != nil {
		return 0, err
	}
	// A person speaking is the answer a parked exchange was waiting for.
	if _, err := tx.ExecContext(ctx,
		"UPDATE conversations SET dialogue_state = '' WHERE id = ?", conversationID); err != nil {
		return 0, err
	}
	return turnID, tx.Commit()
}

func conversationProviders(ctx context.Context, tx *sql.Tx, conversationID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT provider FROM conversation_providers WHERE conversation_id = ? ORDER BY position",
		conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []string
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func insertTurn(ctx context.Context, tx *sql.Tx, conversationID int64, prompt string, providers []string, kind string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		"INSERT INTO chat_turns(conversation_id, prompt, created_at, kind) VALUES(?, ?, ?, ?)",
		conversationID, prompt, now, kind)
	if err != nil {
		return 0, err
	}
	turnID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, name := range providers {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO chat_responses(turn_id, provider, status, updated_at) VALUES(?, ?, ?, ?)",
			turnID, name, domain.StatusQueued, now); err != nil {
			return 0, err
		}
	}
	return turnID, nil
}

func (s *Store) ClaimChatResponse(ctx context.Context) (*domain.ChatJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var job domain.ChatJob
	var role string
	var contextTokens int
	// The card's own choice and what the live session was last run on are read
	// apart, because a session started on another model has to be dropped
	// rather than resumed.
	var chosenModel, sessionModel string
	err = tx.QueryRowContext(ctx, `
SELECT r.id, r.turn_id, r.provider, t.prompt, COALESCE(t.conversation_id, 0),
       COALESCE(c.project_path, ''), COALESCE(c.access, 'edit'),
       COALESCE(ps.session_id, ''), COALESCE(cm.model, ''), COALESCE(ps.model, ''),
       COALESCE(c.role, ''), COALESCE(ps.context_tokens, 0)
FROM chat_responses r
JOIN chat_turns t ON t.id = r.turn_id
LEFT JOIN conversations c ON c.id = t.conversation_id
LEFT JOIN provider_sessions ps
       ON ps.conversation_id = t.conversation_id AND ps.provider = r.provider
LEFT JOIN conversation_models cm
       ON cm.conversation_id = t.conversation_id AND cm.provider = r.provider
WHERE r.status = ?
  AND r.cancel_requested = 0
  AND NOT EXISTS (
    SELECT 1 FROM chat_responses active
    WHERE active.provider = r.provider AND active.status = ?
  )
ORDER BY r.id LIMIT 1`, domain.StatusQueued, domain.StatusRunning).
		Scan(&job.ResponseID, &job.TurnID, &job.Provider, &job.Prompt, &job.ConversationID,
			&job.ProjectPath, &job.Access, &job.SessionID, &chosenModel, &sessionModel,
			&role, &contextTokens)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE chat_responses SET status = ?, updated_at = ? WHERE id = ? AND status = ?",
		domain.StatusRunning, time.Now().UTC().Format(time.RFC3339Nano), job.ResponseID, domain.StatusQueued)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, fmt.Errorf("claim chat response %d changed %d rows", job.ResponseID, changed)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	job.Model = chosenModel
	if job.Model == "" {
		job.Model = sessionModel
	}
	// A session that has grown past what it can carry is started over, and so is
	// one the user has since pointed at another model. Dropping it here rather
	// than at the end of the previous turn means the decision is made with the
	// window size that turn actually reported, and covers a model changed while
	// that turn was still running.
	staleModel := chosenModel != "" && sessionModel != "" && chosenModel != sessionModel
	if job.SessionID != "" && (contextTokens >= contextResetAt || staleModel) {
		if err := s.recycleSession(context.WithoutCancel(ctx), job.ConversationID, job.Provider); err != nil {
			_ = s.RequeueChatResponse(context.WithoutCancel(ctx), job.ResponseID)
			return nil, err
		}
		job.SessionID = ""
		contextTokens = 0
	}
	// A provider that resumes its own session already holds this conversation's
	// history; replaying a transcript into the prompt would send it twice. The
	// transcript is only what stands in for a session the provider does not
	// keep, or has not started yet.
	if job.SessionID == "" {
		job.Prompt, err = s.chatContext(ctx, job.ConversationID, job.Provider, job.TurnID)
		if err != nil {
			_ = s.RequeueChatResponse(context.WithoutCancel(ctx), job.ResponseID)
			return nil, err
		}
	}
	// Deep into a long session a card drifts from what it was asked to be. One
	// line costs almost nothing next to a window this full, and it is only sent
	// once the window is actually large.
	if role != "" && contextTokens >= contextRemindAt {
		job.Prompt = "Hatırlatma — senin rolün: " + role + "\n\n" + job.Prompt
	}
	return &job, nil
}

// chatContext rebuilds the exchange history this provider has seen inside this
// conversation. Scoping by conversation is what keeps two solo conversations
// with the same provider independent of each other.
func (s *Store) chatContext(ctx context.Context, conversationID int64, provider string, turnID int64) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.prompt, CASE WHEN t.id = ? THEN '' ELSE r.content END
FROM chat_turns t
JOIN chat_responses r ON r.turn_id = t.id AND r.provider = ?
WHERE t.conversation_id = ? AND (t.id = ? OR (t.id < ? AND r.status = ?))
ORDER BY t.id DESC LIMIT 8`, turnID, provider, conversationID, turnID, turnID, domain.StatusPassed)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type exchange struct{ user, assistant string }
	var exchanges []exchange
	for rows.Next() {
		var item exchange
		if err := rows.Scan(&item.user, &item.assistant); err != nil {
			return "", err
		}
		exchanges = append(exchanges, item)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var prompt strings.Builder
	prompt.WriteString("Continue this conversation. Answer the latest user message directly.\n\n")
	for index := len(exchanges) - 1; index >= 0; index-- {
		prompt.WriteString("User: ")
		prompt.WriteString(exchanges[index].user)
		prompt.WriteString("\n")
		if exchanges[index].assistant != "" {
			prompt.WriteString("Assistant: ")
			prompt.WriteString(exchanges[index].assistant)
			prompt.WriteString("\n")
		}
	}
	return prompt.String(), nil
}

// RecordChatUsage stores what one turn cost. It is written apart from the
// answer because a provider reports usage on its own event, which may arrive
// before the run is finished — or, on a failure, instead of a finished answer.
func (s *Store) RecordChatUsage(ctx context.Context, id int64, input, output int) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE chat_responses SET input_tokens = ?, output_tokens = ? WHERE id = ?",
		input, output, id)
	return err
}

func (s *Store) FinishChatResponse(ctx context.Context, id int64, status domain.Status, content, failure string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_responses SET status = ?, content = ?, error = ?, activity = '', updated_at = ? WHERE id = ?`,
		status, content, failure, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) RequeueChatResponse(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE chat_responses SET status = ?, updated_at = ? WHERE id = ?",
		domain.StatusQueued, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// RequestConversationCancel stops what a card is doing. A response that has not
// been claimed yet is finished on the spot; one already running is only flagged,
// because the process belongs to a daemon worker and only that worker can end
// it. The card's cycle is disarmed too: stopping a card should not leave a timer
// about to start the next round.
//
// It returns how many responses were affected, which is zero for an idle card.
func (s *Store) RequestConversationCancel(ctx context.Context, conversationID int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
UPDATE chat_responses SET cancel_requested = 1, updated_at = ?
WHERE status IN (?, ?)
  AND turn_id IN (SELECT id FROM chat_turns WHERE conversation_id = ?)`,
		now, domain.StatusQueued, domain.StatusRunning, conversationID)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Nothing owns a queued response, so it can be finished here and now.
	if _, err := tx.ExecContext(ctx, `
UPDATE chat_responses SET status = ?, activity = '', updated_at = ?
WHERE status = ? AND cancel_requested = 1
  AND turn_id IN (SELECT id FROM chat_turns WHERE conversation_id = ?)`,
		domain.StatusCanceled, now, domain.StatusQueued, conversationID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE conversations SET loop_running = 0 WHERE id = ?", conversationID); err != nil {
		return 0, err
	}
	return int(affected), tx.Commit()
}

// CancelChatResponse finishes a stopped response. It deliberately leaves
// content alone: whatever the provider managed to say was already streamed into
// the row, and a stop should not erase it.
func (s *Store) CancelChatResponse(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_responses SET status = ?, error = '', activity = '', updated_at = ? WHERE id = ?`,
		domain.StatusCanceled, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// ChatCancelRequested reports whether a person asked for this response to stop.
// The worker running it polls this: it is the only channel a client has into a
// process the daemon already started.
func (s *Store) ChatCancelRequested(ctx context.Context, id int64) (bool, error) {
	var requested int
	err := s.db.QueryRowContext(ctx,
		"SELECT cancel_requested FROM chat_responses WHERE id = ?", id).Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return requested != 0, err
}

// conversationTurns returns the most recent turns of one conversation in
// chronological order, which is how a transcript is read.
func (s *Store) conversationTurns(ctx context.Context, conversationID int64, limit int) ([]domain.ChatTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, prompt, created_at, COALESCE(kind, 'user') FROM chat_turns
WHERE conversation_id = ? ORDER BY id DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	turns := []domain.ChatTurn{}
	for rows.Next() {
		var turn domain.ChatTurn
		var created string
		if err := rows.Scan(&turn.ID, &turn.Prompt, &created, &turn.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		turn.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		turns = append(turns, turn)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// The query walks backwards to honour LIMIT; the reader wants oldest first.
	for left, right := 0, len(turns)-1; left < right; left, right = left+1, right-1 {
		turns[left], turns[right] = turns[right], turns[left]
	}
	for index := range turns {
		responses, err := s.chatResponses(ctx, turns[index].ID)
		if err != nil {
			return nil, err
		}
		turns[index].Responses = responses
	}
	return turns, nil
}

func (s *Store) chatResponses(ctx context.Context, turnID int64) ([]domain.ChatResponse, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, turn_id, provider, status, content, error, activity
FROM chat_responses WHERE turn_id = ? ORDER BY id`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var responses []domain.ChatResponse
	for rows.Next() {
		var response domain.ChatResponse
		if err := rows.Scan(&response.ID, &response.TurnID, &response.Provider,
			&response.Status, &response.Content, &response.Error, &response.Activity); err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, rows.Err()
}

func (s *Store) CreateRun(ctx context.Context, request domain.RunRequest) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		"INSERT INTO runs(project, status, created_at, updated_at) VALUES(?, ?, ?, ?)",
		request.Project, domain.StatusQueued, now, now)
	if err != nil {
		return 0, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for position, stage := range request.Stages {
		command, err := json.Marshal(stage.Command)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO stages(run_id, position, name, command_json, status) VALUES(?, ?, ?, ?, ?)",
			runID, position, stage.Name, string(command), domain.StatusQueued); err != nil {
			return 0, err
		}
	}
	return runID, tx.Commit()
}

func (s *Store) ClaimRun(ctx context.Context) (*domain.Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM runs WHERE status = ? ORDER BY id LIMIT 1", domain.StatusQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE runs SET status = ?, updated_at = ? WHERE id = ? AND status = ?",
		domain.StatusRunning, time.Now().UTC().Format(time.RFC3339Nano), id, domain.StatusQueued)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, fmt.Errorf("claim run %d changed %d rows", id, changed)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, id)
}

func (s *Store) GetRun(ctx context.Context, id int64) (*domain.Run, error) {
	var run domain.Run
	var created, updated string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, project, status, created_at, updated_at FROM runs WHERE id = ?", id).
		Scan(&run.ID, &run.Project, &run.Status, &created, &updated)
	if err != nil {
		return nil, err
	}
	run.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	run.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	stages, err := s.stages(ctx, id)
	if err != nil {
		return nil, err
	}
	run.Stages = stages
	return &run, nil
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM runs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	runs := make([]domain.Run, 0, len(ids))
	for _, id := range ids {
		run, err := s.GetRun(ctx, id)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	return runs, nil
}

func (s *Store) SetStageResult(ctx context.Context, stageID int64, status domain.Status, exitCode int, output string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE stages SET status = ?, exit_code = ?, output = ? WHERE id = ?",
		status, exitCode, output, stageID)
	return err
}

func (s *Store) SetStageRunning(ctx context.Context, stageID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE stages SET status = ? WHERE id = ?", domain.StatusRunning, stageID)
	return err
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status domain.Status) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE runs SET status = ?, updated_at = ? WHERE id = ?",
		status, time.Now().UTC().Format(time.RFC3339Nano), runID)
	return err
}

func (s *Store) RequeueRun(ctx context.Context, runID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"UPDATE stages SET status = ? WHERE run_id = ? AND status = ?",
		domain.StatusQueued, runID, domain.StatusRunning); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE runs SET status = ?, updated_at = ? WHERE id = ?",
		domain.StatusQueued, time.Now().UTC().Format(time.RFC3339Nano), runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BlockRemaining(ctx context.Context, runID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE stages SET status = ? WHERE run_id = ? AND status = ?",
		domain.StatusBlocked, runID, domain.StatusQueued)
	return err
}

func (s *Store) stages(ctx context.Context, runID int64) ([]domain.Stage, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, run_id, position, name, command_json, status, exit_code, output FROM stages WHERE run_id = ? ORDER BY position",
		runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stages []domain.Stage
	for rows.Next() {
		var stage domain.Stage
		var command string
		var exitCode sql.NullInt64
		if err := rows.Scan(&stage.ID, &stage.RunID, &stage.Position, &stage.Name, &command, &stage.Status, &exitCode, &stage.Output); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(command), &stage.Command); err != nil {
			return nil, fmt.Errorf("decode stage %d: %w", stage.ID, err)
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			stage.ExitCode = &value
		}
		stages = append(stages, stage)
	}
	return stages, rows.Err()
}

// Default node geometry. Clients may resize afterwards; these only decide where
// a freshly created node lands.
// transcriptLimit caps how much history the canvas carries per conversation.
const transcriptLimit = 24

const (
	triggerWidth       = 320
	triggerHeight      = 300
	conversationWidth  = 420
	conversationHeight = 340
	noteWidth          = 240
	noteHeight         = 180
	pipelineWidth      = 360
	pipelineHeight     = 300
	joinWidth          = 220
	joinHeight         = 150
)

// CreateConversation stores a conversation together with the canvas node that
// represents it, so the board never holds a conversation with no node.
func (s *Store) CreateConversation(ctx context.Context, request domain.NewConversation) (domain.Conversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Conversation{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	access := request.Access
	if access == "" {
		access = string(provider.AccessEdit)
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO conversations(title, kind, created_at, project_path, access) VALUES(?, ?, ?, ?, ?)",
		request.Title, request.Kind, now, request.ProjectPath, access)
	if err != nil {
		return domain.Conversation{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Conversation{}, err
	}
	for position, provider := range request.Providers {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO conversation_providers(conversation_id, provider, position) VALUES(?, ?, ?)",
			id, provider, position); err != nil {
			return domain.Conversation{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, conversation_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
		domain.NodeConversation, id, request.X, request.Y,
		conversationWidth, conversationHeight, now); err != nil {
		return domain.Conversation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Conversation{}, err
	}
	created, _ := time.Parse(time.RFC3339Nano, now)
	return domain.Conversation{
		ID: id, Title: request.Title, Kind: request.Kind,
		Providers: request.Providers, CreatedAt: created,
		ProjectPath: request.ProjectPath, Access: access,
	}, nil
}

// CreateNote adds a standalone sticky note to the canvas.
func (s *Store) CreateNote(ctx context.Context, request domain.NewNote) (domain.CanvasNode, error) {
	return s.createNote(ctx, request, noteWidth, noteHeight)
}

func (s *Store) createNote(ctx context.Context, request domain.NewNote, width, height float64) (domain.CanvasNode, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, x, y, width, height, z, color, body, updated_at)
VALUES(?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?, ?, ?)`,
		domain.NodeNote, request.X, request.Y, width, height,
		request.Color, request.Body, now)
	if err != nil {
		return domain.CanvasNode{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.CanvasNode{}, err
	}
	return domain.CanvasNode{
		ID: id, Kind: domain.NodeNote, X: request.X, Y: request.Y,
		Width: width, Height: height, Color: request.Color, Body: request.Body,
	}, nil
}

// BranchFrom starts new conversations from an answer that already exists. The
// answer becomes each new card's first message and a link is drawn back to the
// card it came from, so a board shows where a line of work forked.
//
// One provider per card, not one card with several providers: the point of a
// branch is that the paths diverge, and a group card would merge them again.
func (s *Store) BranchFrom(ctx context.Context, sourceConversationID int64, answer string, providers []string) ([]domain.Conversation, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil, errors.New("a branch needs an answer to start from")
	}
	if len(providers) == 0 {
		return nil, errors.New("a branch needs at least one provider")
	}
	x, y, err := s.branchPosition(ctx, sourceConversationID)
	if err != nil {
		return nil, err
	}
	sourceNode, err := s.nodeOfConversation(ctx, sourceConversationID)
	if err != nil {
		return nil, err
	}
	branches := make([]domain.Conversation, 0, len(providers))
	for index, name := range providers {
		conversation, err := s.CreateConversation(ctx, domain.NewConversation{
			Title:     name,
			Kind:      domain.KindSolo,
			Providers: []string{name},
			X:         x + float64(index)*(conversationWidth+30),
			Y:         y,
		})
		if err != nil {
			return branches, err
		}
		if _, err := s.CreateConversationTurn(ctx, conversation.ID, answer); err != nil {
			return branches, err
		}
		// The link records where the branch came from. It stays a plain relay:
		// a branch is a fork, not a conversation back to the parent.
		if sourceNode != 0 {
			node, err := s.nodeOfConversation(ctx, conversation.ID)
			if err != nil {
				return branches, err
			}
			if _, err := s.CreateLink(ctx, sourceNode, node,
				domain.LinkOptions{Mode: domain.LinkRelay, MaxRounds: 1}.Normalised()); err != nil {
				return branches, err
			}
		}
		branches = append(branches, conversation)
	}
	return branches, nil
}

func (s *Store) nodeOfConversation(ctx context.Context, conversationID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM canvas_nodes WHERE conversation_id = ? AND kind = ?",
		conversationID, domain.NodeConversation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// branchPosition puts the new cards below the one they came from.
func (s *Store) branchPosition(ctx context.Context, conversationID int64) (float64, float64, error) {
	var x, y, height float64
	err := s.db.QueryRowContext(ctx,
		"SELECT x, y, height FROM canvas_nodes WHERE conversation_id = ? AND kind = ?",
		conversationID, domain.NodeConversation).Scan(&x, &y, &height)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	return x, y + height + 60, nil
}

// Canvas returns every conversation and node needed to draw the board.
func (s *Store) Canvas(ctx context.Context) (domain.Canvas, error) {
	canvas := domain.Canvas{
		Conversations: []domain.Conversation{},
		Pipelines:     []domain.Pipeline{},
		Nodes:         []domain.CanvasNode{},
		Links:         []domain.CanvasLink{},
		Joins:         []domain.JoinNode{},
		Triggers:      []domain.Trigger{},
		Runs:          []domain.FlowRun{},
	}
	conversations, err := s.listConversations(ctx)
	if err != nil {
		return canvas, err
	}
	for index := range conversations {
		turns, err := s.conversationTurns(ctx, conversations[index].ID, transcriptLimit)
		if err != nil {
			return canvas, err
		}
		conversations[index].Turns = turns
		steps, err := s.loopSteps(ctx, conversations[index].ID)
		if err != nil {
			return canvas, err
		}
		conversations[index].Loop.Steps = steps
		runs, err := s.recentCardRuns(ctx, conversations[index].ID)
		if err != nil {
			return canvas, err
		}
		conversations[index].Runs = runs
	}
	canvas.Conversations = conversations
	pipelines, err := s.listPipelines(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Pipelines = pipelines
	nodes, err := s.listCanvasNodes(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Nodes = nodes
	links, err := s.listLinks(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Links = links
	joins, err := s.listJoins(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Joins = joins
	triggers, err := s.listTriggers(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Triggers = triggers
	runs, err := s.ActiveFlowRuns(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Runs = runs
	return canvas, nil
}

func (s *Store) listConversations(ctx context.Context) ([]domain.Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, kind, created_at, project_path, access,
		        COALESCE(test_rounds, 0),
		        COALESCE(loop_mode, 'off'), COALESCE(loop_interval_seconds, 5),
		        COALESCE(loop_running, 0),
		        COALESCE(role, ''), COALESCE(dialogue_state, '')
		 FROM conversations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	conversations := []domain.Conversation{}
	index := make(map[int64]int)
	for rows.Next() {
		var item domain.Conversation
		var created string
		var loopRunning int
		var notify int
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &created,
			&item.ProjectPath, &item.Access, &notify,
			&item.Loop.Mode, &item.Loop.IntervalSeconds, &loopRunning,
			&item.Role, &item.DialogueState); err != nil {
			rows.Close()
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.Loop.NotifyOnFailure = notify != 0
		item.LoopRunning = loopRunning != 0
		item.Providers = []string{}
		index[item.ID] = len(conversations)
		conversations = append(conversations, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// One pass over the join table beats a query per conversation.
	providerRows, err := s.db.QueryContext(ctx,
		"SELECT conversation_id, provider FROM conversation_providers ORDER BY conversation_id, position")
	if err != nil {
		return nil, err
	}
	defer providerRows.Close()
	for providerRows.Next() {
		var conversationID int64
		var provider string
		if err := providerRows.Scan(&conversationID, &provider); err != nil {
			return nil, err
		}
		if at, known := index[conversationID]; known {
			conversations[at].Providers = append(conversations[at].Providers, provider)
		}
	}
	if err := providerRows.Err(); err != nil {
		return nil, err
	}
	models, err := s.conversationModels(ctx)
	if err != nil {
		return nil, err
	}
	for id, chosen := range models {
		if at, known := index[id]; known {
			conversations[at].Models = chosen
		}
	}
	return conversations, nil
}

func (s *Store) listCanvasNodes(ctx context.Context) ([]domain.CanvasNode, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, conversation_id, pipeline_id, trigger_id, x, y, width, height, z, color, body
FROM canvas_nodes ORDER BY z, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []domain.CanvasNode{}
	for rows.Next() {
		var node domain.CanvasNode
		var conversationID, pipelineID, triggerID sql.NullInt64
		if err := rows.Scan(&node.ID, &node.Kind, &conversationID, &pipelineID, &triggerID,
			&node.X, &node.Y, &node.Width, &node.Height, &node.Z, &node.Color, &node.Body); err != nil {
			return nil, err
		}
		if conversationID.Valid {
			value := conversationID.Int64
			node.ConversationID = &value
		}
		if pipelineID.Valid {
			value := pipelineID.Int64
			node.PipelineID = &value
		}
		if triggerID.Valid {
			value := triggerID.Int64
			node.TriggerID = &value
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// PatchCanvasNode updates only the supplied fields. Returns sql.ErrNoRows when
// the node is gone, so a stale client learns instead of silently succeeding.
func (s *Store) PatchCanvasNode(ctx context.Context, patch domain.CanvasNodePatch) error {
	assignments := make([]string, 0, 7)
	arguments := make([]any, 0, 9)
	addFloat := func(column string, value *float64) {
		if value != nil {
			assignments = append(assignments, column+" = ?")
			arguments = append(arguments, *value)
		}
	}
	addFloat("x", patch.X)
	addFloat("y", patch.Y)
	addFloat("width", patch.Width)
	addFloat("height", patch.Height)
	if patch.Z != nil {
		assignments = append(assignments, "z = ?")
		arguments = append(arguments, *patch.Z)
	}
	if patch.Color != nil {
		assignments = append(assignments, "color = ?")
		arguments = append(arguments, *patch.Color)
	}
	if patch.Body != nil {
		assignments = append(assignments, "body = ?")
		arguments = append(arguments, *patch.Body)
	}
	if len(assignments) == 0 {
		return nil
	}
	assignments = append(assignments, "updated_at = ?")
	arguments = append(arguments, time.Now().UTC().Format(time.RFC3339Nano), patch.ID)
	result, err := s.db.ExecContext(ctx,
		"UPDATE canvas_nodes SET "+strings.Join(assignments, ", ")+" WHERE id = ?", arguments...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteCanvasNode removes a node and, for a conversation node, the
// conversation and its history along with it.
func (s *Store) DeleteCanvasNode(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	var conversationID, pipelineID, triggerID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		"SELECT kind, conversation_id, pipeline_id, trigger_id FROM canvas_nodes WHERE id = ?", id).
		Scan(&kind, &conversationID, &pipelineID, &triggerID)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM canvas_nodes WHERE id = ?", id); err != nil {
		return err
	}
	if kind == domain.NodeConversation && conversationID.Valid {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM conversations WHERE id = ?", conversationID.Int64); err != nil {
			return err
		}
	}
	// The node is how a pipeline is reached, so closing the card takes the
	// definition with it. A pipeline with no node would be unreachable state.
	if kind == domain.NodePipeline && pipelineID.Valid {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM pipelines WHERE id = ?", pipelineID.Int64); err != nil {
			return err
		}
	}
	// Same for a trigger: closing the card is how a routine is switched off for
	// good, and a trigger left behind would keep firing into nothing.
	if kind == domain.NodeTrigger && triggerID.Valid {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM triggers WHERE id = ?", triggerID.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateChatResponseContent stores partial output while a provider is still
// answering. It refuses to touch a response that is no longer running, so a
// late write cannot overwrite a finished answer or revive a requeued one.
func (s *Store) UpdateChatResponseContent(ctx context.Context, id int64, content, activity string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_responses SET content = ?, activity = ?, updated_at = ?
WHERE id = ? AND status = ?`,
		content, activity, time.Now().UTC().Format(time.RFC3339Nano), id, domain.StatusRunning)
	return err
}

// RecordProviderQuota stores the most recent allowance a provider reported.
// Only some providers report one, so a missing row simply means "unknown".
func (s *Store) RecordProviderQuota(ctx context.Context, name string, quota provider.Quota) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO provider_quota(provider, short_label, short_utilization, short_resets_at,
                           long_label, long_utilization, long_resets_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider) DO UPDATE SET
    short_label = excluded.short_label,
    short_utilization = excluded.short_utilization,
    short_resets_at = excluded.short_resets_at,
    long_label = excluded.long_label,
    long_utilization = excluded.long_utilization,
    long_resets_at = excluded.long_resets_at,
    updated_at = excluded.updated_at`,
		name, quota.ShortLabel, quota.ShortUtilization, quota.ShortResetsAt,
		quota.LongLabel, quota.LongUtilization, quota.LongResetsAt,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ProviderQuota returns the last reported allowance per provider name.
func (s *Store) ProviderQuota(ctx context.Context) (map[string]domain.Quota, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT provider, short_label, short_utilization, short_resets_at,
       long_label, long_utilization, long_resets_at
FROM provider_quota`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	quota := make(map[string]domain.Quota)
	for rows.Next() {
		var name string
		var item domain.Quota
		if err := rows.Scan(&name, &item.ShortLabel, &item.ShortUtilization, &item.ShortResetsAt,
			&item.LongLabel, &item.LongUtilization, &item.LongResetsAt); err != nil {
			return nil, err
		}
		quota[name] = item
	}
	return quota, rows.Err()
}

// maxRelayDepth bounds how far an answer travels along links. Two cards
// pointing at each other would otherwise talk until the quota runs out.
const maxRelayDepth = 3

// CreateLink connects two canvas nodes so the source's answers are relayed into
// the target. Self-links are refused; a card must not answer itself.
func (s *Store) CreateLink(ctx context.Context, sourceID, targetID int64, options domain.LinkOptions) (domain.CanvasLink, error) {
	if sourceID == targetID {
		return domain.CanvasLink{}, errors.New("a card cannot be linked to itself")
	}
	options = options.Normalised()
	// A link carries an answer, so both ends must be something an answer can
	// travel through: a card that speaks, or a join that waits for several.
	var linkable int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM canvas_nodes
WHERE id IN (?, ?)
  AND ((kind = ? AND conversation_id IS NOT NULL) OR kind IN (?, ?))`,
		sourceID, targetID, domain.NodeConversation, domain.NodeJoin, domain.NodeTrigger).Scan(&linkable)
	if err != nil {
		return domain.CanvasLink{}, err
	}
	if linkable != 2 {
		return domain.CanvasLink{}, errors.New("only conversation cards, joins and triggers can be linked")
	}
	// A trigger starts a flow; nothing flows back into one. Accepting such a
	// link would draw a line on the board that never carries anything.
	var intoTrigger int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM canvas_nodes WHERE id = ? AND kind = ?",
		targetID, domain.NodeTrigger).Scan(&intoTrigger); err != nil {
		return domain.CanvasLink{}, err
	}
	if intoTrigger > 0 {
		return domain.CanvasLink{}, errors.New("a trigger starts a flow; nothing links into one")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
INSERT INTO canvas_links(source_id, target_id, created_at, mode, max_rounds, until_done, briefing)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id, target_id) DO UPDATE SET
    mode = excluded.mode,
    max_rounds = excluded.max_rounds,
    until_done = excluded.until_done,
    briefing = excluded.briefing`,
		sourceID, targetID, now, options.Mode, options.MaxRounds, options.UntilDone, options.Briefing)
	if err != nil {
		return domain.CanvasLink{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.CanvasLink{}, err
	}
	if id == 0 {
		// The link already existed; report the one that is stored.
		err = s.db.QueryRowContext(ctx,
			"SELECT id FROM canvas_links WHERE source_id = ? AND target_id = ?",
			sourceID, targetID).Scan(&id)
		if err != nil {
			return domain.CanvasLink{}, err
		}
	}
	// "Dialogue" only means anything if the other card can answer back, so the
	// return link is created with it rather than left for the user to notice.
	// A join has nothing to answer with and a trigger only starts things, so a
	// link through either of them stays one-way.
	oneWay, err := s.linkMustBeOneWay(ctx, sourceID, targetID)
	if err != nil {
		return domain.CanvasLink{}, err
	}
	if options.Mode == domain.LinkDialogue && !oneWay {
		if err := s.ensureReverseLink(ctx, sourceID, targetID, options); err != nil {
			return domain.CanvasLink{}, err
		}
	}
	return domain.CanvasLink{
		ID: id, SourceID: sourceID, TargetID: targetID,
		Mode: options.Mode, MaxRounds: options.MaxRounds, UntilDone: options.UntilDone,
		Briefing: options.Briefing,
	}, nil
}

// ensureReverseLink adds target -> source when it is missing, leaving an
// existing reverse link alone apart from matching its mode and budget.
func (s *Store) ensureReverseLink(ctx context.Context, sourceID, targetID int64, options domain.LinkOptions) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO canvas_links(source_id, target_id, created_at, mode, max_rounds, until_done, briefing)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id, target_id) DO UPDATE SET
    mode = excluded.mode,
    max_rounds = excluded.max_rounds,
    until_done = excluded.until_done,
    briefing = excluded.briefing`,
		targetID, sourceID, time.Now().UTC().Format(time.RFC3339Nano),
		options.Mode, options.MaxRounds, options.UntilDone, options.Briefing)
	return err
}

// PairNodes links two cards in both directions so they answer each other. The
// round budget is shared: each link stops the exchange after MaxRounds hops.
func (s *Store) PairNodes(ctx context.Context, firstID, secondID int64, options domain.LinkOptions) ([]domain.CanvasLink, error) {
	forward, err := s.CreateLink(ctx, firstID, secondID, options)
	if err != nil {
		return nil, err
	}
	backward, err := s.CreateLink(ctx, secondID, firstID, options)
	if err != nil {
		return nil, err
	}
	return []domain.CanvasLink{forward, backward}, nil
}

// UpdateLink changes how an existing link works.
func (s *Store) UpdateLink(ctx context.Context, id int64, options domain.LinkOptions) error {
	options = options.Normalised()
	var sourceID, targetID int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT source_id, target_id FROM canvas_links WHERE id = ?", id).
		Scan(&sourceID, &targetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE canvas_links SET mode = ?, max_rounds = ?, until_done = ?, briefing = ? WHERE id = ?",
		options.Mode, options.MaxRounds, options.UntilDone, options.Briefing, id); err != nil {
		return err
	}
	// A changed arrangement is worth explaining again, so both cards are
	// re-briefed before their next relayed message.
	if _, err := s.db.ExecContext(ctx, `
UPDATE conversations SET briefed = 0 WHERE id IN (
    SELECT conversation_id FROM canvas_nodes
    WHERE id IN (?, ?) AND conversation_id IS NOT NULL
)`, sourceID, targetID); err != nil {
		return err
	}
	// Choosing "dialogue" on a one-way link is a request for a conversation,
	// which needs the return link too.
	if options.Mode == domain.LinkDialogue {
		return s.ensureReverseLink(ctx, sourceID, targetID, options)
	}
	return nil
}

func (s *Store) DeleteLink(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM canvas_links WHERE id = ?", id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) listLinks(ctx context.Context) ([]domain.CanvasLink, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, source_id, target_id, mode, max_rounds, until_done, COALESCE(briefing, '') FROM canvas_links ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []domain.CanvasLink{}
	for rows.Next() {
		var link domain.CanvasLink
		if err := rows.Scan(&link.ID, &link.SourceID, &link.TargetID, &link.Mode, &link.MaxRounds, &link.UntilDone, &link.Briefing); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// RelayPayload reports the finished text of a turn, but only once every
// provider in it has stopped. Relaying per response would fire several times
// for a group card; relaying per turn fires once with the whole answer.
func (s *Store) RelayPayload(ctx context.Context, turnID int64) (string, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT provider, status, content FROM chat_responses WHERE turn_id = ? ORDER BY id", turnID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var parts []string
	multiple := 0
	for rows.Next() {
		var name, status, content string
		if err := rows.Scan(&name, &status, &content); err != nil {
			return "", false, err
		}
		if status == string(domain.StatusQueued) || status == string(domain.StatusRunning) {
			return "", false, nil
		}
		// A stopped turn is not an answer. Relaying what a provider had
		// managed to say before a person cut it off would carry the
		// interruption on to the next card.
		if status == string(domain.StatusCanceled) {
			return "", false, nil
		}
		multiple++
		if status == string(domain.StatusPassed) && strings.TrimSpace(content) != "" {
			parts = append(parts, name+": "+strings.TrimSpace(content))
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(parts) == 0 {
		return "", false, nil
	}
	if multiple == 1 {
		// A single provider needs no attribution prefix.
		_, answer, _ := strings.Cut(parts[0], ": ")
		return answer, true, nil
	}
	return strings.Join(parts, "\n\n"), true, nil
}

// RelayTurn forwards a finished answer to every card linked from this one.
// It returns the number of conversations that received it.
// RelayTurn forwards a finished answer to every card linked from this one,
// framed according to each link's mode. Each link carries its own round budget,
// which is what stops a pair of cards from talking forever.
func (s *Store) RelayTurn(ctx context.Context, turnID int64) (int, error) {
	var conversationID int64
	var depth int
	var sourceTitle, sourceKind string
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(t.conversation_id, 0), t.relay_depth, COALESCE(c.title, ''), t.kind
FROM chat_turns t LEFT JOIN conversations c ON c.id = t.conversation_id
WHERE t.id = ?`, turnID).Scan(&conversationID, &depth, &sourceTitle, &sourceKind)
	if err != nil {
		return 0, err
	}
	if conversationID == 0 {
		return 0, nil
	}
	runID, err := s.runOfTurn(ctx, turnID)
	if err != nil {
		return 0, err
	}
	payload, ready, err := s.RelayPayload(ctx, turnID)
	if err != nil {
		return 0, err
	}
	if !ready {
		// A turn that was stopped, or one still waiting on a sibling provider,
		// hands nothing on. The first of those can be the last thing a run
		// does, so the run still gets a chance to close.
		return 0, s.maybeFinishRun(ctx, runID)
	}
	sourceNodeID, err := s.nodeOfConversation(ctx, conversationID)
	if err != nil {
		return 0, err
	}

	delivered, err := s.relayFrom(ctx, relaySource{
		runID:          runID,
		nodeID:         sourceNodeID,
		conversationID: conversationID,
		title:          sourceTitle,
		turnKind:       sourceKind,
		payload:        payload,
		depth:          depth,
	})
	if err != nil {
		return delivered, err
	}
	// The run ends when the board falls quiet, not when one card stops: a
	// branch that finishes early must not close a run its sibling is still in.
	if err := s.maybeFinishRun(ctx, runID); err != nil {
		return delivered, err
	}
	return delivered, nil
}

// relaySource is one card's finished answer, on its way to whatever the board
// links it to. It carries the run so everything downstream stays part of the
// same journey, and the node rather than the conversation because a join has no
// conversation of its own.
type relaySource struct {
	runID          int64
	nodeID         int64
	conversationID int64
	title          string
	turnKind       string
	payload        string
	depth          int
	hop            int
}

// maxJoinHops bounds a chain of waiting points. Joins produce no turns, so the
// run budget never sees them; a board wired join to join in a circle would
// otherwise recurse without ever spending a step.
const maxJoinHops = 8

// relayFrom hands one answer to everything a node links to. A conversation
// receives it as a turn; a join parks it until every line feeding that join has
// spoken, and only then does the combined message travel on.
func (s *Store) relayFrom(ctx context.Context, src relaySource) (int, error) {
	if src.nodeID == 0 || src.hop > maxJoinHops {
		return 0, nil
	}
	targets, err := s.relayTargets(ctx, src.nodeID)
	if err != nil {
		return 0, err
	}

	delivered := 0
	for _, target := range targets {
		if target.kind == domain.NodeJoin {
			combined, ready, err := s.deliverToJoin(
				ctx, src.runID, target.nodeID, src.nodeID, src.title, src.payload)
			if err != nil {
				return delivered, err
			}
			if !ready {
				continue
			}
			count, err := s.relayFrom(ctx, relaySource{
				runID:   src.runID,
				nodeID:  target.nodeID,
				title:   target.title,
				payload: combined,
				depth:   src.depth + 1,
				hop:     src.hop + 1,
			})
			delivered += count
			if err != nil {
				return delivered, err
			}
			continue
		}
		if target.conversation == 0 {
			continue
		}

		kind := domain.TurnRelay
		if src.turnKind == domain.TurnTrigger {
			// A message a trigger sent is not an answer being passed along, and
			// the card's transcript should say which it was.
			kind = domain.TurnTrigger
		}
		nudge := false
		// An exchange that runs until it is done is a property of two talking
		// cards. A message arriving from a join came from several at once, so
		// there is no exchange to read an outcome from.
		if target.untilDone && src.conversationID != 0 {
			outcome, err := s.dialogueOutcome(ctx, src.conversationID, target.conversation, src.payload, src.turnKind)
			if err != nil {
				return delivered, err
			}
			switch outcome {
			case dialogueOutcomeDone, dialogueOutcomeParked:
				// The exchange is over. Record which of the two it was, so the
				// card can say "finished" or "waiting for you" rather than
				// simply falling silent.
				state := domain.DialogueDone
				if outcome == dialogueOutcomeParked {
					state = domain.DialogueWaiting
				}
				if err := s.setDialogueState(ctx, src.conversationID, target.conversation, state); err != nil {
					return delivered, err
				}
				// The result of an exchange is buried at the bottom of a card
				// nobody scrolled to. It goes on the board instead, next to the
				// two cards that produced it.
				if err := s.createOutcomeNote(ctx, src.conversationID, target.conversation, src.title, src.payload, state); err != nil {
					return delivered, err
				}
				continue
			case dialogueOutcomeNudge:
				// One card asked for a decision. Rather than stopping the whole
				// exchange on a single question, the other card gets one chance
				// to answer it on the user's behalf.
				kind = domain.TurnNudge
				nudge = true
			}
		}
		// Each link decides for itself when the exchange has gone far enough.
		if !target.untilDone && src.depth >= target.maxRounds {
			continue
		}
		// The run budget is the width limit the per-link round count cannot be:
		// it counts every turn the whole spread produces, not the length of one
		// path through it.
		allowed, err := s.countRunStep(ctx, src.runID)
		if err != nil {
			return delivered, err
		}
		if !allowed {
			return delivered, nil
		}
		briefing, err := s.takeBriefing(ctx, target.conversation, target.briefing, src.title, target.mode, target.untilDone)
		if err != nil {
			return delivered, err
		}
		prompt := framePayload(target.mode, src.title, src.payload, briefing, nudge)
		if err := s.createRelayTurn(ctx, target.conversation, prompt, src.depth+1, kind, src.runID); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

// framePayload presents a relayed answer the way the link's mode intends. Once
// a card has been briefed it receives the answer plainly, prefixed only by who
// said it: repeating the arrangement on every hop wastes tokens and reads like
// the two cards keep being introduced to each other.
func framePayload(mode, sourceTitle, payload, briefing string, nudge bool) string {
	// Naming the speaker is what makes the two cards aware of each other
	// instead of receiving anonymous text.
	speaker := sourceTitle
	if speaker == "" {
		speaker = "Bağlı olduğun kart"
	}
	var message string
	switch mode {
	case domain.LinkDialogue:
		message = speaker + ": " + payload
	case domain.LinkReview:
		message = speaker + " kartının çıktısı aşağıda. İncele; hata, eksik veya risk " +
			"görüyorsan açıkça yaz, sorun yoksa kısaca uygun olduğunu söyle.\n\n" + payload
	default:
		message = payload
	}
	if nudge {
		message += "\n\n" + nudgeInstruction
	}
	if briefing == "" {
		return message
	}
	return briefing + "\n\n---\n\n" + message
}

// nudgeInstruction answers a card that stopped for a decision. Nobody is
// watching a dialogue turn by turn, so the other card decides once and says
// what it assumed; only a second question in a row parks the exchange.
const nudgeInstruction = "Karşındaki kart senden bir karar bekliyor. Şu an cevaplayacak bir " +
	"kullanıcı yok: en makul varsayımı kendin seç, ne varsaydığını tek cümleyle yaz ve işe devam et. " +
	"Yalnızca bu karar gerçekten insana aitse (geri alınamaz bir işlem, dışarıya açılan bir değişiklik " +
	"veya senin bilemeyeceğin bir tercih) [CONCLAVE_USER_INPUT] ile bitir."

const (
	dialogueDoneMarker      = "[CONCLAVE_DONE]"
	dialogueUserInputMarker = "[CONCLAVE_USER_INPUT]"
)

// How an exchange should continue after one card has spoken.
type dialogueOutcome int

const (
	// dialogueOutcomeContinue relays the answer as an ordinary turn.
	dialogueOutcomeContinue dialogueOutcome = iota
	// dialogueOutcomeDone stops because the work is finished.
	dialogueOutcomeDone
	// dialogueOutcomeParked stops because a decision really does need a person.
	dialogueOutcomeParked
	// dialogueOutcomeNudge relays the answer with one push to decide and go on.
	dialogueOutcomeNudge
)

// dialogueOutcome combines objective test completion with explicit provider
// terminal states. If both cards define until-pass loops, every loop must pass.
//
// A request for user input is not by itself the end of an exchange: a card that
// simply opens with a question would otherwise stop the dialogue before it
// started. The first such request is answered with a nudge; only when the card
// asks again after being nudged is the exchange parked for the user.
func (s *Store) dialogueOutcome(ctx context.Context, sourceID, targetID int64, payload, sourceKind string) (dialogueOutcome, error) {
	trimmed := strings.TrimSpace(payload)
	if strings.HasSuffix(trimmed, dialogueDoneMarker) {
		return dialogueOutcomeDone, nil
	}
	if strings.HasSuffix(trimmed, dialogueUserInputMarker) {
		if sourceKind == domain.TurnNudge {
			return dialogueOutcomeParked, nil
		}
		return dialogueOutcomeNudge, nil
	}
	var configured, passed int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(CASE WHEN loop_last_signature = 'passed' THEN 1 ELSE 0 END), 0)
FROM conversations
WHERE id IN (?, ?) AND loop_mode = ?`, sourceID, targetID, domain.LoopUntilPass).
		Scan(&configured, &passed)
	if err != nil {
		return dialogueOutcomeContinue, err
	}
	if configured > 0 && passed == configured {
		return dialogueOutcomeDone, nil
	}
	return dialogueOutcomeContinue, nil
}

// outcomeWidth and outcomeHeight give a result card more room than an ordinary
// note: it holds a whole answer, and being able to read it without opening
// anything is the entire point of putting it on the board.
const (
	outcomeWidth  = 380
	outcomeHeight = 260
)

// createOutcomeNote puts the result of a finished exchange on the board, below
// and between the two cards that produced it. It carries the last answer, who
// was talking and how long it took, so the conclusion is readable without
// scrolling either card.
func (s *Store) createOutcomeNote(ctx context.Context, sourceID, targetID int64, speaker, payload, state string) error {
	var targetTitle string
	if err := s.db.QueryRowContext(ctx,
		"SELECT title FROM conversations WHERE id = ?", targetID).Scan(&targetTitle); err != nil {
		return err
	}
	rounds, err := s.exchangeRounds(ctx, sourceID, targetID)
	if err != nil {
		return err
	}
	x, y, err := s.outcomePosition(ctx, sourceID, targetID)
	if err != nil {
		return err
	}
	note, err := s.createNote(ctx, domain.NewNote{
		Body:  outcomeBody(speaker, targetTitle, payload, state, rounds),
		Color: outcomeColor(state),
		X:     x,
		Y:     y,
	}, outcomeWidth, outcomeHeight)
	if err != nil {
		return err
	}
	// Naming the two cards in the text says where the result came from; drawing
	// the lines is what makes it visible on a board with several of them.
	return s.linkOutcome(ctx, note.ID, sourceID, targetID)
}

// linkOutcome draws a line from each card to the result they produced. These
// links are decoration: RelayTurn only follows links whose target is a
// conversation, so nothing is ever relayed into a note.
func (s *Store) linkOutcome(ctx context.Context, noteID, sourceID, targetID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, conversationID := range []int64{sourceID, targetID} {
		node, err := s.nodeOfConversation(ctx, conversationID)
		if err != nil {
			return err
		}
		if node == 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO canvas_links(source_id, target_id, created_at, mode, max_rounds, until_done, briefing)
VALUES(?, ?, ?, ?, 1, 0, '')
ON CONFLICT(source_id, target_id) DO NOTHING`,
			node, noteID, now, domain.LinkRelay); err != nil {
			return err
		}
	}
	return nil
}

// outcomeBody writes the result card. The answer is kept whole: trimming it
// here would send the reader back into the card this is meant to replace.
func outcomeBody(speaker, other, payload, state string, rounds int) string {
	var builder strings.Builder
	if state == domain.DialogueDone {
		builder.WriteString("## Sonuç\n\n")
	} else {
		builder.WriteString("## Karar bekleniyor\n\n")
	}
	builder.WriteString("*")
	builder.WriteString(speaker)
	builder.WriteString(" ↔ ")
	builder.WriteString(other)
	if rounds > 0 {
		builder.WriteString(" · ")
		builder.WriteString(strconv.Itoa(rounds))
		builder.WriteString(" tur")
	}
	builder.WriteString("*\n\n")
	builder.WriteString(strings.TrimSpace(payload))
	return builder.String()
}

// outcomeColor is the note's accent, used directly as a CSS colour by the
// client. A parked exchange gets the same warning colour the card itself shows.
func outcomeColor(state string) string {
	if state == domain.DialogueWaiting {
		return "var(--warning)"
	}
	return "var(--positive)"
}

// exchangeRounds counts the relayed turns the two cards traded, which is what
// "how long did this take" means for a dialogue.
func (s *Store) exchangeRounds(ctx context.Context, sourceID, targetID int64) (int, error) {
	var rounds int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM chat_turns
WHERE conversation_id IN (?, ?) AND kind IN (?, ?)`,
		sourceID, targetID, domain.TurnRelay, domain.TurnNudge).Scan(&rounds)
	return rounds, err
}

// outcomePosition places the result below the two cards and centred between
// them, so the link it belongs to is obvious without drawing one.
func (s *Store) outcomePosition(ctx context.Context, sourceID, targetID int64) (float64, float64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT x, y, height FROM canvas_nodes
WHERE conversation_id IN (?, ?) AND kind = ?`, sourceID, targetID, domain.NodeConversation)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	var left, bottom float64
	var count int
	for rows.Next() {
		var x, y, height float64
		if err := rows.Scan(&x, &y, &height); err != nil {
			return 0, 0, err
		}
		if count == 0 || x < left {
			left = x
		}
		if y+height > bottom {
			bottom = y + height
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if count == 0 {
		return 0, 0, nil
	}
	return left, bottom + 40, nil
}

// takeBriefing returns the briefing a card should be given before its first
// relayed message, and marks the card as briefed so it is never sent twice.
// An empty result means the card already knows the arrangement.
//
// The briefing rides along with the first real message rather than arriving as
// a turn of its own: a turn would cost an answer nobody asked for, and the card
// would spend it introducing itself.
func (s *Store) takeBriefing(ctx context.Context, conversationID int64, custom, speaker, mode string, untilDone bool) (string, error) {
	if mode == domain.LinkRelay {
		// A plain handoff is not a working arrangement; there is nothing to
		// explain beyond the text itself.
		return "", nil
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET briefed = 1 WHERE id = ? AND briefed = 0", conversationID)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed == 0 {
		return "", nil
	}
	var role string
	if err := s.db.QueryRowContext(ctx,
		"SELECT role FROM conversations WHERE id = ?", conversationID).Scan(&role); err != nil {
		return "", err
	}
	return buildBriefing(custom, role, speaker, mode, untilDone), nil
}

// buildBriefing writes the one-off explanation of who a card is talking to and
// what is expected of it.
func buildBriefing(custom, role, speaker, mode string, untilDone bool) string {
	var builder strings.Builder
	builder.WriteString("Bağlam: Bu kart artık doğrudan başka bir yapay zekâ kartıyla konuşuyor. ")
	builder.WriteString("Karşındaki kartın adı: ")
	if speaker == "" {
		builder.WriteString("bağlı kart")
	} else {
		builder.WriteString(speaker)
	}
	builder.WriteString(". Bundan sonra alacağın mesajlar onun çıktısıdır ve senin cevabın " +
		"doğrudan ona iletilir — arada bir insan yok.\n\n")
	builder.WriteString("Buna göre yaz: karşındaki de bir yapay zekâ olduğu için nezaket kalıbı, " +
		"giriş cümlesi ve özet tekrarı gereksiz. Ona işe yarayacak olanı ver.\n")
	if role != "" {
		builder.WriteString("\nSenin rolün: ")
		builder.WriteString(role)
		builder.WriteString("\n")
	}
	if mode == domain.LinkReview {
		builder.WriteString("\nSenin işin onun çıktısını incelemek: hata, eksik ve riskleri açıkça yaz.\n")
	}
	if untilDone {
		builder.WriteString("\nBu konuşma iş bitene kadar sürer. İş doğrulanarak tamamlandığında " +
			"cevabını [CONCLAVE_DONE] ile bitir. Konuşmayı ancak gerçekten insana ait bir karar " +
			"gerekiyorsa [CONCLAVE_USER_INPUT] ile durdur — soru sormak için değil, yalnızca " +
			"sensiz ilerlenemeyecek bir tercih varsa.\n")
	}
	if custom != "" {
		builder.WriteString("\nGörev: ")
		builder.WriteString(custom)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

// setDialogueState records how an exchange ended on both of its cards.
func (s *Store) setDialogueState(ctx context.Context, sourceID, targetID int64, state string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET dialogue_state = ? WHERE id IN (?, ?)", state, sourceID, targetID)
	return err
}

func (s *Store) createRelayTurn(ctx context.Context, conversationID int64, prompt string, depth int, kind string, runID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	providers, err := conversationProviders(ctx, tx, conversationID)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return nil
	}
	turnID, err := insertTurn(ctx, tx, conversationID, prompt, providers, kind)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE chat_turns SET relay_depth = ?, flow_run_id = ? WHERE id = ?",
		depth, sql.NullInt64{Int64: runID, Valid: runID != 0}, turnID); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordProviderSession remembers the provider-side conversation id so the next
// turn continues it instead of starting over.
func (s *Store) RecordProviderSession(ctx context.Context, conversationID int64, name, sessionID, model string) error {
	if conversationID == 0 || sessionID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO provider_sessions(conversation_id, provider, session_id, model, updated_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(conversation_id, provider) DO UPDATE SET
    session_id = excluded.session_id,
    model = excluded.model,
    updated_at = excluded.updated_at`,
		conversationID, name, sessionID, model, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// Context window thresholds, as a count of tokens carried into one turn.
// A long exchange drifts from its role well before the window is actually full,
// and a session that reaches the ceiling stops being resumable at all.
const (
	// contextRemindAt is where a card starts being reminded what its role is.
	contextRemindAt = 120_000
	// contextResetAt is where the provider session is dropped and started over.
	// The transcript takes over as context, so the work is not lost.
	contextResetAt = 170_000
)

// RecordSessionContext stores how full the provider's window was on this turn.
func (s *Store) RecordSessionContext(ctx context.Context, conversationID int64, provider string, tokens int) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE provider_sessions SET context_tokens = ? WHERE conversation_id = ? AND provider = ?",
		tokens, conversationID, provider)
	return err
}

// recycleSession drops a provider session that has grown too large to keep
// resuming. The next turn starts a new one, and because there is no session to
// resume, the transcript is sent instead — so the conversation continues rather
// than restarting. The card is re-briefed for the same reason.
func (s *Store) recycleSession(ctx context.Context, conversationID int64, provider string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM provider_sessions WHERE conversation_id = ? AND provider = ?",
		conversationID, provider); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE conversations SET briefed = 0 WHERE id = ?", conversationID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetConversationRole says what this card is meant to do when it works with
// another card. Changing it re-briefs the card before its next relayed message.
func (s *Store) SetConversationRole(ctx context.Context, conversationID int64, role string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET role = ?, briefed = 0 WHERE id = ?", role, conversationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetConversationModel picks the model one provider of a card runs on. An empty
// model removes the choice and hands it back to the provider's own default.
//
// The provider session is dropped along with it: resuming a session started on
// another model either continues on the old one or is refused outright,
// depending on the CLI. Without a session the transcript is sent instead, so
// the card changes model without losing the conversation.
func (s *Store) SetConversationModel(ctx context.Context, conversationID int64, name, model string) error {
	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM conversations WHERE id = ?", conversationID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	var current string
	err = s.db.QueryRowContext(ctx,
		"SELECT model FROM conversation_models WHERE conversation_id = ? AND provider = ?",
		conversationID, name).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current == model {
		return nil
	}
	if model == "" {
		if _, err := s.db.ExecContext(ctx,
			"DELETE FROM conversation_models WHERE conversation_id = ? AND provider = ?",
			conversationID, name); err != nil {
			return err
		}
	} else if _, err := s.db.ExecContext(ctx, `
INSERT INTO conversation_models(conversation_id, provider, model) VALUES(?, ?, ?)
ON CONFLICT(conversation_id, provider) DO UPDATE SET model = excluded.model`,
		conversationID, name, model); err != nil {
		return err
	}
	// Only a session that is actually running on another model has to go. One
	// already on the chosen model is worth keeping: it holds the history.
	var sessionModel string
	err = s.db.QueryRowContext(ctx,
		"SELECT model FROM provider_sessions WHERE conversation_id = ? AND provider = ?",
		conversationID, name).Scan(&sessionModel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if sessionModel == model {
		return nil
	}
	return s.recycleSession(ctx, conversationID, name)
}

// conversationModels reads every card's model choices in one pass, keyed by
// conversation and then by provider.
func (s *Store) conversationModels(ctx context.Context) (map[int64]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT conversation_id, provider, model FROM conversation_models")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	models := make(map[int64]map[string]string)
	for rows.Next() {
		var conversationID int64
		var name, model string
		if err := rows.Scan(&conversationID, &name, &model); err != nil {
			return nil, err
		}
		if models[conversationID] == nil {
			models[conversationID] = make(map[string]string)
		}
		models[conversationID][name] = model
	}
	return models, rows.Err()
}

// ResumeDialogue clears a parked exchange so the cards can be pushed on again.
func (s *Store) ResumeDialogue(ctx context.Context, conversationID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET dialogue_state = '' WHERE id = ?", conversationID)
	return err
}

// SetConversationProject points a card at a project directory and access level.
func (s *Store) SetConversationProject(ctx context.Context, conversationID int64, path, access string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET project_path = ?, access = ? WHERE id = ?",
		path, access, conversationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}
