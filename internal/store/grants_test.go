package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/grants"
)

func grantInput() grants.ProjectInput {
	facts := make(map[string]string)
	for _, key := range grants.RequiredFacts {
		facts[key] = "Onaylanmış proje bilgisi"
	}
	facts["title"] = "Onaylı sensör projesi"
	facts["team"] = "Ada Yılmaz ada@example.com api_key=private-value"
	return grants.ProjectInput{Program: grants.Program{ID: "call", Name: "Resmi çağrı", URL: "https://agency.example/call", VerifiedAt: "2026-10-01", SourceText: `{"api_key":"source-private-value"}`}, Facts: facts, FactsConfirmed: true, ScopeApproved: true, SensitiveTerms: []string{"Ada Yılmaz"}}
}
func TestGrantApprovalQueuesOnlyExactApprovedPromptAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	project, err := database.CreateGrantProject(ctx, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	if job, err := database.ClaimChatResponse(ctx); err != nil || job != nil {
		t.Fatalf("creation sent unapproved work: %v %#v", err, job)
	}
	pending := project.Pending[0]
	approved := "\n " + pending.Prompt + "\n"
	project, err = database.ApproveGrantSend(ctx, project.ID, grants.SendApproval{Key: pending.Key, Prompt: approved})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApproveGrantSend(ctx, project.ID, grants.SendApproval{Key: pending.Key, Prompt: approved}); !errors.Is(err, ErrGrantConflict) {
		t.Fatalf("duplicate approval accepted: %v", err)
	}
	job, err := database.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if job.Prompt != approved || job.Provider != pending.Provider || job.Access != "read" || job.ProjectPath != "" || !job.WebResearch {
		t.Fatalf("approved dispatch changed scope: %#v", job)
	}
	if strings.Contains(job.Prompt, "private-value") || strings.Contains(job.Prompt, "ada@example.com") || strings.Contains(job.Prompt, "Ada Yılmaz") {
		t.Fatal("private material entered provider prompt")
	}
	if err := database.FinishChatResponse(ctx, job.ResponseID, domain.StatusPassed, `{"summary":"Araştırma tamamlandı","sources":[],"questions":[],"requirements":[]}`, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RefreshGrantResponses(ctx, &project); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateGrantProject(ctx, &project, project.Version); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	restored, err := database.LoadGrantProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Stage != "research" || len(restored.Pending) != 2 || restored.Contributions[0].Status != "passed" {
		t.Fatalf("restart lost workflow state: %#v", restored)
	}
	if !strings.Contains(restored.Input.Facts["team"], "Ada Yılmaz ada@example.com") || strings.Contains(restored.Input.Facts["team"], "private-value") || strings.Contains(restored.Input.Program.SourceText, "source-private-value") {
		t.Fatal("local fact or credential boundary changed")
	}
	if job, err := database.ClaimChatResponse(ctx); err != nil || job != nil {
		t.Fatal("restart dispatched an unapproved recipient")
	}
}
func TestGrantConversationsRejectOrdinarySendAndAccessWidening(t *testing.T) {
	database := openTemp(t)
	ctx := context.Background()
	project, err := database.CreateGrantProject(ctx, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	id := project.Conversations["claude"]
	if _, err := database.CreateConversationTurn(ctx, id, "unapproved message"); err == nil {
		t.Fatal("ordinary turn bypassed grant approval")
	}
	if err := database.SetConversationRole(ctx, id, "another role"); err == nil {
		t.Fatal("grant role changed")
	}
	if err := database.SetConversationProject(ctx, id, t.TempDir(), "edit"); err == nil {
		t.Fatal("grant access widened")
	}
	if err := database.SetLoop(ctx, id, domain.LoopConfig{}); err == nil {
		t.Fatal("grant loop configured")
	}
	if err := database.SetLoopRunning(ctx, id, true); err == nil {
		t.Fatal("grant loop started")
	}
	canvas, err := database.Canvas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range canvas.Conversations {
		if conversation.ID == id {
			t.Fatal("grant conversation leaked into ordinary canvas")
		}
	}
	if job, err := database.ClaimChatResponse(ctx); err != nil || job != nil {
		t.Fatal("rejected action queued provider work")
	}
}
func TestGrantProjectOptimisticVersionRejectsStaleUpdate(t *testing.T) {
	database := openTemp(t)
	ctx := context.Background()
	current, err := database.CreateGrantProject(ctx, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := database.LoadGrantProject(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Stage = "cancelled"
	current.Pending = nil
	if err := database.UpdateGrantProject(ctx, &current, current.Version); err != nil {
		t.Fatal(err)
	}
	stale.Stage = "completed"
	if err := database.UpdateGrantProject(ctx, &stale, stale.Version); !errors.Is(err, ErrGrantConflict) {
		t.Fatalf("stale update accepted: %v", err)
	}
	loaded, err := database.LoadGrantProject(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Stage != "cancelled" || len(loaded.Pending) != 0 {
		t.Fatal("stale update resurrected cancelled project")
	}
}
func TestGrantProjectsDoNotShareProviderConversationContext(t *testing.T) {
	database := openTemp(t)
	ctx := context.Background()
	first, err := database.CreateGrantProject(ctx, grantInput())
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput()
	input.Facts["title"] = "İkinci bağımsız proje"
	second, err := database.CreateGrantProject(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range grants.Providers {
		if first.Conversations[name] == second.Conversations[name] {
			t.Fatal("projects share provider conversation")
		}
	}
	pending := second.Pending[0]
	if _, err := database.ApproveGrantSend(ctx, second.ID, grants.SendApproval{Key: pending.Key, Prompt: pending.Prompt}); err != nil {
		t.Fatal(err)
	}
	job, err := database.ClaimChatResponse(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if strings.Contains(job.Prompt, first.Input.Facts["title"]) || job.ConversationID != second.Conversations[pending.Provider] {
		t.Fatal("other project context leaked")
	}
}
