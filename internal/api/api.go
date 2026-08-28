package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/provider"
	"github.com/Emirfs/conclave/internal/store"
	"github.com/Emirfs/conclave/internal/update"
	"github.com/Emirfs/conclave/internal/vcs"
	"github.com/Emirfs/conclave/internal/version"
)

type Server struct {
	store *store.Store
	token string
	// updates may be nil: a daemon built without a release check still serves
	// everything else, it just has nothing to say about newer versions.
	updates *update.Checker
}

func NewServer(store *store.Store, token string, updates *update.Checker) *Server {
	return &Server{store: store, token: token, updates: updates}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/snapshot", s.snapshot)
	mux.HandleFunc("POST /v1/runs", s.createRun)
	mux.HandleFunc("POST /v1/canvas/conversations/{id}/turns", s.createTurn)
	mux.HandleFunc("GET /v1/canvas", s.canvas)
	mux.HandleFunc("GET /v1/search", s.search)
	mux.HandleFunc("GET /v1/usage", s.usage)
	mux.HandleFunc("GET /v1/canvas/conversations/{id}/export", s.exportConversation)
	mux.HandleFunc("GET /v1/canvas/export", s.exportBoard)
	mux.HandleFunc("POST /v1/canvas/import", s.importBoard)
	mux.HandleFunc("POST /v1/canvas/conversations", s.createConversation)
	mux.HandleFunc("POST /v1/canvas/notes", s.createNote)
	mux.HandleFunc("POST /v1/canvas/joins", s.createJoin)
	mux.HandleFunc("POST /v1/canvas/gates", s.createGate)
	mux.HandleFunc("PUT /v1/canvas/gates/{id}", s.setGate)
	mux.HandleFunc("POST /v1/canvas/triggers", s.createTrigger)
	mux.HandleFunc("PUT /v1/canvas/triggers/{id}", s.setTrigger)
	mux.HandleFunc("POST /v1/canvas/triggers/{id}/fire", s.fireTrigger)
	mux.HandleFunc("GET /v1/canvas/runs", s.flowRuns)
	mux.HandleFunc("GET /v1/canvas/runs/{id}", s.flowRun)
	mux.HandleFunc("POST /v1/canvas/runs/{id}/report", s.reportFlowRun)
	mux.HandleFunc("POST /v1/canvas/runs/{id}/stop", s.stopFlowRun)
	mux.HandleFunc("POST /v1/canvas/pipelines", s.createPipeline)
	mux.HandleFunc("PUT /v1/canvas/pipelines/{id}", s.setPipeline)
	mux.HandleFunc("POST /v1/canvas/pipelines/{id}/runs", s.startPipeline)
	mux.HandleFunc("PATCH /v1/canvas/nodes", s.patchCanvasNode)
	mux.HandleFunc("DELETE /v1/canvas/nodes/{id}", s.deleteCanvasNode)
	mux.HandleFunc("PUT /v1/canvas/conversations/{id}/project", s.setProject)
	mux.HandleFunc("GET /v1/canvas/conversations/{id}/changes", s.projectChanges)
	mux.HandleFunc("GET /v1/canvas/conversations/{id}/diff", s.projectDiff)
	mux.HandleFunc("POST /v1/canvas/links", s.createLink)
	mux.HandleFunc("PATCH /v1/canvas/links/{id}", s.updateLink)
	mux.HandleFunc("DELETE /v1/canvas/links/{id}", s.deleteLink)
	mux.HandleFunc("PUT /v1/canvas/conversations/{id}/loop", s.setLoop)
	mux.HandleFunc("PUT /v1/canvas/conversations/{id}/loop/running", s.setLoopRunning)
	mux.HandleFunc("PUT /v1/canvas/conversations/{id}/role", s.setRole)
	mux.HandleFunc("PUT /v1/canvas/conversations/{id}/model", s.setModel)
	mux.HandleFunc("GET /v1/providers/{name}/models", s.providerModels)
	mux.HandleFunc("POST /v1/canvas/conversations/{id}/cancel", s.cancelConversation)
	mux.HandleFunc("POST /v1/canvas/conversations/{id}/resume", s.resumeDialogue)
	mux.HandleFunc("POST /v1/canvas/conversations/{id}/branch", s.branch)
	mux.HandleFunc("GET /v1/update", s.updateStatus)
	mux.HandleFunc("POST /v1/update/check", s.checkUpdate)
	return s.authenticate(mux)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Origin") != "" {
			writeError(response, http.StatusForbidden, errors.New("browser-origin requests are not allowed"))
			return
		}
		authorization := request.Header.Get("Authorization")
		if s.token == "" || !strings.HasPrefix(authorization, "Bearer ") {
			writeError(response, http.StatusUnauthorized, errors.New("invalid daemon token"))
			return
		}
		provided := strings.TrimPrefix(authorization, "Bearer ")
		if len(provided) != len(s.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
			writeError(response, http.StatusUnauthorized, errors.New("invalid daemon token"))
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) snapshot(response http.ResponseWriter, request *http.Request) {
	runs, err := s.store.ListRuns(request.Context(), 20)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	providers := provider.Discover()
	quota, err := s.store.ProviderQuota(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	for index := range providers {
		if item, known := quota[providers[index].Name]; known {
			providers[index].Quota = &item
		}
	}
	writeJSON(response, http.StatusOK, domain.Snapshot{
		Healthy: true, Version: version.Version, Providers: providers, Runs: runs,
	})
}

// updateStatus answers from the cached check. Reading it never reaches the
// network, so a canvas poll cannot be slowed down by GitHub.
func (s *Server) updateStatus(response http.ResponseWriter, request *http.Request) {
	if s.updates == nil {
		writeJSON(response, http.StatusOK, update.Status{Current: version.Version})
		return
	}
	writeJSON(response, http.StatusOK, s.updates.Status())
}

// checkUpdate refreshes the answer now, for a user who asks instead of waiting
// for the next daily check.
func (s *Server) checkUpdate(response http.ResponseWriter, request *http.Request) {
	if s.updates == nil {
		writeJSON(response, http.StatusOK, update.Status{Current: version.Version})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	writeJSON(response, http.StatusOK, s.updates.CheckNow(ctx))
}

func (s *Server) createTurn(response http.ResponseWriter, request *http.Request) {
	conversationID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || conversationID <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input domain.TurnRequest
	if !decodeJSON(response, request, 64<<10, &input) {
		return
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" || utf8.RuneCountInString(input.Prompt) > 20_000 {
		writeError(response, http.StatusBadRequest, errors.New("prompt requires 1 to 20000 characters"))
		return
	}
	id, err := s.store.CreateConversationTurn(request.Context(), conversationID, input.Prompt)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]int64{"id": id})
}

func (s *Server) createRun(response http.ResponseWriter, request *http.Request) {
	var input domain.RunRequest
	if !decodeJSON(response, request, 1<<20, &input) {
		return
	}
	if !filepath.IsAbs(input.Project) {
		writeError(response, http.StatusBadRequest, errors.New("project must be an absolute path"))
		return
	}
	project := filepath.Clean(input.Project)
	info, err := os.Stat(project)
	if err != nil || !info.IsDir() {
		writeError(response, http.StatusBadRequest, errors.New("project must be an existing directory"))
		return
	}
	if len(input.Stages) == 0 || len(input.Stages) > 50 {
		writeError(response, http.StatusBadRequest, errors.New("pipeline requires 1 to 50 stages"))
		return
	}
	for _, stage := range input.Stages {
		if stage.Name == "" || len(stage.Name) > 100 || len(stage.Command) == 0 || len(stage.Command) > 100 || stage.Command[0] == "" {
			writeError(response, http.StatusBadRequest, errors.New("each stage requires a name and command"))
			return
		}
		for _, argument := range stage.Command {
			if len(argument) > 32*1024 {
				writeError(response, http.StatusBadRequest, errors.New("stage argument exceeds 32 KiB"))
				return
			}
		}
	}
	input.Project = project
	id, err := s.store.CreateRun(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]int64{"id": id})
}

// decodeJSON enforces the shared request rules: JSON content type, a body size
// cap, and no unknown fields so a typo in a client is an error, not a silent
// no-op.
func decodeJSON(response http.ResponseWriter, request *http.Request, limit int64, target any) bool {
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, errors.New("content type must be application/json"))
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (s *Server) canvas(response http.ResponseWriter, request *http.Request) {
	canvas, err := s.store.Canvas(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, canvas)
}

func (s *Server) createConversation(response http.ResponseWriter, request *http.Request) {
	var input domain.NewConversation
	if !decodeJSON(response, request, 16<<10, &input) {
		return
	}
	if input.Kind != domain.KindSolo && input.Kind != domain.KindGroup {
		writeError(response, http.StatusBadRequest, errors.New("kind must be solo or group"))
		return
	}
	selected, err := selectProviders(input.Providers)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	if input.Kind == domain.KindSolo && len(selected) != 1 {
		writeError(response, http.StatusBadRequest, errors.New("a solo conversation needs exactly one provider"))
		return
	}
	project, err := validProject(input.ProjectPath)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	access, err := validAccess(input.Access)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	input.ProjectPath = project
	input.Access = access
	input.Providers = selected
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = selected[0]
	}
	if utf8.RuneCountInString(input.Title) > 120 {
		writeError(response, http.StatusBadRequest, errors.New("title is limited to 120 characters"))
		return
	}
	conversation, err := s.store.CreateConversation(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, conversation)
}

func (s *Server) createNote(response http.ResponseWriter, request *http.Request) {
	var input domain.NewNote
	if !decodeJSON(response, request, 64<<10, &input) {
		return
	}
	if utf8.RuneCountInString(input.Body) > 20_000 {
		writeError(response, http.StatusBadRequest, errors.New("note is limited to 20000 characters"))
		return
	}
	note, err := s.store.CreateNote(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, note)
}

// createJoin puts a waiting point on the board. It carries no text of its own
// beyond a name, so it takes the same body a note does.
func (s *Server) createJoin(response http.ResponseWriter, request *http.Request) {
	var input domain.NewNote
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	if utf8.RuneCountInString(input.Body) > 200 {
		writeError(response, http.StatusBadRequest, errors.New("join name is limited to 200 characters"))
		return
	}
	join, err := s.store.CreateJoin(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, join)
}

// createGate puts a decision point on the board. It starts as a plain "is
// there anything here" check, the one condition useful before a pattern exists.
func (s *Server) createGate(response http.ResponseWriter, request *http.Request) {
	var input domain.NewGate
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	gate, err := s.store.CreateGate(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, gate)
}

// setGate replaces a gate's condition. A pattern that will not compile is
// refused here, not when a run reaches the gate and stops for no visible reason.
func (s *Server) setGate(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("gate id must be a positive integer"))
		return
	}
	var input domain.GateConfig
	if !decodeJSON(response, request, 16<<10, &input) {
		return
	}
	if utf8.RuneCountInString(input.Pattern) > 2000 {
		writeError(response, http.StatusBadRequest,
			errors.New("a gate condition is limited to 2000 characters"))
		return
	}
	if err := s.store.SetGate(request.Context(), id, input); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("gate not found"))
			return
		}
		writeError(response, http.StatusBadRequest, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// createTrigger puts a starting point on the board. It arrives switched off
// and empty; what it sends and when is set on the card itself.
func (s *Server) createTrigger(response http.ResponseWriter, request *http.Request) {
	var input domain.NewTrigger
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	trigger, err := s.store.CreateTrigger(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, trigger)
}

// setTrigger replaces a trigger's message and schedule in one write, arming or
// disarming it at the same time.
func (s *Server) setTrigger(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("trigger id must be a positive integer"))
		return
	}
	var input domain.TriggerConfig
	if !decodeJSON(response, request, 64<<10, &input) {
		return
	}
	if utf8.RuneCountInString(input.Prompt) > 20_000 {
		writeError(response, http.StatusBadRequest,
			errors.New("a trigger message is limited to 20000 characters"))
		return
	}
	if err := s.store.SetTrigger(request.Context(), id, input); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("trigger not found"))
			return
		}
		writeError(response, http.StatusBadRequest, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// fireTrigger runs a trigger now, whatever its schedule says. A routine nobody
// can start by hand is a routine nobody can test.
func (s *Server) fireTrigger(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("trigger id must be a positive integer"))
		return
	}
	delivered, err := s.store.FireTrigger(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("trigger not found"))
			return
		}
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]int{"delivered": delivered})
}

// flowRuns lists recent journeys across the board, the ones still going first.
func (s *Server) flowRuns(response http.ResponseWriter, request *http.Request) {
	limit := 0
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(response, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = parsed
	}
	runs, err := s.store.FlowRuns(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, runs)
}

// flowRun reports everything that happened in one run, in order.
func (s *Server) flowRun(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("run id must be a positive integer"))
		return
	}
	detail, err := s.store.FlowRunDetail(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("run not found"))
			return
		}
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, detail)
}

// reportFlowRun writes a run up as a note on the board.
func (s *Server) reportFlowRun(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("run id must be a positive integer"))
		return
	}
	note, err := s.store.ReportRunToBoard(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("run not found"))
			return
		}
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, note)
}

// stopFlowRun ends one journey across the board and stops every card still
// working on it. Stopping cards one at a time is the thing this exists to
// replace.
func (s *Server) stopFlowRun(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("run id must be a positive integer"))
		return
	}
	stopped, err := s.store.StopFlowRun(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, domain.CancelResult{Stopped: stopped})
}

func (s *Server) patchCanvasNode(response http.ResponseWriter, request *http.Request) {
	var input domain.CanvasNodePatch
	if !decodeJSON(response, request, 64<<10, &input) {
		return
	}
	if input.ID <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("node id is required"))
		return
	}
	if input.Body != nil && utf8.RuneCountInString(*input.Body) > 20_000 {
		writeError(response, http.StatusBadRequest, errors.New("note is limited to 20000 characters"))
		return
	}
	err := s.store.PatchCanvasNode(request.Context(), input)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("node does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteCanvasNode(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("node id must be a positive integer"))
		return
	}
	err = s.store.DeleteCanvasNode(request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("node does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// validProject accepts an absolute path to an existing directory, or the empty
// string, which means "no project, use a scratch directory".
func validProject(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("project must be an absolute path")
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", errors.New("project must be an existing directory")
	}
	return clean, nil
}

func validAccess(access string) (string, error) {
	switch provider.Access(access) {
	case provider.AccessRead, provider.AccessEdit:
		return access, nil
	case "":
		return string(provider.AccessEdit), nil
	default:
		return "", errors.New("access must be read or edit")
	}
}

func (s *Server) setProject(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input domain.ProjectRequest
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	project, err := validProject(input.ProjectPath)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	access, err := validAccess(input.Access)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	err = s.store.SetConversationProject(request.Context(), id, project, access)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("conversation does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) setRole(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input domain.RoleRequest
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	err = s.store.SetConversationRole(request.Context(), id, input.Normalised().Role)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("conversation does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// setModel picks the model one provider of a card runs on. The name is not
// checked against a list: a CLI gains models without this build changing, and
// refusing an unknown one would only stop the user from using them.
func (s *Server) setModel(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input domain.ModelRequest
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	input = input.Normalised()
	if input.Provider == "" {
		writeError(response, http.StatusBadRequest, errors.New("provider is required"))
		return
	}
	if err := validModel(input.Model); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	err = s.store.SetConversationModel(request.Context(), id, input.Provider, input.Model)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("conversation does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// validModel keeps the value usable as a single command argument. A model name
// becomes one element of an argument array, so whitespace would split it and a
// leading dash would be read as a flag.
func validModel(model string) error {
	if model == "" {
		return nil
	}
	if utf8.RuneCountInString(model) > 120 {
		return errors.New("model is limited to 120 characters")
	}
	if strings.HasPrefix(model, "-") {
		return errors.New("model must not start with a dash")
	}
	for _, character := range model {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return errors.New("model must not contain spaces or control characters")
		}
	}
	return nil
}

// providerModels lists what a provider can be asked for, so a card offers real
// choices instead of a blank field.
func (s *Server) providerModels(response http.ResponseWriter, request *http.Request) {
	name := request.PathValue("name")
	if name == "" {
		writeError(response, http.StatusBadRequest, errors.New("provider name is required"))
		return
	}
	models, err := provider.Models(request.Context(), name)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, models)
}

// resumeDialogue clears the parked state so a stalled exchange can be pushed on
// without the user having to remember what it was waiting for.
// exportConversation renders one card's whole transcript as Markdown. It is
// text, not JSON: what comes out is meant to be read and kept.
// usage reports what each provider spent over a window, together with its own
// allowance report. It is a read and answers from SQLite, so a panel may poll.
func (s *Server) usage(response http.ResponseWriter, request *http.Request) {
	days := 7
	if raw := request.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(response, http.StatusBadRequest, errors.New("days must be a positive integer"))
			return
		}
		days = parsed
	}
	report, err := s.store.Usage(request.Context(), days)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, report)
}

// boardImportOffset is how far an imported board lands from the origin, so it
// does not arrive on top of what is already on the canvas.
const boardImportOffset = 120

func (s *Server) exportConversation(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	markdown, err := s.store.ExportConversation(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("conversation not found"))
			return
		}
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, markdown)
}

// exportBoard writes the whole board out as one self-contained value.
func (s *Server) exportBoard(response http.ResponseWriter, request *http.Request) {
	export, err := s.store.ExportBoard(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, export)
}

// importBoard adds an exported board alongside the current one. It is additive
// on purpose: replacing a board would throw away work that is still running.
func (s *Server) importBoard(response http.ResponseWriter, request *http.Request) {
	var input domain.BoardExport
	if !decodeJSON(response, request, 32<<20, &input) {
		return
	}
	result, err := s.store.ImportBoard(request.Context(), input, boardImportOffset, boardImportOffset)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusCreated, result)
}

// createPipeline puts a new, empty pipeline card on the board.
func (s *Server) createPipeline(response http.ResponseWriter, request *http.Request) {
	var input domain.NewPipeline
	if !decodeJSON(response, request, 8<<10, &input) {
		return
	}
	pipeline, err := s.store.CreatePipeline(request.Context(), input)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, pipeline)
}

// setPipeline replaces a pipeline's title, project and stages in one write.
func (s *Server) setPipeline(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("pipeline id must be a positive integer"))
		return
	}
	var input domain.PipelineConfig
	if !decodeJSON(response, request, 64<<10, &input) {
		return
	}
	// A pipeline runs commands in a directory, so its project is validated the
	// same way a card's is.
	project, err := validProject(input.ProjectPath)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	input.ProjectPath = project
	if err := s.store.SetPipeline(request.Context(), id, input); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("pipeline not found"))
			return
		}
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// startPipeline queues the pipeline's stages as an ordinary run, which the
// daemon's existing pipeline worker picks up.
func (s *Server) startPipeline(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("pipeline id must be a positive integer"))
		return
	}
	runID, err := s.store.StartPipelineRun(request.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, errors.New("pipeline not found"))
			return
		}
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]int64{"run_id": runID})
}

// search looks for text anywhere on the board. It is a read, so it takes its
// query from the URL and answers with whatever it found, including nothing.
func (s *Server) search(response http.ResponseWriter, request *http.Request) {
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(response, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = parsed
	}
	hits, err := s.store.Search(request.Context(), query, limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, hits)
}

// cancelConversation stops whatever a card is doing. The request is recorded
// and answered immediately; the worker that owns a running provider process
// ends it on its next poll, so this never blocks on a provider.
func (s *Server) cancelConversation(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	stopped, err := s.store.RequestConversationCancel(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, domain.CancelResult{Stopped: stopped})
}

func (s *Server) resumeDialogue(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	if err := s.store.ResumeDialogue(request.Context(), id); err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// branch forks an existing answer into one or more new cards.
func (s *Server) branch(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input domain.BranchRequest
	// The answer being branched from is a whole provider response.
	if !decodeJSON(response, request, 256<<10, &input) {
		return
	}
	branches, err := s.store.BranchFrom(request.Context(), id, input.Answer, input.Providers)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusCreated, branches)
}

func (s *Server) conversationProject(ctx context.Context, id int64) (string, error) {
	canvas, err := s.store.Canvas(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range canvas.Conversations {
		if item.ID == id {
			return item.ProjectPath, nil
		}
	}
	return "", errors.New("conversation does not exist")
}

func (s *Server) projectChanges(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	project, err := s.conversationProject(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	status, err := vcs.ProjectStatus(request.Context(), project)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (s *Server) projectDiff(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	path := request.URL.Query().Get("path")
	if path == "" {
		writeError(response, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	project, err := s.conversationProject(request.Context(), id)
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	diff, err := vcs.FileDiff(request.Context(), project, path)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusOK, diff)
}

func (s *Server) createLink(response http.ResponseWriter, request *http.Request) {
	var input domain.NewLink
	// A briefing is free text the user writes, so the body has to have room for
	// it on top of the link itself.
	if !decodeJSON(response, request, 16<<10, &input) {
		return
	}
	if input.SourceID <= 0 || input.TargetID <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("source and target node ids are required"))
		return
	}
	options := domain.LinkOptions{
		Mode: input.Mode, MaxRounds: input.MaxRounds,
		UntilDone: input.UntilDone, Briefing: input.Briefing,
		SourceHandle: input.SourceHandle,
	}
	if input.Pair {
		// Pairing links both ways, so the two cards answer each other.
		links, err := s.store.PairNodes(request.Context(), input.SourceID, input.TargetID, options)
		if err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		writeJSON(response, http.StatusCreated, links)
		return
	}
	link, err := s.store.CreateLink(request.Context(), input.SourceID, input.TargetID, options)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	writeJSON(response, http.StatusCreated, link)
}

func (s *Server) updateLink(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("link id must be a positive integer"))
		return
	}
	var input domain.LinkOptions
	if !decodeJSON(response, request, 16<<10, &input) {
		return
	}
	err = s.store.UpdateLink(request.Context(), id, input)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("link does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) setLoop(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input domain.LoopConfig
	if !decodeJSON(response, request, 64<<10, &input) {
		return
	}
	for _, step := range input.Steps {
		if utf8.RuneCountInString(step.Command) > 2000 {
			writeError(response, http.StatusBadRequest, errors.New("a step command is limited to 2000 characters"))
			return
		}
	}
	err = s.store.SetLoop(request.Context(), id, input)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("conversation does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) setLoopRunning(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("conversation id must be a positive integer"))
		return
	}
	var input struct {
		Running bool `json:"running"`
	}
	if !decodeJSON(response, request, 4<<10, &input) {
		return
	}
	err = s.store.SetLoopRunning(request.Context(), id, input.Running)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("conversation does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteLink(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, errors.New("link id must be a positive integer"))
		return
	}
	err = s.store.DeleteLink(request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, errors.New("link does not exist"))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// selectProviders keeps only discovered, chat-capable providers and drops
// duplicates while preserving the caller's order.
func selectProviders(requested []string) ([]string, error) {
	available := make(map[string]bool)
	for _, item := range provider.Discover() {
		if item.Available && item.Kind != "memory" {
			available[item.Name] = true
		}
	}
	seen := make(map[string]bool)
	selected := make([]string, 0, len(requested))
	for _, name := range requested {
		if !available[name] {
			return nil, fmt.Errorf("provider %q is not available", name)
		}
		if !seen[name] {
			seen[name] = true
			selected = append(selected, name)
		}
	}
	if len(selected) == 0 || len(selected) > 4 {
		return nil, errors.New("select 1 to 4 providers")
	}
	return selected, nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": err.Error()})
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) Snapshot(ctx context.Context) (domain.Snapshot, error) {
	var snapshot domain.Snapshot
	err := c.do(ctx, http.MethodGet, "/v1/snapshot", nil, &snapshot, http.StatusOK)
	return snapshot, err
}

func (c *Client) CreateRun(ctx context.Context, input domain.RunRequest) (int64, error) {
	var result map[string]int64
	err := c.do(ctx, http.MethodPost, "/v1/runs", input, &result, http.StatusAccepted)
	return result["id"], err
}

func (c *Client) CreateTurn(ctx context.Context, conversationID int64, prompt string) (int64, error) {
	var result map[string]int64
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/turns"
	err := c.do(ctx, http.MethodPost, path, domain.TurnRequest{Prompt: prompt}, &result, http.StatusAccepted)
	return result["id"], err
}

// do performs an authenticated request and decodes a JSON body into result.
// A nil result means the caller only cares that the request succeeded.
func (c *Client) do(ctx context.Context, method, path string, payload, result any, want int) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		var failure map[string]string
		_ = json.NewDecoder(response.Body).Decode(&failure)
		if message := failure["error"]; message != "" {
			return errors.New(message)
		}
		return fmt.Errorf("daemon returned %s", response.Status)
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(result)
}

// text fetches a response that is not JSON. Only the Markdown export needs it,
// and decoding that as JSON would mean quoting a document into a string.
func (c *Client) text(ctx context.Context, path string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		var failure map[string]string
		_ = json.Unmarshal(body, &failure)
		if message := failure["error"]; message != "" {
			return "", errors.New(message)
		}
		return "", fmt.Errorf("daemon returned %s", response.Status)
	}
	return string(body), nil
}

func (c *Client) Canvas(ctx context.Context) (domain.Canvas, error) {
	var canvas domain.Canvas
	err := c.do(ctx, http.MethodGet, "/v1/canvas", nil, &canvas, http.StatusOK)
	return canvas, err
}

func (c *Client) CreateConversation(ctx context.Context, input domain.NewConversation) (domain.Conversation, error) {
	var conversation domain.Conversation
	err := c.do(ctx, http.MethodPost, "/v1/canvas/conversations", input, &conversation, http.StatusCreated)
	return conversation, err
}

func (c *Client) CreateNote(ctx context.Context, input domain.NewNote) (domain.CanvasNode, error) {
	var node domain.CanvasNode
	err := c.do(ctx, http.MethodPost, "/v1/canvas/notes", input, &node, http.StatusCreated)
	return node, err
}

func (c *Client) CreateJoin(ctx context.Context, input domain.NewNote) (domain.CanvasNode, error) {
	var node domain.CanvasNode
	err := c.do(ctx, http.MethodPost, "/v1/canvas/joins", input, &node, http.StatusCreated)
	return node, err
}

func (c *Client) CreateGate(ctx context.Context, input domain.NewGate) (domain.Gate, error) {
	var gate domain.Gate
	err := c.do(ctx, http.MethodPost, "/v1/canvas/gates", input, &gate, http.StatusCreated)
	return gate, err
}

func (c *Client) SetGate(ctx context.Context, id int64, config domain.GateConfig) error {
	path := "/v1/canvas/gates/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodPut, path, config, nil, http.StatusNoContent)
}

func (c *Client) CreateTrigger(ctx context.Context, input domain.NewTrigger) (domain.Trigger, error) {
	var trigger domain.Trigger
	err := c.do(ctx, http.MethodPost, "/v1/canvas/triggers", input, &trigger, http.StatusCreated)
	return trigger, err
}

func (c *Client) SetTrigger(ctx context.Context, id int64, config domain.TriggerConfig) error {
	path := "/v1/canvas/triggers/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodPut, path, config, nil, http.StatusNoContent)
}

func (c *Client) FireTrigger(ctx context.Context, id int64) (int, error) {
	var result struct {
		Delivered int `json:"delivered"`
	}
	path := "/v1/canvas/triggers/" + strconv.FormatInt(id, 10) + "/fire"
	err := c.do(ctx, http.MethodPost, path, nil, &result, http.StatusOK)
	return result.Delivered, err
}

func (c *Client) FlowRuns(ctx context.Context, limit int) ([]domain.FlowRun, error) {
	path := "/v1/canvas/runs"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var runs []domain.FlowRun
	err := c.do(ctx, http.MethodGet, path, nil, &runs, http.StatusOK)
	return runs, err
}

func (c *Client) FlowRun(ctx context.Context, runID int64) (domain.FlowRunDetail, error) {
	var detail domain.FlowRunDetail
	path := "/v1/canvas/runs/" + strconv.FormatInt(runID, 10)
	err := c.do(ctx, http.MethodGet, path, nil, &detail, http.StatusOK)
	return detail, err
}

func (c *Client) ReportFlowRun(ctx context.Context, runID int64) (domain.CanvasNode, error) {
	var note domain.CanvasNode
	path := "/v1/canvas/runs/" + strconv.FormatInt(runID, 10) + "/report"
	err := c.do(ctx, http.MethodPost, path, nil, &note, http.StatusCreated)
	return note, err
}

func (c *Client) StopFlowRun(ctx context.Context, runID int64) (int, error) {
	var result domain.CancelResult
	path := "/v1/canvas/runs/" + strconv.FormatInt(runID, 10) + "/stop"
	err := c.do(ctx, http.MethodPost, path, nil, &result, http.StatusOK)
	return result.Stopped, err
}

func (c *Client) PatchCanvasNode(ctx context.Context, patch domain.CanvasNodePatch) error {
	return c.do(ctx, http.MethodPatch, "/v1/canvas/nodes", patch, nil, http.StatusNoContent)
}

func (c *Client) SetProject(ctx context.Context, conversationID int64, input domain.ProjectRequest) error {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/project"
	return c.do(ctx, http.MethodPut, path, input, nil, http.StatusNoContent)
}

func (c *Client) SetRole(ctx context.Context, conversationID int64, input domain.RoleRequest) error {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/role"
	return c.do(ctx, http.MethodPut, path, input, nil, http.StatusNoContent)
}

func (c *Client) SetModel(ctx context.Context, conversationID int64, input domain.ModelRequest) error {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/model"
	return c.do(ctx, http.MethodPut, path, input, nil, http.StatusNoContent)
}

func (c *Client) ProviderModels(ctx context.Context, name string) (domain.ProviderModels, error) {
	var models domain.ProviderModels
	if err := c.do(ctx, http.MethodGet, "/v1/providers/"+url.PathEscape(name)+"/models", nil, &models, http.StatusOK); err != nil {
		return domain.ProviderModels{}, err
	}
	return models, nil
}

func (c *Client) Usage(ctx context.Context, days int) (domain.UsageReport, error) {
	var report domain.UsageReport
	err := c.do(ctx, http.MethodGet, "/v1/usage?days="+strconv.Itoa(days), nil, &report, http.StatusOK)
	return report, err
}

func (c *Client) ExportConversation(ctx context.Context, conversationID int64) (string, error) {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/export"
	return c.text(ctx, path)
}

func (c *Client) ExportBoard(ctx context.Context) (domain.BoardExport, error) {
	var export domain.BoardExport
	err := c.do(ctx, http.MethodGet, "/v1/canvas/export", nil, &export, http.StatusOK)
	return export, err
}

func (c *Client) ImportBoard(ctx context.Context, export domain.BoardExport) (domain.ImportResult, error) {
	var result domain.ImportResult
	err := c.do(ctx, http.MethodPost, "/v1/canvas/import", export, &result, http.StatusCreated)
	return result, err
}

func (c *Client) CreatePipeline(ctx context.Context, input domain.NewPipeline) (domain.Pipeline, error) {
	var pipeline domain.Pipeline
	err := c.do(ctx, http.MethodPost, "/v1/canvas/pipelines", input, &pipeline, http.StatusCreated)
	return pipeline, err
}

func (c *Client) SetPipeline(ctx context.Context, id int64, config domain.PipelineConfig) error {
	path := "/v1/canvas/pipelines/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodPut, path, config, nil, http.StatusNoContent)
}

func (c *Client) StartPipeline(ctx context.Context, id int64) (int64, error) {
	path := "/v1/canvas/pipelines/" + strconv.FormatInt(id, 10) + "/runs"
	var created struct {
		RunID int64 `json:"run_id"`
	}
	err := c.do(ctx, http.MethodPost, path, nil, &created, http.StatusCreated)
	return created.RunID, err
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]domain.SearchHit, error) {
	path := "/v1/search?q=" + url.QueryEscape(query) + "&limit=" + strconv.Itoa(limit)
	var hits []domain.SearchHit
	err := c.do(ctx, http.MethodGet, path, nil, &hits, http.StatusOK)
	return hits, err
}

func (c *Client) CancelConversation(ctx context.Context, conversationID int64) (domain.CancelResult, error) {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/cancel"
	var result domain.CancelResult
	err := c.do(ctx, http.MethodPost, path, nil, &result, http.StatusOK)
	return result, err
}

func (c *Client) ResumeDialogue(ctx context.Context, conversationID int64) error {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/resume"
	return c.do(ctx, http.MethodPost, path, nil, nil, http.StatusNoContent)
}

func (c *Client) Branch(ctx context.Context, conversationID int64, input domain.BranchRequest) ([]domain.Conversation, error) {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/branch"
	var branches []domain.Conversation
	err := c.do(ctx, http.MethodPost, path, input, &branches, http.StatusCreated)
	return branches, err
}

func (c *Client) ProjectChanges(ctx context.Context, conversationID int64) (vcs.Status, error) {
	var status vcs.Status
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/changes"
	err := c.do(ctx, http.MethodGet, path, nil, &status, http.StatusOK)
	return status, err
}

func (c *Client) FileDiff(ctx context.Context, conversationID int64, file string) (vcs.Diff, error) {
	var diff vcs.Diff
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) +
		"/diff?path=" + url.QueryEscape(file)
	err := c.do(ctx, http.MethodGet, path, nil, &diff, http.StatusOK)
	return diff, err
}

func (c *Client) CreateLink(ctx context.Context, input domain.NewLink) (domain.CanvasLink, error) {
	input.Pair = false
	var link domain.CanvasLink
	err := c.do(ctx, http.MethodPost, "/v1/canvas/links", input, &link, http.StatusCreated)
	return link, err
}

// PairLink links two cards in both directions; the server answers with both.
func (c *Client) PairLink(ctx context.Context, input domain.NewLink) ([]domain.CanvasLink, error) {
	input.Pair = true
	var links []domain.CanvasLink
	err := c.do(ctx, http.MethodPost, "/v1/canvas/links", input, &links, http.StatusCreated)
	return links, err
}

func (c *Client) UpdateLink(ctx context.Context, id int64, options domain.LinkOptions) error {
	return c.do(ctx, http.MethodPatch, "/v1/canvas/links/"+strconv.FormatInt(id, 10),
		options, nil, http.StatusNoContent)
}

func (c *Client) SetLoop(ctx context.Context, conversationID int64, input domain.LoopConfig) error {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/loop"
	return c.do(ctx, http.MethodPut, path, input, nil, http.StatusNoContent)
}

func (c *Client) SetLoopRunning(ctx context.Context, conversationID int64, running bool) error {
	path := "/v1/canvas/conversations/" + strconv.FormatInt(conversationID, 10) + "/loop/running"
	return c.do(ctx, http.MethodPut, path, map[string]bool{"running": running}, nil, http.StatusNoContent)
}

func (c *Client) DeleteLink(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, "/v1/canvas/links/"+strconv.FormatInt(id, 10), nil, nil, http.StatusNoContent)
}

func (c *Client) DeleteCanvasNode(ctx context.Context, id int64) error {
	path := "/v1/canvas/nodes/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodDelete, path, nil, nil, http.StatusNoContent)
}

func (c *Client) UpdateStatus(ctx context.Context) (update.Status, error) {
	var status update.Status
	err := c.do(ctx, http.MethodGet, "/v1/update", nil, &status, http.StatusOK)
	return status, err
}

func (c *Client) CheckUpdate(ctx context.Context) (update.Status, error) {
	var status update.Status
	err := c.do(ctx, http.MethodPost, "/v1/update/check", nil, &status, http.StatusOK)
	return status, err
}
