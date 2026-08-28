package store

import (
	"context"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// A pipeline card, like a conversation card, must never exist without the node
// that shows it.
func TestPipelineCreatesItsCanvasNode(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	pipeline, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "Derle ve test", X: 40, Y: 80})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Pipelines) != 1 || canvas.Pipelines[0].ID != pipeline.ID {
		t.Fatalf("canvas carries %+v", canvas.Pipelines)
	}
	if len(canvas.Nodes) != 1 || canvas.Nodes[0].Kind != domain.NodePipeline {
		t.Fatalf("canvas nodes = %+v", canvas.Nodes)
	}
	if canvas.Nodes[0].PipelineID == nil || *canvas.Nodes[0].PipelineID != pipeline.ID {
		t.Fatal("the node does not point at its pipeline")
	}
}

// Stage order is the whole meaning of a pipeline, so a save rewrites the list
// rather than merging into it.
func TestSetPipelineReplacesItsStages(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	pipeline, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "P"})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	project := t.TempDir()
	config := domain.PipelineConfig{
		Title:       "Derle ve test",
		ProjectPath: project,
		Stages: []domain.PipelineStage{
			{Name: "build", Command: "go build ./..."},
			{Name: "test", Command: "go test ./..."},
		},
	}
	if err := store.SetPipeline(ctx, pipeline.ID, config); err != nil {
		t.Fatalf("set pipeline: %v", err)
	}
	config.Stages = []domain.PipelineStage{{Name: "vet", Command: "go vet ./..."}}
	if err := store.SetPipeline(ctx, pipeline.ID, config); err != nil {
		t.Fatalf("set pipeline again: %v", err)
	}
	stored, err := store.pipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("read pipeline: %v", err)
	}
	if len(stored.Stages) != 1 || stored.Stages[0].Name != "vet" {
		t.Fatalf("stages = %+v, want only vet", stored.Stages)
	}
	if stored.Title != "Derle ve test" || stored.ProjectPath != project {
		t.Fatalf("pipeline = %+v", stored)
	}
}

// Queueing a pipeline produces an ordinary run: the daemon already knows how to
// work through stages in order, and nothing about this path is special.
func TestStartPipelineQueuesAnOrdinaryRun(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	pipeline, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "P"})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	project := t.TempDir()
	if err := store.SetPipeline(ctx, pipeline.ID, domain.PipelineConfig{
		Title:       "P",
		ProjectPath: project,
		Stages: []domain.PipelineStage{
			{Name: "build", Command: `go build "./..."`},
		},
	}); err != nil {
		t.Fatalf("set pipeline: %v", err)
	}
	runID, err := store.StartPipelineRun(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if run.Project != project || run.Status != domain.StatusQueued {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Stages) != 1 {
		t.Fatalf("run carries %d stages, want 1", len(run.Stages))
	}
	// The typed line is split into an argument array, with quoting honoured and
	// nothing evaluated.
	want := []string{"go", "build", "./..."}
	got := run.Stages[0].Command
	if len(got) != len(want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("command = %v, want %v", got, want)
		}
	}
	// The claim path a daemon worker uses must see it.
	claimed, err := store.ClaimRun(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != runID {
		t.Fatalf("claimed run %d, want %d", claimed.ID, runID)
	}
}

// Running the same stages against the same tree twice at once is not a thing
// anyone wants; a second queue is refused rather than stacked.
func TestStartPipelineRefusesASecondRun(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	pipeline, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "P"})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	if err := store.SetPipeline(ctx, pipeline.ID, domain.PipelineConfig{
		Title:       "P",
		ProjectPath: t.TempDir(),
		Stages:      []domain.PipelineStage{{Name: "build", Command: "go build ./..."}},
	}); err != nil {
		t.Fatalf("set pipeline: %v", err)
	}
	if _, err := store.StartPipelineRun(ctx, pipeline.ID); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := store.StartPipelineRun(ctx, pipeline.ID); err == nil {
		t.Fatal("a second run was queued while the first was still waiting")
	}
}

// A pipeline with nothing to run against, or nothing to run, is refused rather
// than executed somewhere arbitrary.
func TestStartPipelineNeedsAProjectAndStages(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	pipeline, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "P"})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	if _, err := store.StartPipelineRun(ctx, pipeline.ID); err == nil {
		t.Fatal("a pipeline with no project was queued")
	}
	if err := store.SetPipeline(ctx, pipeline.ID, domain.PipelineConfig{
		Title: "P", ProjectPath: t.TempDir(),
	}); err != nil {
		t.Fatalf("set pipeline: %v", err)
	}
	if _, err := store.StartPipelineRun(ctx, pipeline.ID); err == nil {
		t.Fatal("a pipeline with no stages was queued")
	}
}

// Deleting the card deletes the pipeline behind it; a definition with no node
// would be unreachable state.
func TestDeletingAPipelineNodeRemovesThePipeline(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	if _, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "P"}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if err := store.DeleteCanvasNode(ctx, canvas.Nodes[0].ID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Pipelines) != 0 || len(after.Nodes) != 0 {
		t.Fatalf("board still carries %+v / %+v", after.Pipelines, after.Nodes)
	}
}
