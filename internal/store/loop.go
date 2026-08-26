package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// recentRuns caps how much cycle history a card carries to its client.
const recentRuns = 8

// SetLoop replaces a card's step list and cycle settings.
func (s *Store) SetLoop(ctx context.Context, conversationID int64, config domain.LoopConfig) error {
	config = config.Normalised()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	notify := 0
	if config.NotifyOnFailure {
		notify = 1
	}
	// test_rounds carries the notify flag now that a card has a step list
	// rather than the single command it held before.
	result, err := tx.ExecContext(ctx, `
UPDATE conversations
SET loop_mode = ?, loop_interval_seconds = ?, test_rounds = ?, loop_last_signature = ''
WHERE id = ?`, config.Mode, config.IntervalSeconds, notify, conversationID)
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
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM card_steps WHERE conversation_id = ?", conversationID); err != nil {
		return err
	}
	for position, step := range config.Steps {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO card_steps(conversation_id, position, name, command, timeout_seconds)
VALUES(?, ?, ?, ?, ?)`,
			conversationID, position, step.Name, step.Command, step.TimeoutSeconds); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetLoopRunning arms or disarms a card's cycle. Arming makes it due at once.
func (s *Store) SetLoopRunning(ctx context.Context, conversationID int64, running bool) error {
	due := ""
	value := 0
	if running {
		value = 1
		due = time.Now().UTC().Format(time.RFC3339Nano)
	}
	query := "UPDATE conversations SET loop_running = ?, loop_due_at = ? WHERE id = ?"
	if running {
		// A previous pass belongs to the previous run. Re-arming creates a new
		// completion gate for work-until-done dialogue links.
		query = "UPDATE conversations SET loop_running = ?, loop_due_at = ?, loop_last_signature = '' WHERE id = ?"
	}
	result, err := s.db.ExecContext(ctx, query, value, due, conversationID)
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

// LoopJob is a card whose cycle is due to run.
type LoopJob struct {
	ConversationID int64
	Project        string
	Mode           string
	Interval       int
	Notify         bool
	Steps          []domain.CardStep
	LastSignature  string
}

// ClaimLoopRun takes the next card whose cycle is due and pushes its next due
// time out, so two workers cannot pick up the same card at once.
func (s *Store) ClaimLoopRun(ctx context.Context) (*LoopJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var job LoopJob
	var notify int
	err = tx.QueryRowContext(ctx, `
SELECT id, COALESCE(project_path, ''), loop_mode, loop_interval_seconds,
       COALESCE(test_rounds, 0), COALESCE(loop_last_signature, '')
FROM conversations
WHERE loop_running = 1 AND loop_mode <> ? AND project_path <> ''
  AND loop_due_at <> '' AND loop_due_at <= ?
ORDER BY loop_due_at LIMIT 1`,
		domain.LoopOff, now.Format(time.RFC3339Nano)).
		Scan(&job.ConversationID, &job.Project, &job.Mode, &job.Interval, &notify, &job.LastSignature)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.Notify = notify != 0

	// Lease the card by pushing its next slot out, so it is not claimed again
	// while this cycle is still running.
	lease := now.Add(time.Duration(leaseSeconds(job.Interval)) * time.Second)
	if _, err := tx.ExecContext(ctx,
		"UPDATE conversations SET loop_due_at = ? WHERE id = ?",
		lease.Format(time.RFC3339Nano), job.ConversationID); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx,
		"SELECT name, command, timeout_seconds FROM card_steps WHERE conversation_id = ? ORDER BY position",
		job.ConversationID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var step domain.CardStep
		if err := rows.Scan(&step.Name, &step.Command, &step.TimeoutSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		job.Steps = append(job.Steps, step)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(job.Steps) == 0 {
		// Nothing to run; stop rather than spin on an empty list.
		if _, err := tx.ExecContext(ctx,
			"UPDATE conversations SET loop_running = 0, loop_due_at = '' WHERE id = ?",
			job.ConversationID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

// leaseSeconds keeps a claimed card out of the queue for at least a moment,
// even when the user asked for a zero-second interval.
func leaseSeconds(interval int) int {
	if interval < 1 {
		return 1
	}
	return interval
}

// FinishLoopRun records a cycle result and schedules the next one. It reports
// whether this failure is new, which is what stops a card from being told about
// the same broken step on every single cycle.
func (s *Store) FinishLoopRun(ctx context.Context, job *LoopJob, run domain.CardRun, signature string) (bool, error) {
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO card_runs(conversation_id, started_at, finished_at, status, step_name, exit_code, output)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		job.ConversationID, run.StartedAt, now.Format(time.RFC3339Nano),
		run.Status, run.StepName, run.ExitCode, run.Output); err != nil {
		return false, err
	}
	// Keep only recent history; a continuous cycle would grow without bound.
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM card_runs WHERE conversation_id = ? AND id NOT IN (
    SELECT id FROM card_runs WHERE conversation_id = ? ORDER BY id DESC LIMIT ?
)`, job.ConversationID, job.ConversationID, recentRuns); err != nil {
		return false, err
	}

	passed := run.Status == domain.StatusPassed
	// until_pass stops as soon as a whole cycle succeeds. continuous never
	// stops on its own; only the user disarms it.
	if passed && job.Mode == domain.LoopUntilPass {
		_, err := s.db.ExecContext(ctx, `
UPDATE conversations
SET loop_running = 0, loop_due_at = '', loop_last_signature = 'passed'
WHERE id = ?`, job.ConversationID)
		return false, err
	}

	next := now.Add(time.Duration(leaseSeconds(job.Interval)) * time.Second)
	if _, err := s.db.ExecContext(ctx, `
UPDATE conversations SET loop_due_at = ?, loop_last_signature = ?
WHERE id = ? AND loop_running = 1`,
		next.Format(time.RFC3339Nano), signature, job.ConversationID); err != nil {
		return false, err
	}
	return !passed && signature != "" && signature != job.LastSignature, nil
}

// CreateLoopFailureTurn tells the card what broke, as its next message.
func (s *Store) CreateLoopFailureTurn(ctx context.Context, conversationID int64, prompt string) error {
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
	if _, err := insertTurn(ctx, tx, conversationID, prompt, providers); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) loopSteps(ctx context.Context, conversationID int64) ([]domain.CardStep, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name, command, timeout_seconds FROM card_steps WHERE conversation_id = ? ORDER BY position",
		conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []domain.CardStep{}
	for rows.Next() {
		var step domain.CardStep
		if err := rows.Scan(&step.Name, &step.Command, &step.TimeoutSeconds); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func (s *Store) recentCardRuns(ctx context.Context, conversationID int64) ([]domain.CardRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, status, step_name, exit_code, output, started_at, finished_at
FROM card_runs WHERE conversation_id = ? ORDER BY id DESC LIMIT ?`,
		conversationID, recentRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []domain.CardRun{}
	for rows.Next() {
		var run domain.CardRun
		if err := rows.Scan(&run.ID, &run.Status, &run.StepName, &run.ExitCode,
			&run.Output, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
