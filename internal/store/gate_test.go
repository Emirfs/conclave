package store

import (
	"context"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// gateBoard wires source → gate, and the gate's two ports to two cards. It
// answers the source's turn and reports how many turns each side received.
func gateBoard(t *testing.T, store *Store, config domain.GateConfig, answer string) (int, int) {
	t.Helper()
	ctx := context.Background()
	source := card(t, store, "Kaynak")
	passSide := card(t, store, "Geçen")
	elseSide := card(t, store, "Kalan")
	gate, err := store.CreateGate(ctx, domain.NewGate{Title: config.Title})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := store.SetGate(ctx, gate.ID, config); err != nil {
		t.Fatalf("save gate: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	gateNode := canvas.Gates[0].NodeID
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, source.ID), gateNode,
		domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
		t.Fatalf("link into gate: %v", err)
	}
	if _, err := store.CreateLink(ctx, gateNode, nodeOf(t, canvas, passSide.ID),
		domain.LinkOptions{Mode: domain.LinkRelay, SourceHandle: domain.GatePass}); err != nil {
		t.Fatalf("link pass: %v", err)
	}
	if _, err := store.CreateLink(ctx, gateNode, nodeOf(t, canvas, elseSide.ID),
		domain.LinkOptions{Mode: domain.LinkRelay, SourceHandle: domain.GateElse}); err != nil {
		t.Fatalf("link else: %v", err)
	}

	if _, err := store.CreateConversationTurn(ctx, source.ID, "başlat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, answer)
	return turnsOf(t, store, passSide.ID), turnsOf(t, store, elseSide.ID)
}

// A gate sends what reached it down one of two ways. The passing case goes to
// the pass port and everything else to the other, which is what makes a board
// branch on what happened rather than on how it was drawn.
func TestGateSendsAPassingMessageOneWay(t *testing.T) {
	store := openTemp(t)
	passed, other := gateBoard(t, store, domain.GateConfig{
		Title: "Hata var mı", Mode: domain.GateContains, Pattern: "HATA",
	}, "Derleme sırasında HATA çıktı")
	if passed != 1 || other != 0 {
		t.Fatalf("pass side got %d turns and else side %d; want 1 and 0", passed, other)
	}
}

func TestGateSendsAFailingMessageTheOtherWay(t *testing.T) {
	store := openTemp(t)
	passed, other := gateBoard(t, store, domain.GateConfig{
		Title: "Hata var mı", Mode: domain.GateContains, Pattern: "HATA",
	}, "Her şey yolunda")
	if passed != 0 || other != 1 {
		t.Fatalf("pass side got %d turns and else side %d; want 0 and 1", passed, other)
	}
}

// A card writing "TAMAM" means the same thing as one writing "tamam", so a gate
// ignores case unless it is told not to.
func TestGateIgnoresCaseByDefault(t *testing.T) {
	store := openTemp(t)
	passed, _ := gateBoard(t, store, domain.GateConfig{
		Title: "Bitti mi", Mode: domain.GateContains, Pattern: "tamam",
	}, "TAMAM, iş bitti")
	if passed != 1 {
		t.Fatalf("pass side got %d turns, want 1", passed)
	}

	strict := openTemp(t)
	passedStrict, otherStrict := gateBoard(t, strict, domain.GateConfig{
		Title: "Bitti mi", Mode: domain.GateContains, Pattern: "tamam", CaseSensitive: true,
	}, "TAMAM, iş bitti")
	if passedStrict != 0 || otherStrict != 1 {
		t.Fatalf("case-sensitive gate passed %d and rejected %d; want 0 and 1",
			passedStrict, otherStrict)
	}
}

func TestGateMatchesAnExpression(t *testing.T) {
	store := openTemp(t)
	passed, _ := gateBoard(t, store, domain.GateConfig{
		Title: "Çıkış kodu", Mode: domain.GateMatches, Pattern: `exit [1-9][0-9]*`,
	}, "komut bitti: exit 2")
	if passed != 1 {
		t.Fatalf("pass side got %d turns, want 1", passed)
	}
}

// A pattern that will not compile is refused when it is written, not when a run
// reaches the gate and stops for no visible reason.
func TestABrokenExpressionIsRefusedOnSave(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	gate, err := store.CreateGate(ctx, domain.NewGate{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetGate(ctx, gate.ID, domain.GateConfig{
		Mode: domain.GateMatches, Pattern: "([a-z",
	}); err == nil {
		t.Fatal("an expression that cannot be compiled was saved")
	}
	if err := store.SetGate(ctx, gate.ID, domain.GateConfig{
		Mode: domain.GateContains, Pattern: "  ",
	}); err == nil {
		t.Fatal("a condition with nothing to look for was saved")
	}
	if err := store.SetGate(ctx, gate.ID, domain.GateConfig{Mode: "sometimes"}); err == nil {
		t.Fatal("an unknown condition was saved")
	}
}

// The gate remembers what it decided, so a board can be read after the fact
// instead of only watched as it happens.
func TestGateRemembersItsLastDecision(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	gateBoard(t, store, domain.GateConfig{
		Title: "Hata var mı", Mode: domain.GateContains, Pattern: "HATA",
	}, "Her şey yolunda")
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if len(canvas.Gates) != 1 {
		t.Fatalf("%d gates on the board, want 1", len(canvas.Gates))
	}
	if canvas.Gates[0].LastResult != domain.GateElse {
		t.Fatalf("gate remembered %q, want %q", canvas.Gates[0].LastResult, domain.GateElse)
	}
}

// A gate with only one port wired stops the branch that has nowhere to go,
// rather than sending it out of the port that was wired.
func TestGateWithOneWayWiredStopsTheOther(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source := card(t, store, "Kaynak")
	target := card(t, store, "Geçen")
	gate, err := store.CreateGate(ctx, domain.NewGate{Title: "Yalnız geçen"})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := store.SetGate(ctx, gate.ID, domain.GateConfig{
		Mode: domain.GateContains, Pattern: "HATA",
	}); err != nil {
		t.Fatalf("save gate: %v", err)
	}
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	gateNode := canvas.Gates[0].NodeID
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, source.ID), gateNode,
		domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
		t.Fatalf("link in: %v", err)
	}
	if _, err := store.CreateLink(ctx, gateNode, nodeOf(t, canvas, target.ID),
		domain.LinkOptions{Mode: domain.LinkRelay, SourceHandle: domain.GatePass}); err != nil {
		t.Fatalf("link pass: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, source.ID, "başlat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "Her şey yolunda")
	if got := turnsOf(t, store, target.ID); got != 0 {
		t.Fatalf("the pass side got %d turns from a message that did not pass", got)
	}
}

// A board carries its decision points with it, and the port each link left by
// comes back with them: without that, both ways out of a gate would be one way.
func TestExportedGateKeepsItsPorts(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	gateBoard(t, store, domain.GateConfig{
		Title: "Hata var mı", Mode: domain.GateContains, Pattern: "HATA",
	}, "Her şey yolunda")
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
	if len(canvas.Gates) != 1 {
		t.Fatalf("%d gates imported, want 1", len(canvas.Gates))
	}
	imported := canvas.Gates[0]
	if imported.Mode != domain.GateContains || imported.Pattern != "HATA" {
		t.Fatalf("imported gate = %+v", imported)
	}
	handles := map[string]int{}
	for _, link := range canvas.Links {
		if link.SourceID == imported.NodeID {
			handles[link.SourceHandle]++
		}
	}
	if handles[domain.GatePass] != 1 || handles[domain.GateElse] != 1 {
		t.Fatalf("imported ports = %v, want one of each", handles)
	}
}
