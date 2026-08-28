package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// maxRunSteps bounds one run. Depth limits how far a single chain travels, but
// says nothing about width: a board that branches and rejoins can produce far
// more turns than any one path is long. This is the ceiling on the whole
// journey, and it is what keeps a wide graph from multiplying without end.
const maxRunSteps = 40

// StartFlowRun opens a run for a message a person sent. Everything that follows
// from it — relays, nudges, joins — belongs to the same run.
func (s *Store) StartFlowRun(ctx context.Context, tx *sql.Tx, conversationID int64) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		"INSERT INTO flow_runs(origin_conversation_id, status, steps, started_at) VALUES(?, ?, 0, ?)",
		sql.NullInt64{Int64: conversationID, Valid: conversationID != 0}, domain.RunRunning, now)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// countRunStep records that a run produced one more turn, and reports whether
// the run may keep going. A run that has spent its budget is closed here, so
// the ceiling is enforced in one place rather than at every call site.
func (s *Store) countRunStep(ctx context.Context, runID int64) (bool, error) {
	if runID == 0 {
		return true, nil
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE flow_runs SET steps = steps + 1 WHERE id = ?", runID); err != nil {
		return false, err
	}
	var steps int
	if err := s.db.QueryRowContext(ctx,
		"SELECT steps FROM flow_runs WHERE id = ?", runID).Scan(&steps); err != nil {
		return false, err
	}
	if steps >= maxRunSteps {
		if err := s.FinishFlowRun(ctx, runID); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// FinishFlowRun closes a run. It is idempotent: a run reaches its end from
// several directions — a budget spent, a dialogue finished, nothing left to
// deliver — and none of them should have to check first.
func (s *Store) FinishFlowRun(ctx context.Context, runID int64) error {
	if runID == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE flow_runs SET status = ?, finished_at = ?
WHERE id = ? AND status = ?`,
		domain.RunDone, time.Now().UTC().Format(time.RFC3339Nano), runID, domain.RunRunning)
	return err
}

// runOfTurn reports which run a turn belongs to. Zero means a turn from before
// runs existed, which is treated as unbudgeted rather than refused.
func (s *Store) runOfTurn(ctx context.Context, turnID int64) (int64, error) {
	var runID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT flow_run_id FROM chat_turns WHERE id = ?", turnID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return runID.Int64, nil
}

// ActiveFlowRuns reports the runs still in flight, newest first. The board
// shows these so a spreading exchange is something you can see rather than
// something you infer from cards lighting up.
func (s *Store) ActiveFlowRuns(ctx context.Context) ([]domain.FlowRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, COALESCE(origin_conversation_id, 0), status, steps, started_at
FROM flow_runs WHERE status = ? ORDER BY id DESC LIMIT 20`, domain.RunRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []domain.FlowRun{}
	for rows.Next() {
		var run domain.FlowRun
		if err := rows.Scan(&run.ID, &run.OriginConversationID,
			&run.Status, &run.Steps, &run.StartedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// StopFlowRun ends a run and stops every card still working on it. A run is the
// unit a person thinks in — "this thing I started" — so it has to be the unit
// they can stop, rather than having to catch each card in turn.
func (s *Store) StopFlowRun(ctx context.Context, runID int64) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT DISTINCT conversation_id FROM chat_turns WHERE flow_run_id = ? AND conversation_id IS NOT NULL",
		runID)
	if err != nil {
		return 0, err
	}
	var conversations []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		conversations = append(conversations, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	stopped := 0
	for _, id := range conversations {
		count, err := s.RequestConversationCancel(ctx, id)
		if err != nil {
			return stopped, err
		}
		stopped += count
	}
	// Anything parked at a join belongs to a run that is over.
	if _, err := s.db.ExecContext(ctx, "DELETE FROM join_inputs WHERE flow_run_id = ?", runID); err != nil {
		return stopped, err
	}
	return stopped, s.FinishFlowRun(ctx, runID)
}

// deliverToJoin records one line's answer at a waiting point and reports the
// combined message once every line has spoken. Until then it returns ready
// false: a join that hands on early is just a relay with extra steps.
func (s *Store) deliverToJoin(
	ctx context.Context,
	runID, nodeID, sourceNodeID int64,
	sourceTitle, payload string,
) (string, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// A second answer from the same source replaces the first: within one run a
	// card speaks for itself once, and the latest is what it meant.
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO join_inputs(flow_run_id, node_id, source_node_id, source_title, payload, arrived_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(flow_run_id, node_id, source_node_id) DO UPDATE SET
    payload = excluded.payload,
    source_title = excluded.source_title,
    arrived_at = excluded.arrived_at`,
		runID, nodeID, sourceNodeID, sourceTitle, payload, now); err != nil {
		return "", false, err
	}

	expected, err := s.joinInputCount(ctx, nodeID)
	if err != nil {
		return "", false, err
	}
	arrived, err := s.joinArrivals(ctx, runID, nodeID)
	if err != nil {
		return "", false, err
	}
	if expected == 0 || len(arrived) < expected {
		return "", false, nil
	}

	var combined strings.Builder
	for index, item := range arrived {
		if index > 0 {
			combined.WriteString("\n\n")
		}
		combined.WriteString(item.title + ":\n" + item.payload)
	}
	// The waiting point is cleared once it has spoken, so the same join can be
	// reached again later in the same run without carrying stale answers.
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM join_inputs WHERE flow_run_id = ? AND node_id = ?", runID, nodeID); err != nil {
		return "", false, err
	}
	return combined.String(), true, nil
}

type joinArrival struct {
	title   string
	payload string
}

func (s *Store) joinArrivals(ctx context.Context, runID, nodeID int64) ([]joinArrival, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(source_title, ''), payload FROM join_inputs
WHERE flow_run_id = ? AND node_id = ? ORDER BY id`, runID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var arrivals []joinArrival
	for rows.Next() {
		var item joinArrival
		if err := rows.Scan(&item.title, &item.payload); err != nil {
			return nil, err
		}
		if item.title == "" {
			item.title = "Bağlı kart"
		}
		arrivals = append(arrivals, item)
	}
	return arrivals, rows.Err()
}

// joinInputCount is how many lines feed a join, which is what "everyone has
// spoken" means for it.
func (s *Store) joinInputCount(ctx context.Context, nodeID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM canvas_links WHERE target_id = ?", nodeID).Scan(&count)
	return count, err
}

// CreateJoin puts a waiting point on the board.
func (s *Store) CreateJoin(ctx context.Context, request domain.NewNote) (domain.CanvasNode, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	body := strings.TrimSpace(request.Body)
	if body == "" {
		body = "Birleştirici"
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, x, y, width, height, z, color, body, updated_at)
VALUES(?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), '', ?, ?)`,
		domain.NodeJoin, request.X, request.Y, joinWidth, joinHeight, body, now)
	if err != nil {
		return domain.CanvasNode{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.CanvasNode{}, err
	}
	return domain.CanvasNode{
		ID: id, Kind: domain.NodeJoin, X: request.X, Y: request.Y,
		Width: joinWidth, Height: joinHeight, Body: body,
	}, nil
}

// listJoins reports each waiting point with how many lines feed it and how many
// have already spoken, so a join that is holding a run says which side it is
// waiting on.
func (s *Store) listJoins(ctx context.Context) ([]domain.JoinNode, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, COALESCE(body, '') FROM canvas_nodes WHERE kind = ? ORDER BY id", domain.NodeJoin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	joins := []domain.JoinNode{}
	for rows.Next() {
		var item domain.JoinNode
		if err := rows.Scan(&item.NodeID, &item.Title); err != nil {
			return nil, err
		}
		joins = append(joins, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range joins {
		expected, err := s.joinInputCount(ctx, joins[index].NodeID)
		if err != nil {
			return nil, err
		}
		joins[index].Expected = expected
		waiting, err := s.joinWaiting(ctx, joins[index].NodeID)
		if err != nil {
			return nil, err
		}
		joins[index].Waiting = len(waiting)
		joins[index].Sources = waiting
	}
	return joins, nil
}

func (s *Store) joinWaiting(ctx context.Context, nodeID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(source_title, '') FROM join_inputs WHERE node_id = ? ORDER BY id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		sources = append(sources, title)
	}
	return sources, rows.Err()
}

// relayTarget is one link leaving a node, together with what sits at the other
// end. The kind matters because a join is handled entirely differently from a
// card: it holds the message instead of answering it.
type relayTarget struct {
	nodeID       int64
	kind         string
	title        string
	conversation int64
	mode         string
	maxRounds    int
	untilDone    bool
	briefing     string
}

// relayTargets lists what a node hands its answer to. Links are read from the
// node rather than from the conversation, because a join has no conversation
// and would otherwise be invisible to the relay.
func (s *Store) relayTargets(ctx context.Context, nodeID int64) ([]relayTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT target.id, target.kind, COALESCE(target.body, ''), COALESCE(target.conversation_id, 0),
       link.mode, link.max_rounds, link.until_done, link.briefing
FROM canvas_links link
JOIN canvas_nodes target ON target.id = link.target_id
WHERE link.source_id = ?
ORDER BY target.id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []relayTarget
	for rows.Next() {
		var item relayTarget
		if err := rows.Scan(&item.nodeID, &item.kind, &item.title, &item.conversation,
			&item.mode, &item.maxRounds, &item.untilDone, &item.briefing); err != nil {
			return nil, err
		}
		if item.kind != domain.NodeJoin && item.conversation == 0 {
			// A note or a pipeline is not something an answer can be said to.
			continue
		}
		if item.kind == domain.NodeJoin && strings.TrimSpace(item.title) == "" {
			item.title = "Birleştirici"
		}
		targets = append(targets, item)
	}
	return targets, rows.Err()
}

// maybeFinishRun closes a run once the board has fallen quiet. A single card
// finishing is not the end of anything: its siblings may still be working, and
// closing on the first one to stop would end a run halfway through.
func (s *Store) maybeFinishRun(ctx context.Context, runID int64) error {
	if runID == 0 {
		return nil
	}
	var pending int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM chat_responses r JOIN chat_turns t ON t.id = r.turn_id
WHERE t.flow_run_id = ? AND r.status IN (?, ?)`,
		runID, string(domain.StatusQueued), string(domain.StatusRunning)).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	// Anything still parked at a join is waiting on a line that will now never
	// speak. Clearing it keeps a stalled run from showing as waiting forever.
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM join_inputs WHERE flow_run_id = ?", runID); err != nil {
		return err
	}
	return s.FinishFlowRun(ctx, runID)
}

// linkMustBeOneWay reports whether a link can only ever run one way. A return
// link, and an exchange that runs until it is done, need two cards that can
// answer; a join waits and a trigger only ever starts things.
func (s *Store) linkMustBeOneWay(ctx context.Context, sourceID, targetID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM canvas_nodes WHERE id IN (?, ?) AND kind IN (?, ?)",
		sourceID, targetID, domain.NodeJoin, domain.NodeTrigger).Scan(&count)
	return count > 0, err
}
