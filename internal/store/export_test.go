package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

func TestExportConversationRendersTheWholeTranscript(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Linker incelemesi", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "betigi gozden gecir"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed,
		"Flash bolgesi yanlis hizalanmis.", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}

	markdown, err := store.ExportConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, want := range []string{
		"# Linker incelemesi",
		"betigi gozden gecir",
		"### claude",
		"Flash bolgesi yanlis hizalanmis.",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("export is missing %q:\n%s", want, markdown)
		}
	}
}

// A transcript longer than the window the canvas carries must still come out
// whole: an export is the record, not what a card happened to be showing.
func TestExportConversationIsNotLimitedToTheCanvasWindow(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Uzun", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	total := transcriptLimit + 5
	for index := range total {
		if _, err := store.CreateConversationTurn(ctx, conversation.ID, "mesaj "+string(rune('a'+index%26))); err != nil {
			t.Fatalf("turn %d: %v", index, err)
		}
	}
	markdown, err := store.ExportConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got := strings.Count(markdown, "## Kullanıcı"); got != total {
		t.Fatalf("export carries %d turns, want %d", got, total)
	}
}

// An exported and re-imported board must come back with the same cards, the
// same links between them, and the transcripts intact.
func TestBoardSurvivesExportAndImport(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkRelay, MaxRounds: 3}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "ilk mesaj"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "ilk cevap", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.CreateNote(ctx, domain.NewNote{Body: "bir not"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	pipeline, err := store.CreatePipeline(ctx, domain.NewPipeline{Title: "Derle"})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if err := store.SetPipeline(ctx, pipeline.ID, domain.PipelineConfig{
		Title: "Derle", ProjectPath: t.TempDir(),
		Stages: []domain.PipelineStage{{Name: "build", Command: "go build ./..."}},
	}); err != nil {
		t.Fatalf("set pipeline: %v", err)
	}

	export, err := store.ExportBoard(ctx)
	if err != nil {
		t.Fatalf("export board: %v", err)
	}
	if len(export.Nodes) != 4 || len(export.Links) != 1 {
		t.Fatalf("export carries %d nodes and %d links", len(export.Nodes), len(export.Links))
	}

	// Import into an empty board, which is the case that has to reproduce it.
	target := openTemp(t)
	result, err := target.ImportBoard(ctx, export, 0, 0)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Nodes != 4 || result.Links != 1 {
		t.Fatalf("import added %+v", result)
	}
	after, err := target.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Conversations) != 2 || len(after.Pipelines) != 1 || len(after.Links) != 1 {
		t.Fatalf("imported board = %d cards, %d pipelines, %d links",
			len(after.Conversations), len(after.Pipelines), len(after.Links))
	}
	markdown, err := target.ExportConversation(ctx, after.Conversations[0].ID)
	if err != nil {
		t.Fatalf("export imported card: %v", err)
	}
	if !strings.Contains(markdown, "ilk mesaj") || !strings.Contains(markdown, "ilk cevap") {
		t.Fatalf("imported transcript lost its content:\n%s", markdown)
	}
}

// Importing must never send somebody else's prompt to a provider: a transcript
// arrives as history, not as work to do.
func TestImportDoesNotQueueAnything(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	export := domain.BoardExport{
		Version: domain.BoardExportVersion,
		Nodes: []domain.ExportedNode{{
			NodeID: 1, Kind: domain.NodeConversation, Title: "A",
			ConversationKind: domain.KindSolo, Providers: []string{"claude"},
			Turns: []domain.ExportedTurn{{
				Prompt: "eski soru",
				Responses: []domain.ExportedResponse{
					{Provider: "claude", Status: domain.StatusPassed, Content: "eski cevap"},
					// A card that was mid-answer when the board was exported.
					{Provider: "codex", Status: domain.StatusRunning},
				},
			}},
		}},
	}
	if _, err := store.ImportBoard(ctx, export, 0, 0); err != nil {
		t.Fatalf("import: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job != nil {
		t.Fatalf("import queued response %d for %s", job.ResponseID, job.Provider)
	}
}

// An import puts cards alongside what is there; it never replaces the board.
func TestImportIsAdditive(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	if _, err := store.CreateNote(ctx, domain.NewNote{Body: "duran not"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	export := domain.BoardExport{
		Version: domain.BoardExportVersion,
		Nodes:   []domain.ExportedNode{{NodeID: 9, Kind: domain.NodeNote, Body: "gelen not"}},
	}
	if _, err := store.ImportBoard(ctx, export, 200, 200); err != nil {
		t.Fatalf("import: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Nodes) != 2 {
		t.Fatalf("board carries %d nodes, want 2", len(canvas.Nodes))
	}
}

// A file from a newer build describes things this one has nowhere to put.
func TestImportRefusesANewerExport(t *testing.T) {
	store := openTemp(t)
	if _, err := store.ImportBoard(context.Background(), domain.BoardExport{
		Version: domain.BoardExportVersion + 1,
	}, 0, 0); err == nil {
		t.Fatal("a newer export was imported anyway")
	}
}
