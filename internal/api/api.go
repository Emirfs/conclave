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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Emirfs/conclave/internal/domain"
	"github.com/Emirfs/conclave/internal/provider"
	"github.com/Emirfs/conclave/internal/store"
)

const Version = "0.1.0"

type Server struct {
	store *store.Store
	token string
}

func NewServer(store *store.Store, token string) *Server {
	return &Server{store: store, token: token}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/snapshot", s.snapshot)
	mux.HandleFunc("POST /v1/runs", s.createRun)
	mux.HandleFunc("POST /v1/canvas/conversations/{id}/turns", s.createTurn)
	mux.HandleFunc("GET /v1/canvas", s.canvas)
	mux.HandleFunc("POST /v1/canvas/conversations", s.createConversation)
	mux.HandleFunc("POST /v1/canvas/notes", s.createNote)
	mux.HandleFunc("PATCH /v1/canvas/nodes", s.patchCanvasNode)
	mux.HandleFunc("DELETE /v1/canvas/nodes/{id}", s.deleteCanvasNode)
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
		Healthy: true, Version: Version, Providers: providers, Runs: runs,
	})
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

func (c *Client) PatchCanvasNode(ctx context.Context, patch domain.CanvasNodePatch) error {
	return c.do(ctx, http.MethodPatch, "/v1/canvas/nodes", patch, nil, http.StatusNoContent)
}

func (c *Client) DeleteCanvasNode(ctx context.Context, id int64) error {
	path := "/v1/canvas/nodes/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodDelete, path, nil, nil, http.StatusNoContent)
}
