package store

import (
	"context"
	"testing"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

func loopCard(t *testing.T, store *Store, config domain.LoopConfig) domain.Conversation {
	t.Helper()
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Rig", Kind: domain.KindSolo, Providers: []string{"claude"},
		ProjectPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetLoop(ctx, conversation.ID, config); err != nil {
		t.Fatalf("set loop: %v", err)
	}
	return conversation
}

func flashCycle() domain.LoopConfig {
	// The shape of a hardware cycle: flash, then listen on a port.
	return domain.LoopConfig{
		Mode:            domain.LoopContinuous,
		IntervalSeconds: 1,
		NotifyOnFailure: true,
		Steps: []domain.CardStep{
			{Name: "flash", Command: "STM32_Programmer_CLI -c port=SWD -w fw.hex -rst"},
			{Name: "uart", Command: "python listen.py --port COM5", TimeoutSeconds: 10},
		},
	}
}

func TestSetLoopStoresEveryStepInOrder(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())

	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	var card domain.Conversation
	for _, item := range canvas.Conversations {
		if item.ID == conversation.ID {
			card = item
		}
	}
	if len(card.Loop.Steps) != 2 {
		t.Fatalf("steps = %+v", card.Loop.Steps)
	}
	if card.Loop.Steps[0].Name != "flash" || card.Loop.Steps[1].Name != "uart" {
		t.Fatalf("step order = %+v", card.Loop.Steps)
	}
	if card.Loop.Steps[1].TimeoutSeconds != 10 {
		t.Fatalf("step timeout = %d", card.Loop.Steps[1].TimeoutSeconds)
	}
	if card.Loop.Mode != domain.LoopContinuous {
		t.Fatalf("mode = %q", card.Loop.Mode)
	}
}

// A card is only claimed once its cycle is armed.
func TestLoopIsNotClaimedUntilArmed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())

	if job, err := store.ClaimLoopRun(ctx); err != nil || job != nil {
		t.Fatalf("claimed before arming: job=%v err=%v", job, err)
	}
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	job, err := store.ClaimLoopRun(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim after arming: job=%v err=%v", job, err)
	}
	if len(job.Steps) != 2 || job.Steps[0].Name != "flash" {
		t.Fatalf("claimed steps = %+v", job.Steps)
	}
}

// Claiming leases the card so a second worker cannot take it at the same time.
func TestClaimLeasesTheCard(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if _, err := store.ClaimLoopRun(ctx); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second, err := store.ClaimLoopRun(ctx)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != nil {
		t.Fatal("the same card was claimed twice at once")
	}
}

// This is the point of continuous mode: a passing cycle must not stop it.
func TestContinuousLoopKeepsRunningAfterSuccess(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	job, err := store.ClaimLoopRun(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.FinishLoopRun(ctx, job,
		domain.CardRun{Status: domain.StatusPassed, StartedAt: nowStamp()}, "passed"); err != nil {
		t.Fatalf("finish: %v", err)
	}

	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range canvas.Conversations {
		if item.ID == conversation.ID && !item.LoopRunning {
			t.Fatal("a successful cycle disarmed a continuous loop")
		}
	}
}

// until_pass is the opposite: it exists to stop once everything works.
func TestUntilPassLoopStopsOnSuccess(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	config := flashCycle()
	config.Mode = domain.LoopUntilPass
	conversation := loopCard(t, store, config)
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	job, err := store.ClaimLoopRun(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.FinishLoopRun(ctx, job,
		domain.CardRun{Status: domain.StatusPassed, StartedAt: nowStamp()}, "passed"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, item := range canvas.Conversations {
		if item.ID == conversation.ID && item.LoopRunning {
			t.Fatal("until_pass kept running after a successful cycle")
		}
	}
}

// A cycle that keeps failing the same way must interrupt the card once, not on
// every pass, or a continuous loop would spend the whole quota on one bug.
func TestRepeatedFailureNotifiesOnlyOnce(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}

	notifications := 0
	for pass := range 3 {
		job, err := store.ClaimLoopRun(ctx)
		if err != nil {
			t.Fatalf("pass %d claim: %v", pass, err)
		}
		if job == nil {
			// The lease has not expired yet; force the card due again.
			if _, err := store.db.ExecContext(ctx,
				"UPDATE conversations SET loop_due_at = ? WHERE id = ?",
				time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), conversation.ID); err != nil {
				t.Fatalf("re-arm: %v", err)
			}
			job, err = store.ClaimLoopRun(ctx)
			if err != nil || job == nil {
				t.Fatalf("pass %d re-claim: job=%v err=%v", pass, job, err)
			}
		}
		fresh, err := store.FinishLoopRun(ctx, job,
			domain.CardRun{Status: domain.StatusFailed, StepName: "uart", ExitCode: 1,
				Output: "beklenen cikti gelmedi", StartedAt: nowStamp()}, "ayni-hata")
		if err != nil {
			t.Fatalf("pass %d finish: %v", pass, err)
		}
		if fresh {
			notifications++
		}
	}
	if notifications != 1 {
		t.Fatalf("the same failure notified %d times, want 1", notifications)
	}
}

// A different failure is news again.
func TestDifferentFailureNotifiesAgain(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	signatures := []string{"hata-a", "hata-b"}
	notifications := 0
	for _, signature := range signatures {
		if _, err := store.db.ExecContext(ctx,
			"UPDATE conversations SET loop_due_at = ? WHERE id = ?",
			time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), conversation.ID); err != nil {
			t.Fatalf("re-arm: %v", err)
		}
		job, err := store.ClaimLoopRun(ctx)
		if err != nil || job == nil {
			t.Fatalf("claim: job=%v err=%v", job, err)
		}
		fresh, err := store.FinishLoopRun(ctx, job,
			domain.CardRun{Status: domain.StatusFailed, StepName: "uart", ExitCode: 1,
				StartedAt: nowStamp()}, signature)
		if err != nil {
			t.Fatalf("finish: %v", err)
		}
		if fresh {
			notifications++
		}
	}
	if notifications != 2 {
		t.Fatalf("two different failures notified %d times, want 2", notifications)
	}
}

// Cycle history must not grow without bound.
func TestCardRunHistoryIsTrimmed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation := loopCard(t, store, flashCycle())
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	for pass := range recentRuns + 5 {
		if _, err := store.db.ExecContext(ctx,
			"UPDATE conversations SET loop_due_at = ? WHERE id = ?",
			time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), conversation.ID); err != nil {
			t.Fatalf("re-arm: %v", err)
		}
		job, err := store.ClaimLoopRun(ctx)
		if err != nil || job == nil {
			t.Fatalf("pass %d: job=%v err=%v", pass, job, err)
		}
		if _, err := store.FinishLoopRun(ctx, job,
			domain.CardRun{Status: domain.StatusPassed, StartedAt: nowStamp()}, "passed"); err != nil {
			t.Fatalf("finish: %v", err)
		}
	}
	runs, err := store.recentCardRuns(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != recentRuns {
		t.Fatalf("history holds %d runs, want %d", len(runs), recentRuns)
	}
}

// A card with no project has nowhere to run its steps.
func TestLoopWithoutProjectIsNotClaimed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, domain.NewConversation{
		Title: "Projesiz", Kind: domain.KindSolo, Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetLoop(ctx, conversation.ID, flashCycle()); err != nil {
		t.Fatalf("set loop: %v", err)
	}
	if err := store.SetLoopRunning(ctx, conversation.ID, true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if job, err := store.ClaimLoopRun(ctx); err != nil || job != nil {
		t.Fatalf("claimed without a project: job=%v err=%v", job, err)
	}
}

func TestLoopConfigIsClamped(t *testing.T) {
	steps := make([]domain.CardStep, 50)
	for index := range steps {
		steps[index] = domain.CardStep{Command: "echo hi", TimeoutSeconds: 99999}
	}
	config := domain.LoopConfig{Mode: "uydurma", IntervalSeconds: 99999, Steps: steps}.Normalised()
	if config.Mode != domain.LoopOff {
		t.Fatalf("unknown mode became %q", config.Mode)
	}
	if config.IntervalSeconds > 3600 {
		t.Fatalf("interval = %d", config.IntervalSeconds)
	}
	if len(config.Steps) > 20 {
		t.Fatalf("steps = %d", len(config.Steps))
	}
	for _, step := range config.Steps {
		if step.TimeoutSeconds > 3600 {
			t.Fatalf("step timeout = %d", step.TimeoutSeconds)
		}
	}
	// An empty step list cannot be a running loop.
	empty := domain.LoopConfig{Mode: domain.LoopContinuous}.Normalised()
	if empty.Mode != domain.LoopOff {
		t.Fatalf("a loop with no steps stayed %q", empty.Mode)
	}
}

func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
