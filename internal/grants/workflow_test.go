package grants

import (
	"encoding/json"
	"strings"
	"testing"
)

func workflowProject() Project {
	facts := make(map[string]string)
	for _, key := range RequiredFacts {
		facts[key] = "Kullanıcının onayladığı bilgi"
	}
	facts["title"] = "Sensör projesi"
	return Project{ID: 1, Version: 1, Stage: "writing", Input: ProjectInput{Program: Program{ID: "call", Name: "Resmi çağrı", URL: "https://agency.example/call", VerifiedAt: "2026-10-01"}, Facts: facts, FactsConfirmed: true, ScopeApproved: true}, Requirements: []SectionRequirement{{ID: "method", Title: "Yöntem", Requirements: []string{"Gerçek yöntem"}, SourceURL: "https://agency.example/call"}}}
}
func writerResult(markdown string) string {
	data, _ := json.Marshal(map[string]any{"sections": []writtenSection{{ID: "method", Markdown: markdown}}, "questions": []string{}})
	return string(data)
}
func addReviews(project *Project, failedProvider string) {
	for _, name := range Providers {
		status := "passed"
		if name == failedProvider {
			status = "failed"
		}
		project.Contributions = append(project.Contributions, Contribution{Provider: name, Stage: "review", Round: project.Round, Status: status, Content: `{"issues":[],"summary":"Onaylanan gerçekler ve şartlar incelendi."}`})
	}
	project.Pending = nil
}
func TestMissingReviewerDoesNotFinishApplication(t *testing.T) {
	project := workflowProject()
	project.Stage = "review"
	project.Contributions = []Contribution{{Provider: "openai", Stage: "writing", Status: "passed", Content: writerResult("Gerçek yöntem açıklaması.")}, {Provider: "claude", Stage: "review", Status: "passed", Content: `{"issues":[],"summary":"İncelendi"}`}}
	if err := AdvanceCompletedStage(&project); err != nil {
		t.Fatal(err)
	}
	if project.Stage != "review" {
		t.Fatalf("missing reviewers advanced to %s", project.Stage)
	}
}
func TestReviewerFailureNeverCompletes(t *testing.T) {
	project := workflowProject()
	project.Stage = "review"
	project.Contributions = []Contribution{{Provider: "openai", Stage: "writing", Status: "passed", Content: writerResult("Gerçek yöntem açıklaması.")}}
	addReviews(&project, "gemini")
	if err := AdvanceCompletedStage(&project); err == nil {
		t.Fatal("failed reviewer accepted")
	}
	if project.Stage == "completed" {
		t.Fatal("failed review finished the dossier")
	}
}
func TestSectionLimitsSurviveEmptyAIReviews(t *testing.T) {
	for _, limits := range []SectionRequirement{{MinWords: 4}, {MaxWords: 1}, {MinChars: 40}, {MaxChars: 4}} {
		project := workflowProject()
		project.Requirements[0].MinWords = limits.MinWords
		project.Requirements[0].MaxWords = limits.MaxWords
		project.Requirements[0].MinChars = limits.MinChars
		project.Requirements[0].MaxChars = limits.MaxChars
		project.Contributions = []Contribution{{Provider: "openai", Stage: "writing", Status: "passed", Content: writerResult("Ölçüm yapılır.")}}
		if err := AdvanceCompletedStage(&project); err != nil {
			t.Fatal(err)
		}
		addReviews(&project, "")
		if err := AdvanceCompletedStage(&project); err != nil {
			t.Fatal(err)
		}
		if project.Stage != "revision" || project.RevisionCount != 1 || len(project.Pending) != 1 || project.Pending[0].Provider != "openai" {
			t.Fatalf("limits were lost: %#v", project)
		}
	}
}
func TestWriterCannotHideUnverifiedLinkOutsideCitations(t *testing.T) {
	project := workflowProject()
	if err := compileApplication(&project, writerResult("İddia [yayın](https://invented.example/paper) ile desteklenir.")); err == nil {
		t.Fatal("unverified embedded link accepted")
	}
	if project.ApplicationMarkdown != "" {
		t.Fatal("unverified application was published")
	}
	if err := compileApplication(&project, writerResult("[Resmi çağrı](https://agency.example/call) kapsamında gerçek yöntem.")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(project.ApplicationMarkdown, "## Kaynakça") {
		t.Fatal("embedded verified reference missing from bibliography")
	}
}
func TestWriterQuestionsPreventReadyDraft(t *testing.T) {
	project := workflowProject()
	if err := compileApplication(&project, `{"sections":[{"id":"method","markdown":"Taslak"}],"questions":["Deney ölçütünü belirtin."]}`); err == nil {
		t.Fatal("unanswered writer question accepted")
	}
	if project.ApplicationMarkdown != "" || len(project.Questions) != 1 {
		t.Fatal("question did not prevent application completion")
	}
}
func TestThreeCorrectionRoundsAreIndependentOfResearchRetries(t *testing.T) {
	project := workflowProject()
	project.Stage = "review"
	project.Round = 12
	project.Requirements[0].MinWords = 100
	project.Contributions = []Contribution{{Provider: "openai", Stage: "revision", Round: 12, Status: "passed", Content: writerResult("Kısa metin.")}}
	addReviews(&project, "")
	if err := AdvanceCompletedStage(&project); err != nil {
		t.Fatal(err)
	}
	if project.RevisionCount != 1 || project.Stage != "revision" {
		t.Fatal("research retries consumed correction allowance")
	}
	project.Stage = "review"
	project.RevisionCount = 3
	project.Contributions = nil
	project.Contributions = append(project.Contributions, Contribution{Provider: "openai", Stage: "revision", Round: project.Round, Status: "passed", Content: writerResult("Kısa metin.")})
	addReviews(&project, "")
	if err := AdvanceCompletedStage(&project); err == nil {
		t.Fatal("unresolved limits accepted after correction allowance")
	}
	if project.Stage == "completed" {
		t.Fatal("blocking issues produced completed dossier")
	}
}
func TestConfirmedRequirementsRejectUnknownSourcesAndInvalidLimits(t *testing.T) {
	project := workflowProject()
	requirement := project.Requirements[0]
	requirement.SourceURL = "https://invented.example/form"
	if ValidateRequirements([]SectionRequirement{requirement}, project.Input, nil) == nil {
		t.Fatal("unknown official form source accepted")
	}
	requirement.SourceURL = project.Input.Program.URL
	requirement.MinChars = 50
	requirement.MaxChars = 10
	if ValidateRequirements([]SectionRequirement{requirement}, project.Input, nil) == nil {
		t.Fatal("inverted character limits accepted")
	}
}
func TestCredentialRedactionPreservesStructuredModelOutput(t *testing.T) {
	original := `{"summary":"JSON örneği: {\"api_key\":\"private-value\"}","sources":[],"api_key":"another-private-value","count":9007199254740993}`
	cleaned := ScrubModelOutput(original)
	if strings.Contains(cleaned, "private-value") {
		t.Fatal("credential remained in model output")
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &decoded); err != nil {
		t.Fatalf("redaction broke structured output: %v", err)
	}
	if string(decoded["count"]) != "9007199254740993" {
		t.Fatal("redaction changed numeric facts")
	}
	if ScrubModelOutput(cleaned) != cleaned {
		t.Fatal("redaction is not idempotent")
	}
}
func TestMaskRestoresOnlyLocalFactsNotCredentials(t *testing.T) {
	input := ProjectInput{Facts: map[string]string{"team": "Ada Yılmaz ada@example.com; api_key=private-value"}, SensitiveTerms: []string{"Ada Yılmaz"}}
	SanitizeInput(&input)
	masked := MaskForSharing(input.Facts["team"], input.SensitiveTerms)
	if strings.Contains(masked, "Ada Yılmaz") || strings.Contains(masked, "ada@example.com") || strings.Contains(masked, "private-value") {
		t.Fatal("private material escaped masking")
	}
	restored := RestoreSharedText(masked+" [MASK_unknown]", input)
	if !strings.Contains(restored, "Ada Yılmaz ada@example.com") || strings.Contains(restored, "private-value") || !strings.Contains(restored, "[MASK_unknown]") {
		t.Fatal("restoration changed the privacy boundary")
	}
}

func TestSanitizedSecretDoesNotCorruptApprovedProjectJSON(t *testing.T) {
	project := workflowProject()
	project.Stage = "research"
	project.Input.Facts["team"] = "Approved team; api_key=private-value"
	SanitizeInput(&project.Input)
	prompt, err := buildPrompt(project, "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(prompt, "KULLANICININ ONAYLADIĞI BİLGİLER VE BELGELER (JSON):\n")
	if !ok {
		t.Fatal("project context missing")
	}
	line, _, _ := strings.Cut(rest, "\n")
	var decoded ProjectInput
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("sanitized project context became invalid JSON: %v", err)
	}
	if strings.Contains(prompt, "private-value") || !strings.Contains(decoded.Facts["team"], "Approved team") {
		t.Fatal("redaction leaked a credential or lost approved facts")
	}
}

func TestOfficialLimitsCountRestoredLocalNamesNotShortMaskTokens(t *testing.T) {
	project := workflowProject()
	project.Input.Facts["applicant_name"] = "A long approved applicant name containing many separate legal words"
	project.Input.SensitiveTerms = []string{project.Input.Facts["applicant_name"]}
	project.Requirements[0].MaxWords = 3
	project.Requirements[0].MaxChars = 30
	maskedName := MaskForSharing(project.Input.Facts["applicant_name"], project.Input.SensitiveTerms)
	project.Contributions = []Contribution{{Provider: "openai", Stage: "writing", Status: "passed", Content: writerResult(maskedName)}}
	if err := AdvanceCompletedStage(&project); err != nil {
		t.Fatal(err)
	}
	addReviews(&project, "")
	if err := AdvanceCompletedStage(&project); err != nil {
		t.Fatal(err)
	}
	if project.Stage != "revision" {
		t.Fatalf("oversized restored legal name escaped official limits: %s", project.Stage)
	}
	if strings.Contains(project.Pending[0].Prompt, project.Input.Facts["applicant_name"]) {
		t.Fatal("local limit check leaked a masked name to the writer")
	}
}

func TestMaskedUploadedDocumentRemainsAnAllowedCitation(t *testing.T) {
	project := workflowProject()
	project.Input.Documents = []ImportedDocument{{Name: "Private_Finance.xlsx", Text: "Approved budget table."}}
	project.Input.SensitiveTerms = []string{"Private_Finance"}
	data, _ := json.Marshal(map[string]any{"sections": []writtenSection{{ID: "method", Markdown: "Onaylanan belgedeki gerçek yöntem.", Citations: []string{MaskForSharing("document:Private_Finance.xlsx", project.Input.SensitiveTerms)}}}, "questions": []string{}})
	if err := compileApplication(&project, string(data)); err != nil {
		t.Fatalf("approved masked document citation rejected: %v", err)
	}
	final := RestoreSharedText(project.ApplicationMarkdown, project.Input)
	if !strings.Contains(final, "document:Private_Finance.xlsx") {
		t.Fatal("local Word bibliography lost original document identity")
	}
}
