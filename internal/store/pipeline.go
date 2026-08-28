package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// pipelineRunHistory is how many past results a pipeline card carries. The
// board is a working surface, not a build server's archive.
const pipelineRunHistory = 5

// CreatePipeline stores a pipeline together with the canvas node that shows it,
// so the board never holds a pipeline with no node.
func (s *Store) CreatePipeline(ctx context.Context, request domain.NewPipeline) (domain.Pipeline, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Pipeline{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	title := request.Title
	if title == "" {
		title = "Pipeline"
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO pipelines(title, project_path, created_at) VALUES(?, '', ?)", title, now)
	if err != nil {
		return domain.Pipeline{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Pipeline{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO canvas_nodes(kind, pipeline_id, x, y, width, height, z, updated_at)
VALUES(?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(z), 0) + 1 FROM canvas_nodes), ?)`,
		domain.NodePipeline, id, request.X, request.Y,
		pipelineWidth, pipelineHeight, now); err != nil {
		return domain.Pipeline{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Pipeline{}, err
	}
	return domain.Pipeline{ID: id, Title: title, Stages: []domain.PipelineStage{}}, nil
}

// SetPipeline replaces a pipeline's title, project and stage list in one write.
// Stages are rewritten rather than merged: their order is their meaning, and a
// per-stage edit would leave the board and the daemon disagreeing about it.
func (s *Store) SetPipeline(ctx context.Context, id int64, config domain.PipelineConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	title := config.Title
	if title == "" {
		title = "Pipeline"
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE pipelines SET title = ?, project_path = ? WHERE id = ?",
		title, config.ProjectPath, id)
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
	if _, err := tx.ExecContext(ctx, "DELETE FROM pipeline_stages WHERE pipeline_id = ?", id); err != nil {
		return err
	}
	for position, stage := range config.Stages {
		if stage.Command == "" {
			continue
		}
		name := stage.Name
		if name == "" {
			name = "adım " + strconv.Itoa(position+1)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO pipeline_stages(pipeline_id, position, name, command) VALUES(?, ?, ?, ?)",
			id, position, name, stage.Command); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StartPipelineRun queues this pipeline's stages as an ordinary run. Nothing
// about execution is special: the daemon's pipeline worker already runs stages
// in order and stops at the first failure.
func (s *Store) StartPipelineRun(ctx context.Context, id int64) (int64, error) {
	pipeline, err := s.pipeline(ctx, id)
	if err != nil {
		return 0, err
	}
	if pipeline.ProjectPath == "" {
		return 0, errors.New("a pipeline needs a project directory before it can run")
	}
	if len(pipeline.Stages) == 0 {
		return 0, errors.New("a pipeline needs at least one stage")
	}
	// A pipeline already waiting or working is not queued twice: a second copy
	// would run the same stages against the same tree at the same time.
	var pending int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runs WHERE pipeline_id = ? AND status IN (?, ?)",
		id, domain.StatusQueued, domain.StatusRunning).Scan(&pending); err != nil {
		return 0, err
	}
	if pending > 0 {
		return 0, errors.New("this pipeline is already running")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx,
		"INSERT INTO runs(project, status, created_at, updated_at, pipeline_id) VALUES(?, ?, ?, ?, ?)",
		pipeline.ProjectPath, domain.StatusQueued, now, now, id)
	if err != nil {
		return 0, err
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for position, stage := range pipeline.Stages {
		command, err := json.Marshal(domain.SplitCommand(stage.Command))
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

// listPipelines returns every pipeline with its stages and recent results, the
// way listConversations returns a conversation with its transcript.
func (s *Store) listPipelines(ctx context.Context) ([]domain.Pipeline, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, title, project_path FROM pipelines ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pipelines := []domain.Pipeline{}
	for rows.Next() {
		var item domain.Pipeline
		if err := rows.Scan(&item.ID, &item.Title, &item.ProjectPath); err != nil {
			return nil, err
		}
		pipelines = append(pipelines, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range pipelines {
		stages, err := s.pipelineStages(ctx, pipelines[index].ID)
		if err != nil {
			return nil, err
		}
		pipelines[index].Stages = stages
		runs, err := s.pipelineRuns(ctx, pipelines[index].ID)
		if err != nil {
			return nil, err
		}
		pipelines[index].Runs = runs
	}
	return pipelines, nil
}

func (s *Store) pipeline(ctx context.Context, id int64) (domain.Pipeline, error) {
	var item domain.Pipeline
	err := s.db.QueryRowContext(ctx,
		"SELECT id, title, project_path FROM pipelines WHERE id = ?", id).
		Scan(&item.ID, &item.Title, &item.ProjectPath)
	if err != nil {
		return domain.Pipeline{}, err
	}
	stages, err := s.pipelineStages(ctx, id)
	if err != nil {
		return domain.Pipeline{}, err
	}
	item.Stages = stages
	return item, nil
}

func (s *Store) pipelineStages(ctx context.Context, id int64) ([]domain.PipelineStage, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name, command FROM pipeline_stages WHERE pipeline_id = ? ORDER BY position", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stages := []domain.PipelineStage{}
	for rows.Next() {
		var stage domain.PipelineStage
		if err := rows.Scan(&stage.Name, &stage.Command); err != nil {
			return nil, err
		}
		stages = append(stages, stage)
	}
	return stages, rows.Err()
}

func (s *Store) pipelineRuns(ctx context.Context, id int64) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM runs WHERE pipeline_id = ? ORDER BY id DESC LIMIT ?", id, pipelineRunHistory)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var runID int64
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, runID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	runs := make([]domain.Run, 0, len(ids))
	for _, runID := range ids {
		run, err := s.GetRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	return runs, nil
}
