package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	return s.authenticate(mux)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Origin") != "" {
			writeError(response, http.StatusForbidden, errors.New("browser-origin requests are not allowed"))
			return
		}
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
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
	writeJSON(response, http.StatusOK, domain.Snapshot{
		Healthy: true, Version: Version, Providers: provider.Discover(), Runs: runs,
	})
}

func (s *Server) createRun(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, errors.New("content type must be application/json"))
		return
	}
	var input domain.RunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, err)
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/snapshot", nil)
	if err != nil {
		return snapshot, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return snapshot, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return snapshot, fmt.Errorf("daemon returned %s", response.Status)
	}
	err = json.NewDecoder(response.Body).Decode(&snapshot)
	return snapshot, err
}

func (c *Client) CreateRun(ctx context.Context, input domain.RunRequest) (int64, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		var failure map[string]string
		_ = json.NewDecoder(response.Body).Decode(&failure)
		return 0, fmt.Errorf("daemon rejected run: %s", failure["error"])
	}
	var result map[string]int64
	err = json.NewDecoder(response.Body).Decode(&result)
	return result["id"], err
}
