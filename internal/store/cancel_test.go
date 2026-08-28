package store

import (
	"context"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// A queued response is owned by nobody, so stopping a card must finish it on
// the spot rather than wait for a worker that will never claim it.
func TestCancelFinishesQueuedResponses(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "uzun bir is"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	stopped, err := store.RequestConversationCancel(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if stopped != 1 {
		t.Fatalf("stopped = %d, want 1", stopped)
	}
	if status := responseStatus(t, store, conversation.ID); status != string(domain.StatusCanceled) {
		t.Fatalf("status = %q, want canceled", status)
	}
	// A canceled response must not be handed to a worker afterwards.
	job, err := store.ClaimChatResponse(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job != nil {
		t.Fatalf("claimed a canceled response %d", job.ResponseID)
	}
}

// A running response belongs to a worker, so the request is only recorded; the
// worker is what actually ends the process.
func TestCancelFlagsRunningResponseForItsWorker(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "uzun bir is"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.RequestConversationCancel(ctx, conversation.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	requested, err := store.ChatCancelRequested(ctx, job.ResponseID)
	if err != nil {
		t.Fatalf("cancel requested: %v", err)
	}
	if !requested {
		t.Fatal("running response was not flagged for its worker")
	}
	if status := responseStatus(t, store, conversation.ID); status != string(domain.StatusRunning) {
		t.Fatalf("status = %q, want running until the worker finishes it", status)
	}
	// Partial output survives the stop: it is what the provider managed to say.
	if err := store.UpdateChatResponseContent(ctx, job.ResponseID, "yarim cevap", ""); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := store.CancelChatResponse(ctx, job.ResponseID); err != nil {
		t.Fatalf("finish cancel: %v", err)
	}
	var status, content string
	if err := store.db.QueryRowContext(ctx,
		"SELECT status, content FROM chat_responses WHERE id = ?", job.ResponseID).
		Scan(&status, &content); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if status != string(domain.StatusCanceled) || content != "yarim cevap" {
		t.Fatalf("status = %q content = %q, want canceled and the partial answer", status, content)
	}
}

// A stopped turn is not an answer, so nothing travels along the card's links.
func TestCanceledTurnIsNotRelayed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkRelay, MaxRounds: 3}); err != nil {
		t.Fatalf("link: %v", err)
	}
	turnID, err := store.CreateConversationTurn(ctx, first.ID, "baslat")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.UpdateChatResponseContent(ctx, job.ResponseID, "yarim cevap", ""); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if _, err := store.RequestConversationCancel(ctx, first.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := store.CancelChatResponse(ctx, job.ResponseID); err != nil {
		t.Fatalf("finish cancel: %v", err)
	}
	delivered, err := store.RelayTurn(ctx, turnID)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("relayed a stopped turn to %d cards", delivered)
	}
}

// Stopping a card also disarms its cycle: a stop should not leave a timer about
// to start the next round.
func TestCancelDisarmsTheCardCycle(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE conversations SET loop_running = 1 WHERE id = ?", conversation.ID); err != nil {
		t.Fatalf("arm loop: %v", err)
	}
	if _, err := store.RequestConversationCancel(ctx, conversation.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var running int
	if err := store.db.QueryRowContext(ctx,
		"SELECT loop_running FROM conversations WHERE id = ?", conversation.ID).Scan(&running); err != nil {
		t.Fatalf("read loop: %v", err)
	}
	if running != 0 {
		t.Fatal("cycle is still armed after a stop")
	}
}

func responseStatus(t *testing.T, store *Store, conversationID int64) string {
	t.Helper()
	var status string
	err := store.db.QueryRow(`
SELECT r.status FROM chat_responses r
JOIN chat_turns t ON t.id = r.turn_id
WHERE t.conversation_id = ? ORDER BY r.id DESC LIMIT 1`, conversationID).Scan(&status)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}
