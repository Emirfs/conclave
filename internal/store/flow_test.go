package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// card creates a conversation with a single provider, which is what these tests
// want: a group card would answer twice and blur what the run counted.
func card(t *testing.T, store *Store, title string) domain.Conversation {
	t.Helper()
	conversation, err := store.CreateConversation(context.Background(), domain.NewConversation{
		Title: title, Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return conversation
}

// A message a person sends starts a run, and everything it sets off belongs to
// that same run — which is what makes the spread one thing rather than several.
func TestOneMessageIsOneRun(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "başlat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "cevap")

	rows, err := store.db.QueryContext(ctx,
		"SELECT DISTINCT COALESCE(flow_run_id, 0) FROM chat_turns")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	defer rows.Close()
	var runs []int64
	for rows.Next() {
		var run int64
		if err := rows.Scan(&run); err != nil {
			t.Fatalf("scan: %v", err)
		}
		runs = append(runs, run)
	}
	if len(runs) != 1 || runs[0] == 0 {
		t.Fatalf("turns belong to runs %v, want exactly one non-zero run", runs)
	}

	// A run with nothing left to do closes itself; a board that still says
	// "running" after everything answered is worse than one that says nothing.
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Runs) != 0 {
		t.Fatalf("%d runs still active after everything answered", len(after.Runs))
	}
}

// A branch that finishes first must not close a run its sibling is still in.
func TestRunStaysOpenWhileASiblingWorks(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source := card(t, store, "A")
	left := card(t, store, "B")
	right := card(t, store, "C")
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, target := range []int64{left.ID, right.ID} {
		if _, err := store.CreateLink(ctx, nodeOf(t, canvas, source.ID), nodeOf(t, canvas, target),
			domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	if _, err := store.CreateConversationTurn(ctx, source.ID, "dağıt"); err != nil {
		t.Fatalf("turn: %v", err)
	}

	// Answer the first card, then only one of the two branches.
	for range 2 {
		job, err := store.ClaimChatResponse(ctx)
		if err != nil || job == nil {
			t.Fatalf("claim: %v (job %v)", err, job)
		}
		if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "tamam", ""); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
			t.Fatalf("relay: %v", err)
		}
	}
	mid, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(mid.Runs) != 1 {
		t.Fatalf("%d runs active while a branch is still working, want 1", len(mid.Runs))
	}
}

// A join holds what each line said rather than letting the same card be started
// twice by two answers arriving separately.
func TestJoinWaitsForEveryLine(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	left := card(t, store, "Sol")
	right := card(t, store, "Sağ")
	sink := card(t, store, "Toplayıcı")
	join, err := store.CreateJoin(ctx, domain.NewNote{Body: "Birleştir", X: 10, Y: 10})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, source := range []int64{left.ID, right.ID} {
		if _, err := store.CreateLink(ctx, nodeOf(t, canvas, source), join.ID,
			domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
			t.Fatalf("link into join: %v", err)
		}
	}
	if _, err := store.CreateLink(ctx, join.ID, nodeOf(t, canvas, sink.ID),
		domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
		t.Fatalf("link out of join: %v", err)
	}

	// The left card speaks first. The join must hold what it said.
	if _, err := store.CreateConversationTurn(ctx, left.ID, "soldan"); err != nil {
		t.Fatalf("turn left: %v", err)
	}
	answerAll(t, store, "sol cevabı")
	if got := turnsOf(t, store, sink.ID); got != 0 {
		t.Fatalf("sink received %d turns after one line spoke, want 0", got)
	}
	waiting, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(waiting.Joins) != 1 || waiting.Joins[0].Expected != 2 {
		t.Fatalf("join reports %+v, want one join expecting 2", waiting.Joins)
	}
}

// A join that has heard from everyone hands on one message carrying every
// answer, not one message per answer.
func TestJoinCombinesWhatItHeard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	left := card(t, store, "Sol")
	right := card(t, store, "Sağ")
	join, err := store.CreateJoin(ctx, domain.NewNote{Body: "Birleştir"})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, source := range []int64{left.ID, right.ID} {
		if _, err := store.CreateLink(ctx, nodeOf(t, canvas, source), join.ID,
			domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
			t.Fatalf("link: %v", err)
		}
	}
	if _, _, err := store.deliverToJoin(ctx, 0, join.ID, nodeOf(t, canvas, left.ID),
		"Sol", "birinci"); err != nil {
		t.Fatalf("deliver left: %v", err)
	}
	combined, ready, err := store.deliverToJoin(ctx, 0, join.ID, nodeOf(t, canvas, right.ID),
		"Sağ", "ikinci")
	if err != nil {
		t.Fatalf("deliver right: %v", err)
	}
	if !ready {
		t.Fatal("join did not hand on after both lines spoke")
	}
	if !strings.Contains(combined, "birinci") || !strings.Contains(combined, "ikinci") {
		t.Fatalf("combined message = %q; both answers should be in it", combined)
	}
	if !strings.Contains(combined, "Sol") || !strings.Contains(combined, "Sağ") {
		t.Fatalf("combined message = %q; each answer should say who gave it", combined)
	}

	// Having spoken, the join is empty again and ready for the next round.
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Joins) != 1 || after.Joins[0].Waiting != 0 {
		t.Fatalf("join still holds %+v after handing on", after.Joins)
	}
}

// Stopping a run stops every card in it. A run is what a person started, so it
// is what they should be able to stop.
func TestStoppingARunStopsEveryCardInIt(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "başlat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	// Answer the first card so the second one is queued as well.
	job, err := store.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v (job %v)", err, job)
	}
	if err := store.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, "devam", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := store.RelayTurn(ctx, job.TurnID); err != nil {
		t.Fatalf("relay: %v", err)
	}

	stopped, err := store.StopFlowRun(ctx, currentRun(t, store))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopped == 0 {
		t.Fatal("stopping the run stopped nothing")
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Runs) != 0 {
		t.Fatalf("%d runs still active after being stopped", len(after.Runs))
	}
}

// The run budget is the width limit a per-link round count cannot be: it counts
// every turn the spread produces, however wide the board is.
func TestRunBudgetBoundsAWideBoard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	runID, err := store.StartFlowRun(ctx, tx, 0)
	if err != nil {
		tx.Rollback()
		t.Fatalf("start run: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for step := range maxRunSteps + 5 {
		allowed, err := store.countRunStep(ctx, runID)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if step < maxRunSteps-1 && !allowed {
			t.Fatalf("run refused step %d though its budget is %d", step, maxRunSteps)
		}
		if step >= maxRunSteps && allowed {
			t.Fatalf("run allowed step %d beyond its budget of %d", step, maxRunSteps)
		}
	}
}

// turnsOf reports how many turns a card has.
func turnsOf(t *testing.T, store *Store, conversationID int64) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM chat_turns WHERE conversation_id = ?", conversationID).Scan(&count); err != nil {
		t.Fatalf("count turns: %v", err)
	}
	return count
}

// currentRun reports the newest run, which in these tests is the one under way.
func currentRun(t *testing.T, store *Store) int64 {
	t.Helper()
	var id int64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT COALESCE(MAX(id), 0) FROM flow_runs").Scan(&id); err != nil {
		t.Fatalf("read run: %v", err)
	}
	return id
}
