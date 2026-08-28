package store

import (
	"context"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/provider"
)

// answer runs one turn to completion on a card and records what it cost.
func answer(t *testing.T, store *Store, conversationID int64, prompt string, input, output int) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateConversationTurn(ctx, conversationID, prompt); err != nil {
		t.Fatalf("turn: %v", err)
	}
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.RecordChatUsage(ctx, job.ResponseID, input, output); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "cevap", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func TestUsageTotalsPerProvider(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	second, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "B", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	answer(t, store, first.ID, "bir", 1000, 200)
	answer(t, store, second.ID, "iki", 500, 100)

	report, err := store.Usage(ctx, 7)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(report.Providers) != 1 {
		t.Fatalf("report carries %+v", report.Providers)
	}
	got := report.Providers[0]
	if got.Provider != "claude" || got.Turns != 2 || got.InputTokens != 1500 || got.OutputTokens != 300 {
		t.Fatalf("usage = %+v", got)
	}
	// Two cards, not two turns on one: the distinction is what says whether the
	// spend went into one line of work or several.
	if got.Cards != 2 {
		t.Fatalf("cards = %d, want 2", got.Cards)
	}
}

// A turn still waiting or still running has not spent anything yet, so it must
// not be counted as if it had.
func TestUsageIgnoresUnfinishedTurns(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, conversation.ID, "bekleyen"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	report, err := store.Usage(ctx, 7)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(report.Providers) != 0 {
		t.Fatalf("a queued turn was counted: %+v", report.Providers)
	}
}

// A provider that reports no token counts must still show up: reporting nothing
// is not the same as doing nothing.
func TestUsageCountsTurnsWithoutTokenReports(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"ollama"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	answer(t, store, conversation.ID, "soru", 0, 0)
	report, err := store.Usage(ctx, 7)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(report.Providers) != 1 || report.Providers[0].Turns != 1 {
		t.Fatalf("report = %+v", report.Providers)
	}
}

// The provider's own allowance report rides along, so one panel answers both
// "what did I spend" and "how much is left".
func TestUsageCarriesTheProviderQuota(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "A", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	answer(t, store, conversation.ID, "soru", 10, 5)
	if err := store.RecordProviderQuota(ctx, "claude", provider.Quota{
		ShortLabel: "5 saat", ShortUtilization: 0.4,
		LongLabel: "7 gün", LongUtilization: 0.1,
	}); err != nil {
		t.Fatalf("quota: %v", err)
	}
	report, err := store.Usage(ctx, 7)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(report.Providers) != 1 || report.Providers[0].Quota == nil {
		t.Fatalf("report = %+v", report.Providers)
	}
	if report.Providers[0].Quota.ShortLabel != "5 saat" {
		t.Fatalf("quota = %+v", report.Providers[0].Quota)
	}
}
