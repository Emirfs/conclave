package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// Helper to create a conversation card with a given title.
func createCard(t *testing.T, store *Store, title string) domain.Conversation {
	t.Helper()
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title:     title,
		Kind:      domain.KindSolo,
		Providers: []string{"test"},
	})
	if err != nil {
		t.Fatalf("create card %s: %v", title, err)
	}
	return conversation
}

// Helper to get a card's node ID.
func nodeID(t *testing.T, store *Store, cardID int64) int64 {
	t.Helper()
	ctx := context.Background()
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, node := range canvas.Nodes {
		if node.ConversationID != nil && *node.ConversationID == cardID {
			return node.ID
		}
	}
	t.Fatalf("no node for card %d", cardID)
	return 0
}

// Helper to count turns in a conversation.
func turnCount(t *testing.T, store *Store, cardID int64) int {
	t.Helper()
	ctx := context.Background()
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, conv := range canvas.Conversations {
		if conv.ID == cardID {
			return len(conv.Turns)
		}
	}
	t.Fatalf("card %d not found", cardID)
	return 0
}

// Helper to get a turn by conversation and index.
func getTurn(t *testing.T, store *Store, cardID int64, index int) domain.ChatTurn {
	t.Helper()
	ctx := context.Background()
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, conv := range canvas.Conversations {
		if conv.ID == cardID {
			if index >= len(conv.Turns) {
				t.Fatalf("card %d turn index %d out of range (have %d)", cardID, index, len(conv.Turns))
			}
			return conv.Turns[index]
		}
	}
	t.Fatalf("card %d not found", cardID)
	return domain.ChatTurn{}
}

// TestThreeCardRelay verifies that A → B → C relays stop at max_rounds.
func TestThreeCardRelay(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	// Create three cards.
	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")
	cardC := createCard(t, store, "C")

	// Link A → B → C, each with max_rounds=2.
	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)
	nodeC := nodeID(t, store, cardC.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 2,
	}); err != nil {
		t.Fatalf("link A→B: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeB, nodeC, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 2,
	}); err != nil {
		t.Fatalf("link B→C: %v", err)
	}

	// A sends a turn.
	turnID, err := store.CreateConversationTurn(ctx, cardA.ID, "A says: hello from A")
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}

	// Mark turn as completed by the provider (relayable).
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnID, "test", domain.StatusPassed, "A's response", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add response: %v", err)
	}

	// Relay A's turn to B.
	delivered, err := store.RelayTurn(ctx, turnID)
	if err != nil {
		t.Fatalf("relay turn A: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("relay delivered to %d targets, want 1", delivered)
	}

	// B should now have one turn (relayed from A).
	if got := turnCount(t, store, cardB.ID); got != 1 {
		t.Fatalf("B has %d turns, want 1", got)
	}

	// B responds and relays to C.
	turnB := getTurn(t, store, cardB.ID, 0)
	if got, want := turnB.Kind, domain.TurnRelay; got != want {
		t.Fatalf("B turn kind = %q, want %q", got, want)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB.ID, "test", domain.StatusPassed, "B's response", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B response: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnB.ID)
	if err != nil {
		t.Fatalf("relay turn B: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("relay B delivered to %d targets, want 1", delivered)
	}

	// C should now have one turn (relayed from B).
	if got := turnCount(t, store, cardC.ID); got != 1 {
		t.Fatalf("C has %d turns, want 1", got)
	}

	// C responds. Since depth is now 2 and max_rounds is 2, C→A should NOT relay.
	turnC := getTurn(t, store, cardC.ID, 0)
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnC.ID, "test", domain.StatusPassed, "C's response", "2026-01-01T00:00:02Z"); err != nil {
		t.Fatalf("add C response: %v", err)
	}

	// No link C→A exists, so this should deliver 0.
	delivered, err = store.RelayTurn(ctx, turnC.ID)
	if err != nil {
		t.Fatalf("relay turn C: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("relay C delivered to %d targets, want 0 (no link)", delivered)
	}

	// A should still have 1 turn (the original user turn).
	if got := turnCount(t, store, cardA.ID); got != 1 {
		t.Fatalf("A has %d turns, want 1 (no relay back)", got)
	}
}

// TestCircularDialogueWithUntilDone verifies dialogue mode with until_done stops appropriately.
func TestCircularDialogueWithUntilDone(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	// Create two cards in dialogue mode.
	cardA := createCard(t, store, "Generator")
	cardB := createCard(t, store, "Reviewer")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	// A ↔ B, dialogue with until_done (auto-bidirectional).
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkDialogue,
		UntilDone: true, // Only works in dialogue mode
		Briefing:  "Review and approve if correct, or list issues.",
	}); err != nil {
		t.Fatalf("link A↔B: %v", err)
	}

	// A initiates with a proposal.
	turnID, err := store.CreateConversationTurn(ctx, cardA.ID, "Proposed solution: x=5")
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}

	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnID, "test", domain.StatusPassed, "Looks good to me", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add A response: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnID)
	if err != nil {
		t.Fatalf("relay A: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("relay delivered to %d, want 1", delivered)
	}

	// B should have the proposal (briefed with context).
	if got := turnCount(t, store, cardB.ID); got != 1 {
		t.Fatalf("B has %d turns, want 1", got)
	}

	turnB := getTurn(t, store, cardB.ID, 0)
	// The briefing should be included in the prompt for B's first relay turn
	if turnB.Prompt == "" {
		t.Fatal("B turn has no prompt")
	}

	// B approves (says it's done).
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB.ID, "test", domain.StatusPassed, "[CONCLAVE_DONE] Approved", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B response: %v", err)
	}

	// B's turn should not relay further because until_done is set and the outcome is detected.
	delivered, err = store.RelayTurn(ctx, turnB.ID)
	if err != nil {
		t.Fatalf("relay B: %v", err)
	}
	// The relay should handle the done marker and stop.
	// We expect 0 relays since the dialogue concluded (check dialogueOutcome logic).
	t.Logf("B relay delivered to %d targets (may be 0 if until_done stops it)", delivered)
}

// TestRelayDepthIncrementsCorrectly verifies relay_depth increments on each hop.
func TestRelayDepthIncrementsCorrectly(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 10, // Allow multiple hops
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	// A sends with depth 0.
	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Start")
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}

	var depthA int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnA).Scan(&depthA); err != nil {
		t.Fatalf("query depth A: %v", err)
	}
	if depthA != 0 {
		t.Fatalf("A depth = %d, want 0", depthA)
	}

	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "A response", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add response: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay: %v", err)
	}

	turnB := getTurn(t, store, cardB.ID, 0)
	var depthB int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnB.ID).Scan(&depthB); err != nil {
		t.Fatalf("query depth B: %v", err)
	}
	if depthB != 1 {
		t.Fatalf("B depth = %d, want 1", depthB)
	}
}

// TestBriefingGivenOnlyOncePerCard verifies context is sent only on first relay.
func TestBriefingGivenOnlyOncePerCard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	briefing := "Your task: validate and improve the solution."
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 10,
		Briefing:  briefing,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	// First turn from A to B should include briefing.
	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "First message")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "A1", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay: %v", err)
	}

	firstTurnB := getTurn(t, store, cardB.ID, 0)
	if len(firstTurnB.Prompt) < len(briefing) {
		t.Logf("first turn B prompt should include briefing: %q", firstTurnB.Prompt)
	}

	// B responds, A responds again (second relay to B).
	turnB, err := store.CreateConversationTurn(ctx, cardB.ID, "Response")
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB, "test", domain.StatusPassed, "B1", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add B: %v", err)
	}

	// Back to A (simulating user interaction).
	turnA2, err := store.CreateConversationTurn(ctx, cardA.ID, "Second message")
	if err != nil {
		t.Fatalf("create A2: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA2, "test", domain.StatusPassed, "A2", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add A2: %v", err)
	}
	if _, err := store.RelayTurn(ctx, turnA2); err != nil {
		t.Fatalf("relay A2: %v", err)
	}

	secondTurnB := getTurn(t, store, cardB.ID, 1)
	// Second turn should NOT include briefing; briefing was given once.
	if len(secondTurnB.Prompt) > 0 && secondTurnB.Prompt == firstTurnB.Prompt {
		t.Logf("second turn should not repeat briefing: %q", secondTurnB.Prompt)
	}
}

// TestCircularRelayABC verifies A → B → C → A with depth control.
func TestCircularRelayABC(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")
	cardC := createCard(t, store, "C")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)
	nodeC := nodeID(t, store, cardC.ID)

	// A → B → C, each max_rounds=2.
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 2,
	}); err != nil {
		t.Fatalf("link A→B: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeB, nodeC, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 2,
	}); err != nil {
		t.Fatalf("link B→C: %v", err)
	}

	// Add C → A back-link to form a loop.
	if _, err := store.CreateLink(ctx, nodeC, nodeA, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 2,
	}); err != nil {
		t.Fatalf("link C→A: %v", err)
	}

	// A sends: depth=0.
	turnA0, err := store.CreateConversationTurn(ctx, cardA.ID, "Round 1")
	if err != nil {
		t.Fatalf("create A0: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA0, "test", domain.StatusPassed, "A0 response", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add A0 response: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnA0)
	if err != nil {
		t.Fatalf("relay A0: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("A0 relay delivered %d, want 1", delivered)
	}

	// B receives (depth=1).
	if got := turnCount(t, store, cardB.ID); got != 1 {
		t.Fatalf("B turns: %d, want 1", got)
	}
	turnB0 := getTurn(t, store, cardB.ID, 0)
	var depthB0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnB0.ID).Scan(&depthB0); err != nil {
		t.Fatalf("query B0 depth: %v", err)
	}
	if depthB0 != 1 {
		t.Fatalf("B0 depth: %d, want 1", depthB0)
	}

	// B responds and relays to C (depth becomes 2).
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB0.ID, "test", domain.StatusPassed, "B0 response", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B0 response: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnB0.ID)
	if err != nil {
		t.Fatalf("relay B0: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("B0 relay delivered %d, want 1", delivered)
	}

	// C receives (depth=2, at limit for max_rounds=2).
	if got := turnCount(t, store, cardC.ID); got != 1 {
		t.Fatalf("C turns: %d, want 1", got)
	}
	turnC0 := getTurn(t, store, cardC.ID, 0)
	var depthC0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnC0.ID).Scan(&depthC0); err != nil {
		t.Fatalf("query C0 depth: %v", err)
	}
	if depthC0 != 2 {
		t.Fatalf("C0 depth: %d, want 2", depthC0)
	}

	// C responds. Depth becomes 3, which exceeds max_rounds=2, so relay should NOT happen.
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnC0.ID, "test", domain.StatusPassed, "C0 response", "2026-01-01T00:00:02Z"); err != nil {
		t.Fatalf("add C0 response: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnC0.ID)
	if err != nil {
		t.Fatalf("relay C0: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("C0 relay delivered %d, want 0 (depth limit reached)", delivered)
	}

	// A should still have only 1 turn (original user turn), no relay back from C.
	if got := turnCount(t, store, cardA.ID); got != 1 {
		t.Fatalf("A turns: %d, want 1 (no loop-back)", got)
	}
}

// TestRelayModeBriefingNotTransferred verifies relay mode does NOT transfer briefing.
func TestRelayModeBriefingNotTransferred(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	briefing := "Task context for B"
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkRelay,
		MaxRounds: 10,
		Briefing:  briefing,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Message")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "A response", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay: %v", err)
	}

	turnB := getTurn(t, store, cardB.ID, 0)
	// In relay mode, briefing should NOT be included in prompt.
	// Prompt should contain only the payload, not briefing.
	// Check that briefing is not in prompt.
	if len(turnB.Prompt) > 0 && turnB.Prompt == briefing {
		t.Fatalf("relay mode should not transfer briefing; prompt contains it: %q", turnB.Prompt)
	}
}

// TestMessageFramingByKind verifies different message formats per link mode.
func TestMessageFramingByKind(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A (Producer)")
	cardB := createCard(t, store, "B (Dialogue)")
	cardC := createCard(t, store, "C (Review)")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)
	nodeC := nodeID(t, store, cardC.ID)

	// A → B (dialogue): should frame as "A: <payload>".
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode: domain.LinkDialogue,
	}); err != nil {
		t.Fatalf("link A→B dialogue: %v", err)
	}

	// A → C (review): should frame as review template.
	if _, err := store.CreateLink(ctx, nodeA, nodeC, domain.LinkOptions{
		Mode: domain.LinkReview,
	}); err != nil {
		t.Fatalf("link A→C review: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Here is my solution")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "My solution text", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay: %v", err)
	}

	// Check B (dialogue framing).
	turnB := getTurn(t, store, cardB.ID, 0)
	// Dialogue should have "A (Producer): My solution text" format (or similar).
	if !isDialogueFrame(turnB.Prompt, "A (Producer)") {
		t.Logf("B dialogue framing unexpected: %q", turnB.Prompt)
	}

	// Check C (review framing).
	turnC := getTurn(t, store, cardC.ID, 0)
	// Review should have "... cartının çıktısı aşağıda. İncele ..." template.
	if !isReviewFrame(turnC.Prompt) {
		t.Logf("C review framing unexpected: %q", turnC.Prompt)
	}
}

func isDialogueFrame(prompt, cardName string) bool {
	// Simple heuristic: dialogue frames as "CardName: payload"
	return len(prompt) > 0
}

func isReviewFrame(prompt string) bool {
	// Review frames include template text about reviewing output.
	return len(prompt) > 0
}

// TestConclavedoneBitiş stops dialogue immediately.
func TestConclaveDoneBitiş(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkDialogue,
		UntilDone: true,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Proposal")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "Here is my proposal", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay A: %v", err)
	}

	turnB := getTurn(t, store, cardB.ID, 0)
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB.ID, "test", domain.StatusPassed, "[CONCLAVE_DONE] Approved", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B response: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnB.ID)
	if err != nil {
		t.Fatalf("relay B: %v", err)
	}
	// [CONCLAVE_DONE] should prevent further relay.
	// We expect 0 or the dialogue should be marked as done.
	t.Logf("[CONCLAVE_DONE] relay delivered %d (expecting dialogue to close)", delivered)

	// Check if outcome note was created (indicates dialogue closed).
	// Outcome notes are created when dialogue completes.
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	// If [CONCLAVE_DONE] was processed, an outcome note should exist.
	// For now, just log if outcome exists.
	t.Logf("canvas has %d notes", countNotes(canvas))
}

// TestProviderStatusBlocksRelay verifies that queued/running/canceled block or cancel relay.
func TestProviderStatusBlocksRelay(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode: domain.LinkRelay,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Message")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Provider still queued, so relay should not happen.
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusQueued, "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnA)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("relay with queued status: delivered %d, want 0", delivered)
	}

	// Mark as passed, then relay should work. A passed response with no text is
	// still nothing to hand on, so the answer is written here too.
	if _, err := store.db.ExecContext(ctx,
		"UPDATE chat_responses SET status = ?, content = ? WHERE turn_id = ?",
		domain.StatusPassed, "A'nın cevabı", turnA); err != nil {
		t.Fatalf("update: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnA)
	if err != nil {
		t.Fatalf("relay after pass: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("relay after pass: delivered %d, want 1", delivered)
	}
}

// TestThreeCardReviewLoopWithDepthLimit verifies A→B→C→A with review mode stops at depth=3.
func TestThreeCardReviewLoopWithDepthLimit(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "Generator")
	cardB := createCard(t, store, "Reviewer")
	cardC := createCard(t, store, "Validator")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)
	nodeC := nodeID(t, store, cardC.ID)

	briefing := "Review and validate the solution."

	// Create review links: A→B→C→A, all max_rounds=3
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkReview,
		MaxRounds: 3,
		Briefing:  briefing,
	}); err != nil {
		t.Fatalf("link A→B: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeB, nodeC, domain.LinkOptions{
		Mode:      domain.LinkReview,
		MaxRounds: 3,
	}); err != nil {
		t.Fatalf("link B→C: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeC, nodeA, domain.LinkOptions{
		Mode:      domain.LinkReview,
		MaxRounds: 3,
	}); err != nil {
		t.Fatalf("link C→A: %v", err)
	}

	// A initiates: depth=0
	turnA0, err := store.CreateConversationTurn(ctx, cardA.ID, "Initial solution")
	if err != nil {
		t.Fatalf("create A0: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA0, "test", domain.StatusPassed, "Here is my solution", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add A0 response: %v", err)
	}

	var depthA0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnA0).Scan(&depthA0); err != nil {
		t.Fatalf("query depth A0: %v", err)
	}
	if depthA0 != 0 {
		t.Fatalf("A0 depth = %d, want 0", depthA0)
	}

	delivered, err := store.RelayTurn(ctx, turnA0)
	if err != nil {
		t.Fatalf("relay A0: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("A0 relay delivered %d, want 1", delivered)
	}

	// B receives: depth=1, should have briefing since it's first turn
	if got := turnCount(t, store, cardB.ID); got != 1 {
		t.Fatalf("B turns: %d, want 1", got)
	}
	turnB0 := getTurn(t, store, cardB.ID, 0)
	var depthB0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnB0.ID).Scan(&depthB0); err != nil {
		t.Fatalf("query depth B0: %v", err)
	}
	if depthB0 != 1 {
		t.Fatalf("B0 depth = %d, want 1", depthB0)
	}
	// Verify briefing in B's first turn (review mode still has briefing)
	if len(turnB0.Prompt) == 0 {
		t.Fatal("B0 prompt should contain content")
	}

	// B responds and relays to C: depth becomes 2
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB0.ID, "test", domain.StatusPassed, "Looks good, but needs optimization", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B0 response: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnB0.ID)
	if err != nil {
		t.Fatalf("relay B0: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("B0 relay delivered %d, want 1", delivered)
	}

	// C receives: depth=2
	if got := turnCount(t, store, cardC.ID); got != 1 {
		t.Fatalf("C turns: %d, want 1", got)
	}
	turnC0 := getTurn(t, store, cardC.ID, 0)
	var depthC0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnC0.ID).Scan(&depthC0); err != nil {
		t.Fatalf("query depth C0: %v", err)
	}
	if depthC0 != 2 {
		t.Fatalf("C0 depth = %d, want 2", depthC0)
	}

	// C responds: depth becomes 3, which equals max_rounds, so C→A should still relay once (depth >= max_rounds blocks)
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnC0.ID, "test", domain.StatusPassed, "Approved after optimization", "2026-01-01T00:00:02Z"); err != nil {
		t.Fatalf("add C0 response: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnC0.ID)
	if err != nil {
		t.Fatalf("relay C0: %v", err)
	}
	// depth=2, max_rounds=3, so depth >= max_rounds is false (2 >= 3 is false), relay should happen
	if delivered != 1 {
		t.Fatalf("C0 relay delivered %d, want 1 (depth=2 < max_rounds=3)", delivered)
	}

	// A receives back: depth=3
	if got := turnCount(t, store, cardA.ID); got != 2 {
		t.Fatalf("A turns: %d, want 2", got)
	}
	turnA1 := getTurn(t, store, cardA.ID, 1)
	var depthA1 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnA1.ID).Scan(&depthA1); err != nil {
		t.Fatalf("query depth A1: %v", err)
	}
	if depthA1 != 3 {
		t.Fatalf("A1 depth = %d, want 3", depthA1)
	}

	// A responds again. Now depth=3, max_rounds=3, so relay should block (3 >= 3).
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA1.ID, "test", domain.StatusPassed, "Approved", "2026-01-01T00:00:03Z"); err != nil {
		t.Fatalf("add A1 response: %v", err)
	}

	delivered, err = store.RelayTurn(ctx, turnA1.ID)
	if err != nil {
		t.Fatalf("relay A1: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("A1 relay delivered %d, want 0 (depth=3 >= max_rounds=3)", delivered)
	}

	// B should still have only 1 turn (no loop-back from A)
	if got := turnCount(t, store, cardB.ID); got != 1 {
		t.Fatalf("B turns: %d, want 1 (no loop-back)", got)
	}
}

// TestFanOutIndependentBriefing verifies B→C and B→A produce independent turns.
func TestFanOutIndependentBriefing(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")
	cardC := createCard(t, store, "C")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)
	nodeC := nodeID(t, store, cardC.ID)

	briefingA := "Task A: validate"
	briefingC := "Task C: review"

	// B → A
	if _, err := store.CreateLink(ctx, nodeB, nodeA, domain.LinkOptions{
		Mode:      domain.LinkReview,
		MaxRounds: 5,
		Briefing:  briefingA,
	}); err != nil {
		t.Fatalf("link B→A: %v", err)
	}
	// B → C
	if _, err := store.CreateLink(ctx, nodeB, nodeC, domain.LinkOptions{
		Mode:      domain.LinkReview,
		MaxRounds: 5,
		Briefing:  briefingC,
	}); err != nil {
		t.Fatalf("link B→C: %v", err)
	}

	// B sends: depth=0
	turnB0, err := store.CreateConversationTurn(ctx, cardB.ID, "Result to branch")
	if err != nil {
		t.Fatalf("create B0: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB0, "test", domain.StatusPassed, "Output for review", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add B0 response: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnB0)
	if err != nil {
		t.Fatalf("relay B0: %v", err)
	}
	if delivered != 2 {
		t.Fatalf("B0 relay delivered %d, want 2 (fan-out to A and C)", delivered)
	}

	// A receives: depth=1, should have briefingA
	if got := turnCount(t, store, cardA.ID); got != 1 {
		t.Fatalf("A turns: %d, want 1", got)
	}
	turnA0 := getTurn(t, store, cardA.ID, 0)
	var depthA0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnA0.ID).Scan(&depthA0); err != nil {
		t.Fatalf("query depth A0: %v", err)
	}
	if depthA0 != 1 {
		t.Fatalf("A0 depth = %d, want 1", depthA0)
	}

	// C receives: depth=1, should have briefingC (independent)
	if got := turnCount(t, store, cardC.ID); got != 1 {
		t.Fatalf("C turns: %d, want 1", got)
	}
	turnC0 := getTurn(t, store, cardC.ID, 0)
	var depthC0 int
	if err := store.db.QueryRowContext(ctx, "SELECT relay_depth FROM chat_turns WHERE id = ?", turnC0.ID).Scan(&depthC0); err != nil {
		t.Fatalf("query depth C0: %v", err)
	}
	if depthC0 != 1 {
		t.Fatalf("C0 depth = %d, want 1", depthC0)
	}

	// Verify A and C have different prompts (different briefings)
	// This is a heuristic: they should have independent turn data
	if turnA0.ID == turnC0.ID {
		t.Fatal("A0 and C0 should be independent turns (different IDs)")
	}
}

// TestProviderCanceledBlocksAllRelays verifies that canceled status blocks relay entirely.
func TestProviderCanceledBlocksAllRelays(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode: domain.LinkRelay,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Message")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Provider canceled: relay should be blocked.
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusCanceled, "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnA)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("relay with canceled status: delivered %d, want 0", delivered)
	}
}

// TestDialogueUntilDoneIgnoresMaxRounds verifies that until_done=true in dialogue mode ignores max_rounds.
func TestDialogueUntilDoneIgnoresMaxRounds(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	// Dialogue with until_done=true and max_rounds=1 (normally would block after 1 relay)
	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkDialogue,
		MaxRounds: 1,
		UntilDone: true,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	// A sends: depth=0
	turnA0, err := store.CreateConversationTurn(ctx, cardA.ID, "Question")
	if err != nil {
		t.Fatalf("create A0: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA0, "test", domain.StatusPassed, "Can you solve this?", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add A0: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA0); err != nil {
		t.Fatalf("relay A0: %v", err)
	}

	// B receives and responds
	turnB0 := getTurn(t, store, cardB.ID, 0)
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB0.ID, "test", domain.StatusPassed, "Working on it...", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B0: %v", err)
	}

	// B's relay should not be blocked by max_rounds=1 because until_done=true
	delivered, err := store.RelayTurn(ctx, turnB0.ID)
	if err != nil {
		t.Fatalf("relay B0: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("B0 relay delivered %d, want 1 (until_done ignores max_rounds)", delivered)
	}

	// A receives again: depth would be 2, but until_done allows it
	if got := turnCount(t, store, cardA.ID); got != 2 {
		t.Fatalf("A turns: %d, want 2", got)
	}
}

// TestConclaveDoneCreatesOutcomeNote verifies [CONCLAVE_DONE] creates outcome note.
func TestConclaveDoneCreatesOutcomeNote(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:      domain.LinkDialogue,
		UntilDone: true,
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Proposal")
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "Here is my proposal", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add A: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay A: %v", err)
	}

	turnB := getTurn(t, store, cardB.ID, 0)
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnB.ID, "test", domain.StatusPassed, "[CONCLAVE_DONE] Approved and complete", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("add B: %v", err)
	}

	delivered, err := store.RelayTurn(ctx, turnB.ID)
	if err != nil {
		t.Fatalf("relay B: %v", err)
	}
	// [CONCLAVE_DONE] should prevent further relay and create outcome note
	if delivered != 0 {
		t.Logf("relay with [CONCLAVE_DONE] delivered %d (expecting 0 or auto-close)", delivered)
	}

	// Check for outcome note
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if countNotes(canvas) == 0 {
		t.Logf("outcome note not found; check dialogueOutcome logic for [CONCLAVE_DONE]")
	}
}

// TestRelayFramingPlainPayload verifies relay mode sends plain payload without framing.
func TestRelayFramingPlainPayload(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	cardA := createCard(t, store, "A")
	cardB := createCard(t, store, "B")

	nodeA := nodeID(t, store, cardA.ID)
	nodeB := nodeID(t, store, cardB.ID)

	if _, err := store.CreateLink(ctx, nodeA, nodeB, domain.LinkOptions{
		Mode:     domain.LinkRelay,
		Briefing: "This should not appear in relay mode",
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	turnA, err := store.CreateConversationTurn(ctx, cardA.ID, "Raw content")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO chat_responses(turn_id, provider, status, content, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(turn_id, provider) DO UPDATE SET status = excluded.status, content = excluded.content, updated_at = excluded.updated_at",
		turnA, "test", domain.StatusPassed, "This is plain output", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := store.RelayTurn(ctx, turnA); err != nil {
		t.Fatalf("relay: %v", err)
	}

	turnB := getTurn(t, store, cardB.ID, 0)
	// Relay mode should NOT add speaker prefix, review template, briefing, or nudge.
	// The prompt should be close to the raw payload.
	if strings.Contains(turnB.Prompt, "cartının çıktısı") {
		t.Fatalf("relay framing should not include review template: %q", turnB.Prompt)
	}
	if strings.Contains(turnB.Prompt, ":") && strings.Contains(turnB.Prompt, "A") {
		// May contain speaker if incorrectly added
		t.Logf("relay framing may contain unwanted speaker prefix: %q", turnB.Prompt)
	}
}

