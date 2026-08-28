package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
)

// A run says what started it. A number alone tells nobody which of last
// night's runs they are reading.
func TestARunIsNamedAfterWhatStartedIt(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source := card(t, store, "Gözcü")
	if _, err := store.CreateConversationTurn(ctx, source.ID, "başlat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "tamam")

	runs, err := store.FlowRuns(ctx, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("%d runs in history, want 1", len(runs))
	}
	if runs[0].OriginLabel != "Gözcü" || runs[0].OriginKind != domain.OriginUser {
		t.Fatalf("run reads %+v", runs[0])
	}
	if runs[0].Cards != 1 || runs[0].FinishedAt == "" {
		t.Fatalf("finished run reads %+v", runs[0])
	}
}

// A run outlives the card it started from: the history has to survive the
// board being tidied up.
func TestRunHistorySurvivesTheCardBeingDeleted(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source := card(t, store, "Geçici")
	if _, err := store.CreateConversationTurn(ctx, source.ID, "başlat"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "tamam")
	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	if err := store.DeleteCanvasNode(ctx, nodeOf(t, canvas, source.ID)); err != nil {
		t.Fatalf("delete: %v", err)
	}

	runs, err := store.FlowRuns(ctx, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 1 || runs[0].OriginLabel != "Geçici" {
		t.Fatalf("history after deletion = %+v", runs)
	}
}

// The detail of a run is what a person reads when they were not there: which
// card, what it was asked, what it said.
func TestRunDetailReadsInOrder(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	first, second, canvas := linkedPair(t, store)
	if _, err := store.CreateLink(ctx, nodeOf(t, canvas, first.ID), nodeOf(t, canvas, second.ID),
		domain.LinkOptions{Mode: domain.LinkRelay}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := store.CreateConversationTurn(ctx, first.ID, "ilk soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "cevabım")

	runs, err := store.FlowRuns(ctx, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	detail, err := store.FlowRunDetail(ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.Steps) != 2 {
		t.Fatalf("%d steps in the run, want 2", len(detail.Steps))
	}
	if detail.Steps[0].Card != "A" || detail.Steps[1].Card != "B" {
		t.Fatalf("steps ran %q then %q", detail.Steps[0].Card, detail.Steps[1].Card)
	}
	if detail.Steps[0].Prompt != "ilk soru" {
		t.Fatalf("first step was asked %q", detail.Steps[0].Prompt)
	}
	if !strings.Contains(detail.Steps[0].Answer, "cevabım") {
		t.Fatalf("first step answered %q", detail.Steps[0].Answer)
	}
	if detail.Steps[0].Status != string(domain.StatusPassed) {
		t.Fatalf("first step ended %q", detail.Steps[0].Status)
	}
}

// A routine runs while nobody is watching, so it leaves its result on the
// board by itself.
func TestATriggerRunReportsItself(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	target := card(t, store, "Gece nöbeti")
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Gece"})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Gece", Prompt: "Testleri koştur", Mode: domain.TriggerManual,
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
	answerAll(t, store, "Hepsi geçti")

	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	var report string
	for _, node := range after.Nodes {
		if node.Kind == domain.NodeNote && strings.Contains(node.Body, "Gece") {
			report = node.Body
		}
	}
	if report == "" {
		t.Fatal("a trigger run left no report on the board")
	}
	if !strings.Contains(report, "Gece nöbeti") || !strings.Contains(report, "Hepsi geçti") {
		t.Fatalf("report does not say what happened:\n%s", report)
	}
	if !strings.Contains(report, "Tetikleyici çalıştı") {
		t.Fatalf("report does not say it ran on its own:\n%s", report)
	}
}

// A person who sent a message was there to read the answer, so their run does
// not litter the board with a report of its own.
func TestAUserRunDoesNotReportItself(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	source := card(t, store, "Elle")
	if _, err := store.CreateConversationTurn(ctx, source.ID, "soru"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	answerAll(t, store, "cevap")

	canvas, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	for _, node := range canvas.Nodes {
		if node.Kind == domain.NodeNote {
			t.Fatalf("a run someone was watching left a report anyway:\n%s", node.Body)
		}
	}

	// Asking for one is a different matter, and always allowed.
	runs, err := store.FlowRuns(ctx, 0)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	note, err := store.ReportRunToBoard(ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(note.Body, "Elle") || !strings.Contains(note.Body, "cevap") {
		t.Fatalf("requested report does not say what happened:\n%s", note.Body)
	}
}

// A report is a summary, not a second copy of every transcript.
func TestALongAnswerIsClippedInTheReport(t *testing.T) {
	long := strings.Repeat("uzun cevap. ", 400)
	report := formatRunReport(domain.FlowRunDetail{
		Run: domain.FlowRun{ID: 1, OriginLabel: "Uzun", Status: domain.RunDone, Cards: 1},
		Steps: []domain.FlowStep{{
			Card: "A", Answer: long, Status: string(domain.StatusPassed),
		}},
	})
	if len([]rune(report)) > reportAnswerLimit+400 {
		t.Fatalf("report is %d runes; a summary should not carry the whole answer",
			len([]rune(report)))
	}
	if !strings.Contains(report, "kısaltıldı") {
		t.Fatal("a clipped report does not say it was clipped")
	}
}

// A failed card is the one thing a person wants to find in a report, so it is
// named rather than left to look like every other line.
func TestAFailedCardIsNamedInTheReport(t *testing.T) {
	report := formatRunReport(domain.FlowRunDetail{
		Run: domain.FlowRun{ID: 2, OriginLabel: "Gece", OriginKind: domain.OriginTrigger,
			Status: domain.RunDone, Cards: 2},
		Steps: []domain.FlowStep{
			{Card: "A", Answer: "tamam", Status: string(domain.StatusPassed)},
			{Card: "B", Answer: "", Status: string(domain.StatusFailed)},
		},
	})
	if !strings.Contains(report, "B · başarısız") {
		t.Fatalf("report does not name the card that failed:\n%s", report)
	}
	if !strings.Contains(report, "1 başarısız") {
		t.Fatalf("report does not count the failure:\n%s", report)
	}
	if strings.Contains(report, "A · ") {
		t.Fatalf("report labels an ordinary card, burying the failed one:\n%s", report)
	}
}

// A routine cut short still gets its write-up: "it was stopped half way" is
// exactly the sort of thing a person needs to be told.
func TestAStoppedTriggerRunStillReports(t *testing.T) {
	store := openTemp(t)
	ctx := context.Background()
	target := card(t, store, "Uzun iş")
	trigger, err := store.CreateTrigger(ctx, domain.NewTrigger{Title: "Gece"})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if err := store.SetTrigger(ctx, trigger.ID, domain.TriggerConfig{
		Title: "Gece", Prompt: "Uzun işi başlat", Mode: domain.TriggerManual,
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

	if _, err := store.StopFlowRun(ctx, currentRun(t, store)); err != nil {
		t.Fatalf("stop: %v", err)
	}
	after, err := store.Canvas(ctx)
	if err != nil {
		t.Fatalf("canvas: %v", err)
	}
	found := false
	for _, node := range after.Nodes {
		if node.Kind == domain.NodeNote && strings.Contains(node.Body, "Gece") {
			found = true
		}
	}
	if !found {
		t.Fatal("a stopped trigger run left no report")
	}
}
