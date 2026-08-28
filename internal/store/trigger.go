package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// minInterval keeps a repeating trigger from becoming a busy loop. A minute is
// short enough for anything a person actually watches and long enough that a
// mistyped interval cannot flood the board.
const minInterval = 60

// CreateTrigger puts a starting point on the board. It arrives switched off:
// something that fires on its own should not begin doing so before anyone has
// said what it sends or what it is linked to.
func (s *Store) CreateTrigger(ctx context.Context, request domain.NewTrigger) (domain.Trigger, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Trigger{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Tetikleyici"
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO triggers(title, prompt, mode, interval_seconds, at_time, enabled, created_at)
VALUES(?, '', ?, 3600, '', 0, ?)`, title, domain.TriggerManual, now)
	if err != nil {
		return domain.Trigger{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Trigger{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, trigger_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
		domain.NodeTrigger, id, request.X, request.Y,
		triggerWidth, triggerHeight, now); err != nil {
		return domain.Trigger{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Trigger{}, err
	}
	return domain.Trigger{
		ID: id, Title: title, Mode: domain.TriggerManual, IntervalSeconds: 3600,
	}, nil
}

// SetTrigger replaces everything about a trigger in one write and works out
// when it is next due. Arming and disarming go through here too, so the next
// slot is always decided in the same place as the schedule it comes from.
func (s *Store) SetTrigger(ctx context.Context, id int64, config domain.TriggerConfig) error {
	title := strings.TrimSpace(config.Title)
	if title == "" {
		title = "Tetikleyici"
	}
	mode := config.Mode
	switch mode {
	case domain.TriggerManual, domain.TriggerInterval, domain.TriggerDaily:
	default:
		return fmt.Errorf("unknown trigger mode %q", config.Mode)
	}
	interval := config.IntervalSeconds
	if interval < minInterval {
		interval = minInterval
	}
	at := strings.TrimSpace(config.AtTime)
	if mode == domain.TriggerDaily {
		if _, err := parseAtTime(at); err != nil {
			return err
		}
	}
	// A trigger with nothing to say would start a run carrying an empty
	// message, which every card would answer with a question.
	prompt := strings.TrimSpace(config.Prompt)
	enabled := config.Enabled && prompt != ""

	due := ""
	if enabled && mode != domain.TriggerManual {
		next, err := nextDue(time.Now(), mode, interval, at)
		if err != nil {
			return err
		}
		due = next.UTC().Format(time.RFC3339Nano)
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE triggers SET title = ?, prompt = ?, mode = ?, interval_seconds = ?,
                    at_time = ?, enabled = ?, due_at = ?
WHERE id = ?`, title, prompt, mode, interval, at, boolToInt(enabled), due, id)
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

// parseAtTime reads a wall-clock "HH:MM". A daily routine is scheduled against
// a person's own day, so this is local time, not UTC.
func parseAtTime(at string) (time.Duration, error) {
	parsed, err := time.Parse("15:04", at)
	if err != nil {
		return 0, errors.New("time of day must be written as HH:MM")
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
}

// nextDue reports when a trigger should fire next, counted from now.
func nextDue(now time.Time, mode string, interval int, at string) (time.Time, error) {
	switch mode {
	case domain.TriggerInterval:
		return now.Add(time.Duration(interval) * time.Second), nil
	case domain.TriggerDaily:
		offset, err := parseAtTime(at)
		if err != nil {
			return time.Time{}, err
		}
		local := now.Local()
		slot := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).Add(offset)
		// Today's slot has passed, so the next one is tomorrow's. Anything
		// else would fire immediately every time the trigger is saved.
		if !slot.After(local) {
			slot = slot.AddDate(0, 0, 1)
		}
		return slot, nil
	}
	return time.Time{}, fmt.Errorf("mode %q has no schedule", mode)
}

// ClaimTriggerFire hands back one trigger that is due, having already moved its
// next slot forward. Two daemons, or two passes of one, must not fire the same
// trigger twice.
func (s *Store) ClaimTriggerFire(ctx context.Context) (*domain.Trigger, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now()
	var trigger domain.Trigger
	var enabled int
	err = tx.QueryRowContext(ctx, `
SELECT id, title, prompt, mode, interval_seconds, at_time, enabled, due_at, last_run_id
FROM triggers
WHERE enabled = 1 AND mode <> ? AND due_at <> '' AND due_at <= ?
ORDER BY due_at LIMIT 1`,
		domain.TriggerManual, now.UTC().Format(time.RFC3339Nano)).
		Scan(&trigger.ID, &trigger.Title, &trigger.Prompt, &trigger.Mode,
			&trigger.IntervalSeconds, &trigger.AtTime, &enabled, &trigger.DueAt, &trigger.LastRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	trigger.Enabled = enabled != 0

	next, err := nextDue(now, trigger.Mode, trigger.IntervalSeconds, trigger.AtTime)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE triggers SET due_at = ? WHERE id = ?",
		next.UTC().Format(time.RFC3339Nano), trigger.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// A routine that overruns its own interval must not start again on top of
	// itself: the slot is skipped, not queued.
	working, err := s.triggerWorking(ctx, trigger.LastRunID)
	if err != nil {
		return nil, err
	}
	if working {
		return nil, nil
	}
	return &trigger, nil
}

// FireTrigger starts a run and hands the trigger's message to everything the
// board links to it. It reports how many cards received it, which is zero for a
// trigger nobody has linked anywhere yet.
func (s *Store) FireTrigger(ctx context.Context, id int64) (int, error) {
	var trigger domain.Trigger
	var nodeID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT t.id, t.title, t.prompt, n.id
FROM triggers t LEFT JOIN canvas_nodes n ON n.trigger_id = t.id
WHERE t.id = ?`, id).Scan(&trigger.ID, &trigger.Title, &trigger.Prompt, &nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, sql.ErrNoRows
	}
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(trigger.Prompt) == "" {
		return 0, errors.New("a trigger with no message has nothing to send")
	}
	if !nodeID.Valid {
		return 0, errors.New("a trigger with no card on the board cannot reach anything")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	runID, err := s.StartFlowRun(ctx, tx, 0)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	title := trigger.Title
	if title == "" {
		title = "Tetikleyici"
	}
	delivered, err := s.relayFrom(ctx, relaySource{
		runID:    runID,
		nodeID:   nodeID.Int64,
		title:    title,
		turnKind: domain.TurnTrigger,
		payload:  trigger.Prompt,
	})
	if err != nil {
		return delivered, err
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE triggers SET last_fired_at = ?, last_run_id = ? WHERE id = ?",
		time.Now().UTC().Format(time.RFC3339Nano), runID, id); err != nil {
		return delivered, err
	}
	// A trigger linked to nothing produced no work, so its run is already over.
	if delivered == 0 {
		return 0, s.FinishFlowRun(ctx, runID)
	}
	return delivered, nil
}

// triggerWorking reports whether the run a trigger last started is still going.
func (s *Store) triggerWorking(ctx context.Context, runID int64) (bool, error) {
	if runID == 0 {
		return false, nil
	}
	var status string
	err := s.db.QueryRowContext(ctx,
		"SELECT status FROM flow_runs WHERE id = ?", runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == domain.RunRunning, nil
}

// listTriggers reports every trigger with the node that shows it, so the board
// can draw each one on its own card.
func (s *Store) listTriggers(ctx context.Context) ([]domain.Trigger, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, COALESCE(n.id, 0), t.title, t.prompt, t.mode, t.interval_seconds,
       t.at_time, t.enabled, t.due_at, t.last_fired_at, t.last_run_id
FROM triggers t LEFT JOIN canvas_nodes n ON n.trigger_id = t.id
ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	triggers := []domain.Trigger{}
	for rows.Next() {
		var trigger domain.Trigger
		var enabled int
		if err := rows.Scan(&trigger.ID, &trigger.NodeID, &trigger.Title, &trigger.Prompt,
			&trigger.Mode, &trigger.IntervalSeconds, &trigger.AtTime, &enabled,
			&trigger.DueAt, &trigger.LastFiredAt, &trigger.LastRunID); err != nil {
			return nil, err
		}
		trigger.Enabled = enabled != 0
		triggers = append(triggers, trigger)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range triggers {
		working, err := s.triggerWorking(ctx, triggers[index].LastRunID)
		if err != nil {
			return nil, err
		}
		triggers[index].Working = working
	}
	return triggers, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
