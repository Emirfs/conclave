package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// The model a card is given is what the provider is actually run on, and it
// outlives the session that recorded a different one.
func TestConversationModelIsUsedForTheNextJob(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Model", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetConversationModel(ctx, conversation.ID, "claude", "opus"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "merhaba"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v (job %v)", err, job)
	}
	if job.Model != "opus" {
		t.Fatalf("model = %q, want opus", job.Model)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if got := canvas.Conversations[0].Models["claude"]; got != "opus" {
		t.Fatalf("card model = %q, want opus", got)
	}
}

// Changing the model must not resume a session that was started on another one:
// the provider would either keep the old model or refuse the resume outright.
func TestChangingModelDropsTheProviderSession(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Oturum", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ilk"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v (job %v)", err, job)
	}
	if err := store.RecordProviderSession(ctx, conversation.ID, "claude", "session-1", "sonnet"); err != nil {
		t.Fatalf("record session: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "tamam", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// Same model: the session is worth resuming.
	if err := store.SetConversationModel(ctx, conversation.ID, "claude", "sonnet"); err != nil {
		t.Fatalf("set same model: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ikinci"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	resumed, err := store.ClaimChatResponse(ctx)
	if err != nil || resumed == nil {
		t.Fatalf("claim: %v (job %v)", err, resumed)
	}
	if resumed.SessionID != "session-1" || resumed.Model != "sonnet" {
		t.Fatalf("session = %q model = %q, want session-1/sonnet", resumed.SessionID, resumed.Model)
	}
	if err := store.FinishChatResponse(ctx, resumed.ResponseID, domain.StatusPassed, "tamam", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// Another model: the session goes, and the transcript carries the
	// conversation into the new one instead.
	if err := store.SetConversationModel(ctx, conversation.ID, "claude", "opus"); err != nil {
		t.Fatalf("set new model: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ucuncu"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	fresh, err := store.ClaimChatResponse(ctx)
	if err != nil || fresh == nil {
		t.Fatalf("claim: %v (job %v)", err, fresh)
	}
	if fresh.SessionID != "" {
		t.Fatalf("session = %q, want it dropped", fresh.SessionID)
	}
	if fresh.Model != "opus" {
		t.Fatalf("model = %q, want opus", fresh.Model)
	}
	if !strings.Contains(fresh.Prompt, "ilk") {
		t.Fatalf("prompt does not carry the transcript: %q", fresh.Prompt)
	}
}

// A model changed while the turn was still running must not be undone by that
// turn recording its own session afterwards.
func TestSessionRecordedOnTheOldModelIsNotResumed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Yaris", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetConversationModel(ctx, conversation.ID, "claude", "sonnet"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ilk"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v (job %v)", err, job)
	}
	// The user picks another model while the run is still in flight.
	if err := store.SetConversationModel(ctx, conversation.ID, "claude", "opus"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := store.RecordProviderSession(ctx, conversation.ID, "claude", "session-1", job.Model); err != nil {
		t.Fatalf("record session: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "tamam", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "ikinci"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	next, err := store.ClaimChatResponse(ctx)
	if err != nil || next == nil {
		t.Fatalf("claim: %v (job %v)", err, next)
	}
	if next.SessionID != "" || next.Model != "opus" {
		t.Fatalf("session = %q model = %q, want dropped session on opus", next.SessionID, next.Model)
	}
}

// Clearing the model hands the choice back to the provider's own default.
func TestClearingModelRemovesTheChoice(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()

	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Varsayilan", Kind: domain.KindSolo, Providers: []string{"ollama"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetConversationModel(ctx, conversation.ID, "ollama", "qwen3:8b"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := store.SetConversationModel(ctx, conversation.ID, "ollama", ""); err != nil {
		t.Fatalf("clear model: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if _, chosen := canvas.Conversations[0].Models["ollama"]; chosen {
		t.Fatalf("model survived being cleared: %v", canvas.Conversations[0].Models)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "merhaba"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v (job %v)", err, job)
	}
	if job.Model != "" {
		t.Fatalf("model = %q, want the provider default", job.Model)
	}
}

// A card that is gone is reported as such rather than silently accepting a
// model nobody will ever run on.
func TestSetModelOnMissingConversation(t *testing.T) {
	store := openTemp(t)
	err := store.SetConversationModel(context.Background(), 4040, "claude", "opus")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}
