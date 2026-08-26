package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	turnID, err := insertTurn(ctx, tx, conversationID, prompt, providers)
	if err != nil {
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

func insertTurn(ctx context.Context, tx *sql.Tx, conversationID int64, prompt string, providers []string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		"INSERT INTO chat_turns(conversation_id, prompt, created_at) VALUES(?, ?, ?)",
		conversationID, prompt, now)
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
	err = tx.QueryRowContext(ctx, `
SELECT r.id, r.turn_id, r.provider, t.prompt, COALESCE(t.conversation_id, 0)
FROM chat_responses r JOIN chat_turns t ON t.id = r.turn_id
WHERE r.status = ?
  AND NOT EXISTS (
    SELECT 1 FROM chat_responses active
    WHERE active.provider = r.provider AND active.status = ?
  )
ORDER BY r.id LIMIT 1`, domain.StatusQueued, domain.StatusRunning).
		Scan(&job.ResponseID, &job.TurnID, &job.Provider, &job.Prompt, &job.ConversationID)
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
	job.Prompt, err = s.chatContext(ctx, job.ConversationID, job.Provider, job.TurnID)
	if err != nil {
		_ = s.RequeueChatResponse(context.WithoutCancel(ctx), job.ResponseID)
		return nil, err
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

func (s *Store) FinishChatResponse(ctx context.Context, id int64, status domain.Status, content, failure string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_responses SET status = ?, content = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, content, failure, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) RequeueChatResponse(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE chat_responses SET status = ?, updated_at = ? WHERE id = ?",
		domain.StatusQueued, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// conversationTurns returns the most recent turns of one conversation in
// chronological order, which is how a transcript is read.
func (s *Store) conversationTurns(ctx context.Context, conversationID int64, limit int) ([]domain.ChatTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, prompt, created_at FROM chat_turns
WHERE conversation_id = ? ORDER BY id DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	turns := []domain.ChatTurn{}
	for rows.Next() {
		var turn domain.ChatTurn
		var created string
		if err := rows.Scan(&turn.ID, &turn.Prompt, &created); err != nil {
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
SELECT id, turn_id, provider, status, content, error
FROM chat_responses WHERE turn_id = ? ORDER BY id`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var responses []domain.ChatResponse
	for rows.Next() {
		var response domain.ChatResponse
		if err := rows.Scan(&response.ID, &response.TurnID, &response.Provider, &response.Status, &response.Content, &response.Error); err != nil {
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
	conversationWidth  = 420
	conversationHeight = 340
	noteWidth          = 240
	noteHeight         = 180
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
	result, err := tx.ExecContext(ctx,
		"INSERT INTO conversations(title, kind, created_at) VALUES(?, ?, ?)",
		request.Title, request.Kind, now)
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
	}, nil
}

// CreateNote adds a standalone sticky note to the canvas.
func (s *Store) CreateNote(ctx context.Context, request domain.NewNote) (domain.CanvasNode, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, x, y, width, height, z, color, body, updated_at)
VALUES(?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?, ?, ?)`,
		domain.NodeNote, request.X, request.Y, noteWidth, noteHeight,
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
		Width: noteWidth, Height: noteHeight, Color: request.Color, Body: request.Body,
	}, nil
}

// Canvas returns every conversation and node needed to draw the board.
func (s *Store) Canvas(ctx context.Context) (domain.Canvas, error) {
	canvas := domain.Canvas{Conversations: []domain.Conversation{}, Nodes: []domain.CanvasNode{}}
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
	}
	canvas.Conversations = conversations
	nodes, err := s.listCanvasNodes(ctx)
	if err != nil {
		return canvas, err
	}
	canvas.Nodes = nodes
	return canvas, nil
}

func (s *Store) listConversations(ctx context.Context) ([]domain.Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, title, kind, created_at FROM conversations ORDER BY id")
	if err != nil {
		return nil, err
	}
	conversations := []domain.Conversation{}
	index := make(map[int64]int)
	for rows.Next() {
		var item domain.Conversation
		var created string
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &created); err != nil {
			rows.Close()
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
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
	return conversations, providerRows.Err()
}

func (s *Store) listCanvasNodes(ctx context.Context) ([]domain.CanvasNode, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, conversation_id, x, y, width, height, z, color, body
FROM canvas_nodes ORDER BY z, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []domain.CanvasNode{}
	for rows.Next() {
		var node domain.CanvasNode
		var conversationID sql.NullInt64
		if err := rows.Scan(&node.ID, &node.Kind, &conversationID, &node.X, &node.Y,
			&node.Width, &node.Height, &node.Z, &node.Color, &node.Body); err != nil {
			return nil, err
		}
		if conversationID.Valid {
			value := conversationID.Int64
			node.ConversationID = &value
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
	var conversationID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		"SELECT kind, conversation_id FROM canvas_nodes WHERE id = ?", id).Scan(&kind, &conversationID)
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
	return tx.Commit()
}

// UpdateChatResponseContent stores partial output while a provider is still
// answering. It refuses to touch a response that is no longer running, so a
// late write cannot overwrite a finished answer or revive a requeued one.
func (s *Store) UpdateChatResponseContent(ctx context.Context, id int64, content string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE chat_responses SET content = ?, updated_at = ?
WHERE id = ? AND status = ?`,
		content, time.Now().UTC().Format(time.RFC3339Nano), id, domain.StatusRunning)
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
