package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Emirfs/conclave/internal/grants"
	"github.com/Emirfs/conclave/internal/store"
)

func (s *Server) registerGrantProjects(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/grants/projects", s.listGrantProjects)
	mux.HandleFunc("POST /v1/grants/projects", s.createGrantProject)
	mux.HandleFunc("GET /v1/grants/projects/{id}", s.grantProject)
	mux.HandleFunc("POST /v1/grants/projects/{id}/send", s.approveGrantSend)
	mux.HandleFunc("POST /v1/grants/projects/{id}/research", s.approveGrantResearch)
	mux.HandleFunc("POST /v1/grants/projects/{id}/stop", s.stopGrantProject)
	mux.HandleFunc("POST /v1/grants/projects/{id}/retry", s.retryGrantProject)
}

func grantID(response http.ResponseWriter, request *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("başvuru kimliği pozitif sayı olmalı"))
		return 0, false
	}
	return id, true
}

func grantError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(response, http.StatusNotFound, errors.New("başvuru bulunamadı"))
	case errors.Is(err, store.ErrGrantConflict):
		writeError(response, http.StatusConflict, err)
	default:
		writeError(response, http.StatusBadRequest, errors.New(grants.ScrubCredentials(err.Error())))
	}
}

func (s *Server) listGrantProjects(response http.ResponseWriter, request *http.Request) {
	projects, err := s.store.ListGrantProjects(request.Context())
	if err != nil {
		grantError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, projects)
}

func (s *Server) createGrantProject(response http.ResponseWriter, request *http.Request) {
	var input grants.ProjectInput
	if !decodeJSON(response, request, 2<<20, &input) {
		return
	}
	grants.SanitizeInput(&input)
	if err := grants.ValidateInput(input); err != nil {
		grantError(response, err)
		return
	}
	if _, err := selectProviders(grants.Providers); err != nil {
		grantError(response, fmt.Errorf("Claude, Codex ve Gemini birlikte gerekli: %w", err))
		return
	}
	catalogue, err := s.store.LoadGrantCatalogue(request.Context())
	if errors.Is(err, sql.ErrNoRows) {
		catalogue = grants.SeedCatalogue()
	} else if err != nil {
		grantError(response, err)
		return
	}
	found := false
	for _, program := range catalogue.Programs {
		if program.ID == input.Program.ID {
			input.Program = program
			found = true
			break
		}
	}
	if !found {
		grantError(response, errors.New("program önce resmi kaynak kataloğuna eklenmeli"))
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	text, err := grants.FetchPublicSource(ctx, input.Program.URL)
	if err != nil {
		grantError(response, fmt.Errorf("resmi program kaynağı doğrulanamadı: %w", err))
		return
	}
	visible, err := grants.BoundedPublicSourceText(text, 90000)
	if err != nil {
		grantError(response, err)
		return
	}
	input.Program.SourceText = grants.ScrubCredentials(visible)
	input.Program.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
	input.Program.SourceError = ""
	project, err := s.store.CreateGrantProject(request.Context(), input)
	if err != nil {
		grantError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, project)
}

func (s *Server) grantProject(response http.ResponseWriter, request *http.Request) {
	id, ok := grantID(response, request)
	if !ok {
		return
	}
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	project, err := s.store.LoadGrantProject(request.Context(), id)
	if err != nil {
		grantError(response, err)
		return
	}
	changed, err := s.store.RefreshGrantResponses(request.Context(), &project)
	if err != nil {
		grantError(response, err)
		return
	}
	if len(grants.StageProviders(project.Stage)) > 0 {
		current := grants.CurrentContributions(project)
		settled := len(current) == len(grants.StageProviders(project.Stage)) && len(project.Pending) == 0
		allPassed := settled
		for _, item := range current {
			settled = settled && (item.Status == "passed" || item.Status == "failed" || item.Status == "canceled")
			allPassed = allPassed && item.Status == "passed" && strings.TrimSpace(item.Content) != ""
		}
		if settled {
			previousStage := project.Stage
			if previousStage == "research" && allPassed {
				err = grants.ReadResearch(&project)
				if err == nil {
					if len(project.Evidence) > 36 {
						err = errors.New("araştırma 36 kaynak sınırını aştı; daha odaklı araştırma gerekli")
					} else {
						ctx, cancel := context.WithTimeout(request.Context(), 45*time.Second)
						project.Evidence = grants.VerifyEvidence(ctx, project.Evidence)
						cancel()
						project.Stage = "discussion"
						err = grants.PreparePending(&project)
					}
				}
			} else {
				err = grants.AdvanceCompletedStage(&project)
			}
			if err != nil {
				project.ResumeStage = previousStage
				project.Stage = "blocked"
				project.Pending = nil
				project.Error = grants.ScrubCredentials(err.Error())
				if len(project.Questions) > 0 && (previousStage == "writing" || previousStage == "revision") {
					project.Stage = "research_approval"
				}
			}
			changed = true
		}
	}
	if changed {
		project.ResearchMarkdown = grants.BuildResearchRecord(project)
		if err := s.store.UpdateGrantProject(request.Context(), &project, project.Version); err != nil {
			if !errors.Is(err, store.ErrGrantConflict) {
				grantError(response, err)
				return
			}
			project, err = s.store.LoadGrantProject(request.Context(), id)
			if err != nil {
				grantError(response, err)
				return
			}
		}
	}
	writeJSON(response, http.StatusOK, project)
}

func (s *Server) approveGrantSend(response http.ResponseWriter, request *http.Request) {
	id, ok := grantID(response, request)
	if !ok {
		return
	}
	var input grants.SendApproval
	if !decodeJSON(response, request, 1<<20, &input) {
		return
	}
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	if _, err := selectProviders(grants.Providers); err != nil {
		grantError(response, err)
		return
	}
	project, err := s.store.ApproveGrantSend(request.Context(), id, input)
	if err != nil {
		grantError(response, err)
		return
	}
	writeJSON(response, http.StatusAccepted, project)
}

func (s *Server) approveGrantResearch(response http.ResponseWriter, request *http.Request) {
	id, ok := grantID(response, request)
	if !ok {
		return
	}
	var approval grants.ResearchApproval
	if !decodeJSON(response, request, 1<<20, &approval) {
		return
	}
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	project, err := s.store.LoadGrantProject(request.Context(), id)
	if err != nil {
		grantError(response, err)
		return
	}
	if project.Stage != "research_approval" || approval.Version != project.Version {
		grantError(response, store.ErrGrantConflict)
		return
	}
	if !approval.Confirmed || !approval.ScopeApproved {
		grantError(response, errors.New("bilgiler, araştırma ve yeni paylaşım kapsamı onaylanmalı"))
		return
	}
	if approval.Facts == nil {
		grantError(response, errors.New("onaylanan proje bilgileri gerekli"))
		return
	}
	for index, question := range project.Questions {
		answer := strings.TrimSpace(approval.Answers[strconv.Itoa(index)])
		if answer == "" {
			grantError(response, fmt.Errorf("sorunun yanıtı gerekli: %s", question))
			return
		}
		if len(answer) > 12000 {
			grantError(response, errors.New("bir soru yanıtı 12000 baytı aşamaz"))
			return
		}
		approval.Facts[fmt.Sprintf("research_answer_%d", index)] = fmt.Sprintf("Soru: %s\nKullanıcı yanıtı: %s", question, grants.ScrubCredentials(answer))
	}
	project.Input.Facts = approval.Facts
	project.Input.SensitiveTerms = approval.SensitiveTerms
	project.Input.RequirementsConfirmed = true
	grants.SanitizeInput(&project.Input)
	if err := grants.ValidateInput(project.Input); err != nil {
		grantError(response, err)
		return
	}
	if err := grants.ValidateRequirements(approval.Requirements, project.Input, project.Evidence); err != nil {
		grantError(response, err)
		return
	}
	project.Requirements = approval.Requirements
	project.Stage = "writing"
	project.ResumeStage = ""
	project.Error = ""
	project.Round++
	project.Input.FactsConfirmed = true
	if err := grants.PreparePending(&project); err != nil {
		grantError(response, err)
		return
	}
	project.ResearchMarkdown = grants.BuildResearchRecord(project)
	if err := s.store.UpdateGrantProject(request.Context(), &project, project.Version); err != nil {
		grantError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, project)
}

func (s *Server) stopGrantProject(response http.ResponseWriter, request *http.Request) {
	id, ok := grantID(response, request)
	if !ok {
		return
	}
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	project, err := s.store.LoadGrantProject(request.Context(), id)
	if err != nil {
		grantError(response, err)
		return
	}
	if project.Stage == "completed" || project.Stage == "cancelled" {
		grantError(response, errors.New("tamamlanan veya iptal edilen çalışma durdurulamaz"))
		return
	}
	for _, conversationID := range project.Conversations {
		if _, err := s.store.RequestConversationCancel(request.Context(), conversationID); err != nil {
			grantError(response, err)
			return
		}
	}
	if _, err := s.store.RefreshGrantResponses(request.Context(), &project); err != nil {
		grantError(response, err)
		return
	}
	project.Stage = "cancelled"
	project.Pending = nil
	project.Error = ""
	project.ResearchMarkdown = grants.BuildResearchRecord(project)
	if err := s.store.UpdateGrantProject(request.Context(), &project, project.Version); err != nil {
		grantError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, project)
}

func (s *Server) retryGrantProject(response http.ResponseWriter, request *http.Request) {
	id, ok := grantID(response, request)
	if !ok {
		return
	}
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	project, err := s.store.LoadGrantProject(request.Context(), id)
	if err != nil {
		grantError(response, err)
		return
	}
	if project.Stage != "blocked" || project.ResumeStage == "" {
		grantError(response, errors.New("yalnızca engellenen aşama açıkça yeniden denenebilir"))
		return
	}
	if _, err := selectProviders(grants.Providers); err != nil {
		grantError(response, err)
		return
	}
	project.Stage = project.ResumeStage
	if project.Stage == "review" && len(project.Issues) > 0 {
		project.Stage = "revision"
	}
	project.Round++
	project.ResumeStage = ""
	project.Error = ""
	if err := grants.PreparePending(&project); err != nil {
		grantError(response, err)
		return
	}
	if err := s.store.UpdateGrantProject(request.Context(), &project, project.Version); err != nil {
		grantError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, project)
}

func (c *Client) GrantProjects(ctx context.Context) ([]grants.ProjectSummary, error) {
	var result []grants.ProjectSummary
	err := c.do(ctx, http.MethodGet, "/v1/grants/projects", nil, &result, http.StatusOK)
	return result, err
}
func (c *Client) CreateGrantProject(ctx context.Context, input grants.ProjectInput) (grants.Project, error) {
	var result grants.Project
	err := c.doWithTimeout(ctx, 30*time.Second, http.MethodPost, "/v1/grants/projects", input, &result, http.StatusCreated)
	return result, err
}
func (c *Client) GrantProject(ctx context.Context, id int64) (grants.Project, error) {
	var result grants.Project
	err := c.doWithTimeout(ctx, 45*time.Second, http.MethodGet, "/v1/grants/projects/"+strconv.FormatInt(id, 10), nil, &result, http.StatusOK)
	return result, err
}
func (c *Client) ApproveGrantSend(ctx context.Context, id int64, input grants.SendApproval) (grants.Project, error) {
	var result grants.Project
	err := c.do(ctx, http.MethodPost, "/v1/grants/projects/"+strconv.FormatInt(id, 10)+"/send", input, &result, http.StatusAccepted)
	return result, err
}
func (c *Client) ApproveGrantResearch(ctx context.Context, id int64, input grants.ResearchApproval) (grants.Project, error) {
	var result grants.Project
	err := c.do(ctx, http.MethodPost, "/v1/grants/projects/"+strconv.FormatInt(id, 10)+"/research", input, &result, http.StatusOK)
	return result, err
}
func (c *Client) StopGrantProject(ctx context.Context, id int64) (grants.Project, error) {
	var result grants.Project
	err := c.do(ctx, http.MethodPost, "/v1/grants/projects/"+strconv.FormatInt(id, 10)+"/stop", nil, &result, http.StatusOK)
	return result, err
}
func (c *Client) RetryGrantProject(ctx context.Context, id int64) (grants.Project, error) {
	var result grants.Project
	err := c.do(ctx, http.MethodPost, "/v1/grants/projects/"+strconv.FormatInt(id, 10)+"/retry", nil, &result, http.StatusOK)
	return result, err
}
