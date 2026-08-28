package store

import (
	"context"
	"testing"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// A trigger arrives switched off. Something that fires on its own should not
// begin doing so before anyone has said what it sends.
func TestATriggerStartsDisarmed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Sabah kontrolü"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if trigger.Enabled {
		t.Fatal("a freshly created trigger is armed")
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Triggers) != 1 || canvas.Triggers[0].NodeID == 0 {
		t.Fatalf("board shows %+v, want one trigger with a card", canvas.Triggers)
	}
}

// Arming a trigger with no message would start a run carrying nothing, which
// every card would answer with a question.
func TestAnEmptyTriggerCannotBeArmed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Boş", Prompt: "   ", Mode: domain.TriggerInterval,
		IntervalSeconds: 600, Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved := triggerOf(t, store, trigger.ID)
	if saved.Enabled {
		t.Fatal("a trigger with no message was armed")
	}
	if saved.DueAt != "" {
		t.Fatalf("a disarmed trigger has a due time: %q", saved.DueAt)
	}
}

// An armed schedule works out its own next slot, and an interval below the
// floor is raised rather than accepted: a mistyped interval must not flood the
// board.
func TestArmingASchedulePicksTheNextSlot(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Her saat", Prompt: "Durum raporu ver", Mode: domain.TriggerInterval,
		IntervalSeconds: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved := triggerOf(t, store, trigger.ID)
	if !saved.Enabled {
		t.Fatal("trigger with a message was not armed")
	}
	if saved.IntervalSeconds != minInterval {
		t.Fatalf("interval saved as %d, want it raised to %d", saved.IntervalSeconds, minInterval)
	}
	due, err := time.Parse(time.RFC3339Nano, saved.DueAt)
	if err != nil {
		t.Fatalf("due time %q: %v", saved.DueAt, err)
	}
	if !due.After(time.Now().UTC()) {
		t.Fatalf("next slot %s is not in the future", saved.DueAt)
	}
}

// A daily trigger fires at a wall-clock time, and saving it must not make it
// due immediately just because today's slot has already passed.
func TestDailyTriggerSchedulesTheNextDay(t *testing.T) {
	past := time.Now().Local().Add(-2 * time.Hour).Format("15:04")
	next, err := nextDue(time.Now(), domain.TriggerDaily, 0, past)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if !next.After(time.Now()) {
		t.Fatalf("next slot %s is not in the future", next)
	}
	if next.Sub(time.Now()) > 25*time.Hour {
		t.Fatalf("next slot %s is more than a day away", next)
	}
	if _, err := nextDue(time.Now(), domain.TriggerDaily, 0, "yarın sabah"); err == nil {
		t.Fatal("an unreadable time of day was accepted")
	}
}

// Firing hands the trigger's message to every card linked to it, as one run.
func TestFiringATriggerStartsOneRun(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first := card(t, store, "A")
	second := card(t, store, "B")
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Sabah"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Sabah", Prompt: "Dünkü hataları özetle", Mode: domain.TriggerManual,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	node := canvas.Triggers[0].NodeID
	for _, target := range []int64{first.ID, second.ID} {
		if _, err := store.CreateLink(ctx, node, nodeOf(t, canvas, target),
			domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
			t.Fatalf("link: %v", err)
		}
	}

	delivered, err := store.FireTrigger(ctx, trigger.ID)
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if delivered != 2 {
		t.Fatalf("trigger reached %d cards, want 2", delivered)
	}
	// Both cards got the message itself, not a relayed answer wrapped in
	// somebody else's name.
	for _, conversation := range []int64{first.ID, second.ID} {
		var prompt, kind string
		if err := store.db.QueryRowContext(ctx,
			"SELECT prompt, kind FROM chat_turns WHERE conversation_id = ? ORDER BY id LIMIT 1",
			conversation).Scan(&prompt, &kind); err != nil {
			t.Fatalf("read turn: %v", err)
		}
		if prompt != "Dünkü hataları özetle" {
			t.Fatalf("card received %q", prompt)
		}
		if kind != domain.TurnTrigger {
			t.Fatalf("turn kind = %q, want %q", kind, domain.TurnTrigger)
		}
	}

	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Runs) != 1 {
		t.Fatalf("%d runs active after one firing, want 1", len(after.Runs))
	}
}

// Nothing links into a trigger: it starts flows and does not receive them.
func TestNothingLinksIntoATrigger(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source := card(t, store, "A")
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, source.ID), canvas.Triggers[0].NodeID,
		domain.LinkOptions{Mode: domain.LinkRelay}); err == nil {
		t.Fatal("a link into a trigger was accepted")
	}
	_ = trigger
}

// A routine that overruns its own interval must not start again on top of
// itself: the slot is skipped, not queued.
func TestADueTriggerIsSkippedWhileItsLastRunIsGoing(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	target := card(t, store, "A")
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Sık"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Sık", Prompt: "Kontrol et", Mode: domain.TriggerInterval,
		IntervalSeconds: minInterval, Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if _, err := store.CreateLink(ctx, canvas.Triggers[0].NodeID, nodeOf(t, canvas, target.ID),
		domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.FireTrigger(ctx, trigger.ID); err != nil {
		t.Fatalf("fire: %v", err)
	}

	// Make it due again while the first run is still unanswered.
	if _, err := store.db.ExecContext(ctx,
		"UPDATE triggers SET due_at = ? WHERE id = ?",
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), trigger.ID); err != nil {
		t.Fatalf("make due: %v", err)
	}
	claimed, err := store.ClaimTriggerFire(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != nil {
		t.Fatal("a trigger fired again while its previous run was still going")
	}
}

// Deleting the card takes the trigger with it. A trigger with no card would
// keep firing where nobody could see or stop it.
func TestDeletingATriggerCardRemovesTheTrigger(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	if _, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Geçici"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if err := store.DeleteCanvasNode(ctx, canvas.Triggers[0].NodeID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(after.Triggers) != 0 {
		t.Fatalf("%d triggers left after the card was deleted", len(after.Triggers))
	}
}

func triggerOf(t *testing.T, store *Store, id int64) domain.Trigger {
	t.Helper()
	triggers, err := store.listTriggers(context.Background())
	if err != nil {
		t.Fatalf("list triggers: %v", err)
	}
	for _, trigger := range triggers {
		if trigger.ID == id {
			return trigger
		}
	}
	t.Fatalf("trigger %d not found", id)
	return domain.Trigger{}
}

// A board carries its routines with it. An exported trigger comes back with its
// message and schedule, but disarmed: a board someone is looking through should
// not start doing work the moment it lands.
func TestExportedTriggerReturnsDisarmed(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Gece"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Gece", Prompt: "Testleri koştur", Mode: domain.TriggerDaily,
		AtTime: "03:30", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	export, err := store.ExportBoard(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	fresh := openTemp(t)
	if _, err := fresh.ImportBoard(ctx, export, 0, 0); err != nil {
		t.Fatalf("import: %v", err)
	}
	canvas, err := fresh.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Triggers) != 1 {
		t.Fatalf("%d triggers imported, want 1", len(canvas.Triggers))
	}
	imported := canvas.Triggers[0]
	if imported.Prompt != "Testleri koştur" || imported.AtTime != "03:30" ||
		imported.Mode != domain.TriggerDaily {
		t.Fatalf("imported trigger = %+v", imported)
	}
	if imported.Enabled {
		t.Fatal("an imported trigger arrived armed")
	}
}

// A join is part of the board's shape, so it survives an export too.
func TestExportedJoinSurvivesImport(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	if _, err := store.CreateJoin(ctx, domain.NewNote{Body: "Topla"}); err != nil {
		t.Fatalf("join: %v", err)
	}
	export, err := store.ExportBoard(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	fresh := openTemp(t)
	if _, err := fresh.ImportBoard(ctx, export, 0, 0); err != nil {
		t.Fatalf("import: %v", err)
	}
	canvas, err := fresh.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Joins) != 1 || canvas.Joins[0].Title != "Topla" {
		t.Fatalf("imported joins = %+v", canvas.Joins)
	}
}
