package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// helper: run every queued response of a conversation to completion.
func answerAll(t *testing.T, store *Store, reply string) int {
	t.Helper()
	ctx := context.Background()
	answered := 0
	for range 20 {
		job, err := store.ClaimChatResponse(ctx)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if job == nil {
			return answered
		}
		if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, reply, ""); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
			t.Fatalf("relay: %v", err)
		}
		answered++
	}
	t.Fatal("answering did not settle; a relay loop is likely")
	return answered
}

func linkedPair(t *testing.T, store *Store) (domain.Conversation, domain.Conversation, domain.Canvas) {
	t.Helper()
	ctx := context.Background()
	first, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	second, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "B", Kind: domain.KindSolo, Providers: []string{"openai"},
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	return first, second, canvas
}

func nodeOf(t *testing.T, canvas domain.Canvas, conversationID int64) int64 {
	t.Helper()
	for _, node := range canvas.Nodes {
		if node.ConversationID != nil && *node.ConversationID == conversationID {
			return node.ID
		}
	}
	t.Fatalf("no node for conversation %d", conversationID)
	return 0
}

// defaultRounds mirrors domain.LinkOptions{}.Normalised().
const defaultRounds = 3

func TestLinkRelaysAnswerToTargetCard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)

	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID), domain.LinkOptions{}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "ilk soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "A'nin cevabi")

	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	var target domain.Conversation
	for _, item := range after.Conversations {
		if item.ID == second.ID {
			target = item
		}
	}
	if len(target.Turns) != 1 {
		t.Fatalf("target received %d turns, want 1", len(target.Turns))
	}
	if target.Turns[0].Prompt != "A'nin cevabi" {
		t.Fatalf("relayed prompt = %q", target.Turns[0].Prompt)
	}
}

// Two cards pointing at each other must stop, not talk forever.
func TestMutualLinksStopAtRelayDepth(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)

	firstNode := nodeOf(t, canvas, first.ID)
	secondNode := nodeOf(t, canvas, second.ID)
	if _, err := store.CreateLink(ctx, firstNode, secondNode, domain.LinkOptions{}); err != nil {
		t.Fatalf("link A->B: %v", err)
	}
	if _, err := store.CreateLink(ctx, secondNode, firstNode, domain.LinkOptions{}); err != nil {
		t.Fatalf("link B->A: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}

	// answerAll fails the test if this never settles.
	answered := answerAll(t, store, "devam")

	// One original turn plus maxRelayDepth relayed turns, and no more.
	if answered != defaultRounds+1 {
		t.Fatalf("answered %d turns, want %d", answered, defaultRounds+1)
	}
	var depths []int
	rows, err := store.db.QueryContext(ctx, "SELECT relay_depth FROM chat_turns ORDER BY id")
	if err != nil {
		t.Fatalf("read depths: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var depth int
		if err := rows.Scan(&depth); err != nil {
			t.Fatalf("scan: %v", err)
		}
		depths = append(depths, depth)
		if depth > defaultRounds {
			t.Fatalf("relay depth %d exceeded the limit", depth)
		}
	}
	if len(depths) != defaultRounds+1 {
		t.Fatalf("turns = %v", depths)
	}
}

// A group card must relay once with every provider's answer, not once each.
func TestGroupCardRelaysOnceWithEveryAnswer(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	group, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Grup", Kind: domain.KindGroup, Providers: []string{"claude", "openai"},
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	target, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Hedef", Kind: domain.KindSolo, Providers: []string{"gemini"},
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, group.ID), nodeOf(t, canvas, target.ID), domain.LinkOptions{}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, group.ID, "karsilastir"); err != nil {
		t.Fatalf("turn: %v", err)
	}

	// Finish only the first provider: nothing may be relayed yet.
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim one: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "birinci", ""); err != nil {
		t.Fatalf("finish one: %v", err)
	}
	delivered, err := store.RelayTurn(ctx, job.TurnID)
	if err != nil {
		t.Fatalf("relay one: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("relayed %d times before the turn finished", delivered)
	}

	second, err := store.ClaimChatResponse(ctx)
	if err != nil || second == nil {
		t.Fatalf("claim two: %v", err)
	}
	if err := store.FinishChatResponse(ctx, second.ResponseID, domain.StatusPassed, "ikinci", ""); err != nil {
		t.Fatalf("finish two: %v", err)
	}
	if delivered, err = store.RelayTurn(ctx, second.TurnID); err != nil {
		t.Fatalf("relay two: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("relayed %d times, want 1", delivered)
	}

	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range after.Conversations {
		if item.ID != target.ID {
			continue
		}
		if len(item.Turns) != 1 {
			t.Fatalf("target got %d turns, want 1", len(item.Turns))
		}
		prompt := item.Turns[0].Prompt
		if !strings.Contains(prompt, "birinci") || !strings.Contains(prompt, "ikinci") {
			t.Fatalf("relayed prompt is missing an answer: %q", prompt)
		}
	}
}

func TestSelfLinkIsRefused(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Tek", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	node := nodeOf(t, canvas, conversation.ID)
	if _, err := store.CreateLink(ctx, node, node, domain.LinkOptions{}); err == nil {
		t.Fatal("a card was allowed to link to itself")
	}
}

func TestNoteCannotBeLinked(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Kart", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	note, err := store.CreateNote(ctx, domain.NewNote{Body: "not"})
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, conversation.ID), note.ID, domain.LinkOptions{}); err == nil {
		t.Fatal("a note was allowed to be a relay target")
	}
}

func TestPairLinksBothDirections(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)

	links, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 2})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("pair created %d links, want 2", len(links))
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Links) != 2 {
		t.Fatalf("canvas shows %d links", len(after.Links))
	}
	for _, link := range after.Links {
		if link.Mode != domain.LinkDialogue || link.MaxRounds != 2 {
			t.Fatalf("link = %+v", link)
		}
	}
}

// A paired conversation must stop after the link's own round budget, not the
// old global constant.
func TestPairedCardsStopAtLinkRoundBudget(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)

	if _, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 1}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answered := answerAll(t, store, "devam")
	// The opening turn plus exactly one relayed hop.
	if answered != 2 {
		t.Fatalf("answered %d turns, want 2", answered)
	}
}

func TestDialogueModeFramesTheRelayedAnswer(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)

	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 3}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "benim cevabim", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
		t.Fatalf("relay: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range after.Conversations {
		if item.ID != second.ID {
			continue
		}
		if len(item.Turns) != 1 {
			t.Fatalf("target got %d turns", len(item.Turns))
		}
		prompt := item.Turns[0].Prompt
		if !strings.Contains(prompt, "benim cevabim") {
			t.Fatalf("relayed answer is missing: %q", prompt)
		}
		// Dialogue mode must ask for a reply, not just hand the text over.
		if prompt == "benim cevabim" {
			t.Fatal("dialogue mode relayed the answer verbatim")
		}
	}
}

func TestReviewModeAsksForCritique(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)

	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkReview, MaxRounds: 2}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "yaz"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, _ := store.ClaimChatResponse(ctx)
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "kod ciktisi", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
		t.Fatalf("relay: %v", err)
	}
	after, _ := store.Canvas(ctx)
	for _, item := range after.Conversations {
		if item.ID == second.ID && len(item.Turns) == 1 {
			if !strings.Contains(item.Turns[0].Prompt, "İncele") &&
				!strings.Contains(item.Turns[0].Prompt, "incele") {
				t.Fatalf("review mode did not ask for a critique: %q", item.Turns[0].Prompt)
			}
		}
	}
}

func TestLinkOptionsAreClamped(t *testing.T) {
	huge := domain.LinkOptions{Mode: "uydurma", MaxRounds: 9999, UntilDone: true}.Normalised()
	if huge.Mode != domain.LinkRelay {
		t.Fatalf("unknown mode became %q", huge.Mode)
	}
	if huge.MaxRounds > 12 {
		t.Fatalf("round budget was not clamped: %d", huge.MaxRounds)
	}
	if huge.UntilDone {
		t.Fatal("a non-dialogue link retained until-done mode")
	}
	zero := domain.LinkOptions{}.Normalised()
	if zero.MaxRounds < 1 {
		t.Fatalf("default round budget = %d", zero.MaxRounds)
	}
}

func TestUntilDoneDialogueIgnoresRoundBudgetAndStopsAtMarker(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 1, UntilDone: true}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}

	for index, answer := range []string{"devam", "hala calisiyorum", "tamam " + dialogueDoneMarker} {
		job, err := store.ClaimChatResponse(ctx)
		if err != nil || job == nil {
			t.Fatalf("claim %d: %v", index, err)
		}
		if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, answer, ""); err != nil {
			t.Fatalf("finish %d: %v", index, err)
		}
		delivered, err := store.RelayTurn(ctx, job.TurnID)
		if err != nil {
			t.Fatalf("relay %d: %v", index, err)
		}
		if index < 2 && delivered != 1 {
			t.Fatalf("round %d delivered %d, want 1", index, delivered)
		}
		if index == 2 && delivered != 0 {
			t.Fatalf("completion marker delivered %d turns", delivered)
		}
	}
}

func TestUntilDoneDialogueStopsWhenConfiguredTestsPass(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 1, UntilDone: true}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE conversations SET loop_mode = ?, loop_last_signature = 'passed' WHERE id IN (?, ?)`,
		domain.LoopUntilPass, first.ID, second.ID); err != nil {
		t.Fatalf("mark tests passed: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "devam", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	delivered, err := store.RelayTurn(ctx, job.TurnID)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("passed tests delivered %d turns", delivered)
	}
}

// Choosing dialogue must make the link mutual, otherwise "karşılıklı" is a
// one-way handoff that ends after a single hop.
func TestDialogueLinkCreatesTheReturnLink(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	firstNode := nodeOf(t, canvas, first.ID)
	secondNode := nodeOf(t, canvas, second.ID)

	if _, err := store.CreateLink(ctx, firstNode, secondNode,
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 3}); err != nil {
		t.Fatalf("link: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Links) != 2 {
		t.Fatalf("dialogue produced %d links, want 2", len(after.Links))
	}
	seen := map[string]bool{}
	for _, link := range after.Links {
		seen[fmt.Sprintf("%d->%d", link.SourceID, link.TargetID)] = true
	}
	forward := fmt.Sprintf("%d->%d", firstNode, secondNode)
	backward := fmt.Sprintf("%d->%d", secondNode, firstNode)
	if !seen[forward] || !seen[backward] {
		t.Fatalf("links = %v", seen)
	}
}

// Switching an existing one-way link to dialogue must do the same.
func TestSwitchingToDialogueAddsTheReturnLink(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	firstNode := nodeOf(t, canvas, first.ID)
	secondNode := nodeOf(t, canvas, second.ID)

	link, err := store.CreateLink(ctx, firstNode, secondNode, domain.LinkOptions{})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	before, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(before.Links) != 1 {
		t.Fatalf("a relay link produced %d links", len(before.Links))
	}

	if err := store.UpdateLink(ctx, link.ID,
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 3}); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Links) != 2 {
		t.Fatalf("switching to dialogue produced %d links, want 2", len(after.Links))
	}
}

// Relay stays one-way: it is a handoff, not a conversation.
func TestRelayLinkStaysOneWay(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkRelay, MaxRounds: 3}); err != nil {
		t.Fatalf("link: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Links) != 1 {
		t.Fatalf("relay produced %d links, want 1", len(after.Links))
	}
}

// The receiving card must be told which card is speaking to it.
// The conclusion of an exchange is buried at the bottom of a card nobody
// scrolled to, so it goes on the board as its own card.
func TestFinishedDialogueLeavesAnOutcomeCard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 5, UntilDone: true}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	before, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	notesBefore := countNotes(before)

	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed,
		"is bitti, karar X "+dialogueDoneMarker, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
		t.Fatalf("relay: %v", err)
	}

	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas 2: %v", err)
	}
	if countNotes(after) != notesBefore+1 {
		t.Fatalf("outcome card was not created: %d notes, want %d", countNotes(after), notesBefore+1)
	}
	var outcome domain.CanvasNode
	for _, node := range after.Nodes {
		if node.Kind == domain.NodeNote {
			outcome = node
		}
	}
	if !strings.Contains(outcome.Body, "karar X") {
		t.Fatalf("outcome card lost the answer: %q", outcome.Body)
	}
	// Both cards must be named on it, or it says nothing about where it is from.
	if !strings.Contains(outcome.Body, first.Title) || !strings.Contains(outcome.Body, second.Title) {
		t.Fatalf("outcome card does not name both cards: %q", outcome.Body)
	}
}

func countNotes(canvas domain.Canvas) int {
	count := 0
	for _, node := range canvas.Nodes {
		if node.Kind == domain.NodeNote {
			count++
		}
	}
	return count
}

// Branching carries one answer into several independent cards. One provider per
// card: a group card would merge the paths that are supposed to diverge.
func TestBranchOpensOneCardPerProvider(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	branches, err := store.BranchFrom(ctx, source.ID, "devam edilecek cevap", []string{"openai", "gemini"})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("branch opened %d cards, want 2", len(branches))
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range after.Conversations {
		if item.ID == source.ID {
			continue
		}
		if len(item.Providers) != 1 {
			t.Fatalf("branch card has %d providers, want 1", len(item.Providers))
		}
		if len(item.Turns) != 1 || item.Turns[0].Prompt != "devam edilecek cevap" {
			t.Fatalf("branch card did not start from the answer: %+v", item.Turns)
		}
	}
	// The link is what shows where the work forked.
	if len(after.Links) != 2 {
		t.Fatalf("branch drew %d links, want 2", len(after.Links))
	}
}

func TestBranchRejectsAnEmptyAnswerOrNoProvider(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.BranchFrom(ctx, source.ID, "   ", []string{"openai"}); err == nil {
		t.Fatal("an empty answer was accepted")
	}
	if _, err := store.BranchFrom(ctx, source.ID, "cevap", nil); err == nil {
		t.Fatal("a branch with no provider was accepted")
	}
}

// A long session drifts from the role it was given, so the role is restated
// once the window is actually large — and not before, because the reminder is
// only worth its tokens against a full window.
func TestRoleIsRestatedOnlyWhenTheWindowIsFull(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetConversationRole(ctx, conversation.ID, "gözden geçiren"); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := store.RecordProviderSession(ctx, conversation.ID, "claude", "sid", ""); err != nil {
		t.Fatalf("session: %v", err)
	}

	// A small window carries no reminder.
	if err := store.RecordSessionContext(ctx, conversation.ID, "claude", 1_000); err != nil {
		t.Fatalf("context: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if strings.Contains(job.Prompt, "Hatırlatma") {
		t.Fatalf("role restated on a small window: %q", job.Prompt)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "cevap", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// A large one does.
	if err := store.RecordSessionContext(ctx, conversation.ID, "claude", contextRemindAt+1); err != nil {
		t.Fatalf("context 2: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ikinci"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	next, err := store.ClaimChatResponse(ctx)
	if err != nil || next == nil {
		t.Fatalf("claim 2: %v", err)
	}
	if !strings.Contains(next.Prompt, "gözden geçiren") {
		t.Fatalf("role was not restated on a full window: %q", next.Prompt)
	}
}

// A session too large to keep resuming is started over. The transcript takes
// over as context, so the conversation continues instead of restarting.
func TestFullContextRecyclesTheSessionAndKeepsTheHistory(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ilk soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "ilk cevap", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := store.RecordProviderSession(ctx, conversation.ID, "claude", "sid", ""); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := store.RecordSessionContext(ctx, conversation.ID, "claude", contextResetAt+1); err != nil {
		t.Fatalf("context: %v", err)
	}

	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ikinci soru"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	next, err := store.ClaimChatResponse(ctx)
	if err != nil || next == nil {
		t.Fatalf("claim 2: %v", err)
	}
	if next.SessionID != "" {
		t.Fatalf("a full session was still resumed: %q", next.SessionID)
	}
	// Dropping the session must not drop the conversation with it.
	if !strings.Contains(next.Prompt, "ilk cevap") {
		t.Fatalf("history was lost when the session was recycled: %q", next.Prompt)
	}
	var sessions int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM provider_sessions WHERE conversation_id = ?", conversation.ID).
		Scan(&sessions); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("recycled session row still present: %d", sessions)
	}
}

// A card that opens by asking a question must not end the exchange on its own:
// two cards would otherwise stall before either has done any work. The first
// request is nudged along; only a second one in a row parks the dialogue.
func TestUserInputRequestIsNudgedBeforeItParksTheDialogue(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 1, UntilDone: true}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}

	// First card asks for a decision: the other card is nudged rather than the
	// exchange stopping.
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed,
		"ne yapayim? "+dialogueUserInputMarker, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	delivered, err := store.RelayTurn(ctx, job.TurnID)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("first request for input delivered %d turns, want 1", delivered)
	}

	nudged, err := store.ClaimChatResponse(ctx)
	if err != nil || nudged == nil {
		t.Fatalf("claim nudge: %v", err)
	}
	var kind string
	if err := store.db.QueryRowContext(ctx,
		"SELECT kind FROM chat_turns WHERE id = ?", nudged.TurnID).Scan(&kind); err != nil {
		t.Fatalf("kind: %v", err)
	}
	if kind != domain.TurnNudge {
		t.Fatalf("relayed turn kind = %q, want %q", kind, domain.TurnNudge)
	}

	// The nudged card asks again anyway. That is a real request for the user.
	if err := store.FinishChatResponse(ctx, nudged.ResponseID, domain.StatusPassed,
		"karar senin "+dialogueUserInputMarker, ""); err != nil {
		t.Fatalf("finish nudge: %v", err)
	}
	delivered, err = store.RelayTurn(ctx, nudged.TurnID)
	if err != nil {
		t.Fatalf("relay nudge: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("second request for input delivered %d turns, want 0", delivered)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range after.Conversations {
		if item.DialogueState != domain.DialogueWaiting {
			t.Fatalf("card %d state = %q, want %q", item.ID, item.DialogueState, domain.DialogueWaiting)
		}
	}
}

// The arrangement is explained once. Repeating it on every hop wastes tokens
// and reads like the two cards keep being introduced to each other.
func TestBriefingIsSentOnlyOnce(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.PairNodes(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 5, Briefing: "ortak hedef"}); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "baslat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	for index := range 3 {
		job, err := store.ClaimChatResponse(ctx)
		if err != nil || job == nil {
			t.Fatalf("claim %d: %v", index, err)
		}
		if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "cevap", ""); err != nil {
			t.Fatalf("finish %d: %v", index, err)
		}
		if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
			t.Fatalf("relay %d: %v", index, err)
		}
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range after.Conversations {
		briefed := 0
		for _, turn := range item.Turns {
			if strings.Contains(turn.Prompt, "ortak hedef") {
				briefed++
			}
		}
		if briefed > 1 {
			t.Fatalf("card %d was briefed %d times", item.ID, briefed)
		}
	}
}

// A provider that resumes its own session already holds the history. Sending a
// transcript as well would deliver the same conversation twice.
func TestResumedSessionDropsTheReplayedTranscript(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ilk soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	// Without a session the transcript stands in for one.
	if !strings.Contains(job.Prompt, "ilk soru") {
		t.Fatalf("first prompt lost the question: %q", job.Prompt)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "ilk cevap", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := store.RecordProviderSession(ctx, conversation.ID, "claude", "sid", ""); err != nil {
		t.Fatalf("session: %v", err)
	}

	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ikinci soru"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	next, err := store.ClaimChatResponse(ctx)
	if err != nil || next == nil {
		t.Fatalf("claim 2: %v", err)
	}
	if next.Prompt != "ikinci soru" {
		t.Fatalf("resumed prompt = %q, want only the new message", next.Prompt)
	}
	if strings.Contains(next.Prompt, "ilk cevap") {
		t.Fatalf("history was replayed into a resumed session: %q", next.Prompt)
	}
}

func TestRelayedPromptNamesTheSpeakingCard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkDialogue, MaxRounds: 3}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "cevabim", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
		t.Fatalf("relay: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range after.Conversations {
		if item.ID != second.ID || len(item.Turns) == 0 {
			continue
		}
		// "A" is the title linkedPair gives the first card.
		if !strings.Contains(item.Turns[0].Prompt, first.Title) {
			t.Fatalf("prompt does not name the speaker %q: %q", first.Title, item.Turns[0].Prompt)
		}
	}
}
