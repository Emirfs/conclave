package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func userVersion(t *testing.T, store *Store) int {
	t.Helper()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return version
}

func TestOpenAppliesEveryMigration(t *testing.T) {
	store := openTemp(t)
	if got := userVersion(t, store); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	canvas, err := store.Canvas(context.Background())
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Conversations) != 0 || len(canvas.Nodes) != 0 {
		t.Fatalf("fresh canvas is not empty: %+v", canvas)
	}
}

// A database created before conversations existed must be upgraded in place
// with its chat history intact, not recreated.
func TestOpenUpgradesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")

	legacy, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := legacy.Exec(migrations[0]); err != nil {
		t.Fatalf("apply migration 1: %v", err)
	}
	if _, err := legacy.Exec(
		"INSERT INTO chat_turns(prompt, created_at) VALUES('eski soru', '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store.Close()

	if got := userVersion(t, store); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	var prompt string
	var conversationID sql.NullInt64
	err = store.db.QueryRow("SELECT prompt, conversation_id FROM chat_turns").Scan(&prompt, &conversationID)
	if err != nil {
		t.Fatalf("read upgraded turn: %v", err)
	}
	if prompt != "eski soru" {
		t.Fatalf("prompt = %q, want %q", prompt, "eski soru")
	}
	if conversationID.Valid {
		t.Fatalf("legacy turn should have no conversation yet, got %d", conversationID.Int64)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	for attempt := range 3 {
		store, err := Open(path)
		if err != nil {
			t.Fatalf("open attempt %d: %v", attempt, err)
		}
		if got := userVersion(t, store); got != len(migrations) {
			t.Fatalf("attempt %d: user_version = %d, want %d", attempt, got, len(migrations))
		}
		store.Close()
	}
}

func TestConversationCreatesItsCanvasNode(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Claude", Kind: domain.KindSolo, Providers: []string{"claude"}, X: 120, Y: 80,
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Conversations) != 1 || len(canvas.Nodes) != 1 {
		t.Fatalf("want one conversation and one node, got %d and %d",
			len(canvas.Conversations), len(canvas.Nodes))
	}
	if got := canvas.Conversations[0].Providers; len(got) != 1 || got[0] != "claude" {
		t.Fatalf("providers = %v, want [claude]", got)
	}
	node := canvas.Nodes[0]
	if node.Kind != domain.NodeConversation {
		t.Fatalf("node kind = %q", node.Kind)
	}
	if node.ConversationID == nil || *node.ConversationID != conversation.ID {
		t.Fatalf("node is not linked to conversation %d", conversation.ID)
	}
	if node.X != 120 || node.Y != 80 {
		t.Fatalf("node position = (%v, %v), want (120, 80)", node.X, node.Y)
	}
}

func TestPatchCanvasNodeTouchesOnlySuppliedFields(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	note, err := store.CreateNote(ctx, domain.NewNote{Body: "ilk metin", Color: "#f2c55c", X: 10, Y: 20})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	// A drag moves the node and must leave the text alone.
	x, y := 300.0, 400.0
	if err := store.PatchCanvasNode(ctx, domain.CanvasNodePatch{ID: note.ID, X: &x, Y: &y}); err != nil {
		t.Fatalf("patch position: %v", err)
	}
	// A text edit must leave the position alone.
	body := "guncellenmis metin"
	if err := store.PatchCanvasNode(ctx, domain.CanvasNodePatch{ID: note.ID, Body: &body}); err != nil {
		t.Fatalf("patch body: %v", err)
	}

	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	got := canvas.Nodes[0]
	if got.X != 300 || got.Y != 400 {
		t.Fatalf("position = (%v, %v), want (300, 400)", got.X, got.Y)
	}
	if got.Body != body {
		t.Fatalf("body = %q, want %q", got.Body, body)
	}
	if got.Color != "#f2c55c" {
		t.Fatalf("colour = %q, want %q", got.Color, "#f2c55c")
	}
}

func TestPatchMissingCanvasNodeReportsNoRows(t *testing.T) {
	store := openTemp(t)
	x := 1.0
	err := store.PatchCanvasNode(context.Background(), domain.CanvasNodePatch{ID: 9999, X: &x})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteConversationNodeRemovesConversation(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	if _, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Grup", Kind: domain.KindGroup, Providers: []string{"claude", "openai"},
	}); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if err := store.DeleteCanvasNode(ctx, canvas.Nodes[0].ID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	canvas, err = store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas after delete: %v", err)
	}
	if len(canvas.Nodes) != 0 || len(canvas.Conversations) != 0 {
		t.Fatalf("delete left %d nodes and %d conversations",
			len(canvas.Nodes), len(canvas.Conversations))
	}
}

// Two solo conversations with the same provider must not see each other's
// history. This is what makes "talk to each AI separately" true.
func TestChatContextIsScopedToItsConversation(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	first, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Birinci", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Ikinci", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	// A completed exchange in the first conversation.
	firstTurn, err := store.CreateConversationTurn(ctx, first.ID, "parolam kirmizi")
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim first: %v (job %v)", err, job)
	}
	if job.ConversationID != first.ID {
		t.Fatalf("job conversation = %d, want %d", job.ConversationID, first.ID)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "anladim", ""); err != nil {
		t.Fatalf("finish first: %v", err)
	}
	_ = firstTurn

	// A fresh turn in the second conversation must carry none of that.
	if _, err := store.CreateConversationTurn(ctx, second.ID, "parolam ne"); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	secondJob, err := store.ClaimChatResponse(ctx)
	if err != nil || secondJob == nil {
		t.Fatalf("claim second: %v (job %v)", err, secondJob)
	}
	if secondJob.ConversationID != second.ID {
		t.Fatalf("second job conversation = %d, want %d", secondJob.ConversationID, second.ID)
	}
	if strings.Contains(secondJob.Prompt, "kirmizi") || strings.Contains(secondJob.Prompt, "anladim") {
		t.Fatalf("second conversation leaked the first one's history:\n%s", secondJob.Prompt)
	}
	if !strings.Contains(secondJob.Prompt, "parolam ne") {
		t.Fatalf("second prompt is missing its own message:\n%s", secondJob.Prompt)
	}
}

// Within one conversation, history must accumulate for that provider.
func TestChatContextCarriesEarlierTurnsOfSameConversation(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Tek", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "parolam kirmizi"); err != nil {
		t.Fatalf("turn one: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim one: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "anladim", ""); err != nil {
		t.Fatalf("finish one: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "parolam ne"); err != nil {
		t.Fatalf("turn two: %v", err)
	}
	second, err := store.ClaimChatResponse(ctx)
	if err != nil || second == nil {
		t.Fatalf("claim two: %v", err)
	}
	for _, want := range []string{"parolam kirmizi", "anladim", "parolam ne"} {
		if !strings.Contains(second.Prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, second.Prompt)
		}
	}
}

// A group conversation queues one response per provider from a single message.
func TestGroupTurnFansOutToEveryProvider(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Grup", Kind: domain.KindGroup, Providers: []string{"claude", "openai", "gemini"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "karsilastir"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	turns := canvas.Conversations[0].Turns
	if len(turns) != 1 {
		t.Fatalf("want one turn, got %d", len(turns))
	}
	if len(turns[0].Responses) != 3 {
		t.Fatalf("want three responses, got %d", len(turns[0].Responses))
	}
	// Each provider may only run one job at a time, so a single claim per pass.
	seen := map[string]bool{}
	for range 3 {
		job, err := store.ClaimChatResponse(ctx)
		if err != nil || job == nil {
			t.Fatalf("claim: %v (job %v)", err, job)
		}
		if seen[job.Provider] {
			t.Fatalf("provider %s claimed twice", job.Provider)
		}
		seen[job.Provider] = true
		if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "ok", ""); err != nil {
			t.Fatalf("finish: %v", err)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("claimed providers = %v", seen)
	}
}

// Transcripts must read oldest-first even though the query walks backwards.
func TestConversationTurnsAreChronological(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Sira", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, prompt := range []string{"bir", "iki", "uc"} {
		if _, err := store.CreateConversationTurn(ctx, conversation.ID, prompt); err != nil {
			t.Fatalf("turn %s: %v", prompt, err)
		}
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	turns := canvas.Conversations[0].Turns
	got := make([]string, 0, len(turns))
	for _, turn := range turns {
		got = append(got, turn.Prompt)
	}
	if strings.Join(got, ",") != "bir,iki,uc" {
		t.Fatalf("order = %v, want [bir iki uc]", got)
	}
}
