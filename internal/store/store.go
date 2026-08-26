package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
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

func (s *Store) init() error {
	_, err := s.db.Exec(`
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
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
UPDATE stages SET status = 'queued' WHERE status = 'running';
UPDATE runs SET status = 'queued', updated_at = CURRENT_TIMESTAMP WHERE status = 'running';
`)
	return err
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
