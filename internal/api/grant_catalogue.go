package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Emirfs/conclave/internal/grants"
)

var grantCatalogueMu sync.Mutex
var grantCatalogueRefreshMu sync.Mutex

const grantCatalogueRefreshInterval = 24 * time.Hour

func (s *Server) registerGrantCatalogue(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/grants/catalogue", s.grantCatalogue)
	mux.HandleFunc("POST /v1/grants/catalogue/refresh", s.refreshGrantCatalogue)
	mux.HandleFunc("POST /v1/grants/programmes", s.addGrantProgram)
}

// loadGrantCatalogueLocked loads persisted state and seeds the row once.
func (s *Server) loadGrantCatalogueLocked(ctx context.Context) (grants.Catalogue, error) {
	catalogue, err := s.store.LoadGrantCatalogue(ctx)
	if err == nil {
		return catalogue, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return grants.Catalogue{}, err
	}
	catalogue = grants.SeedCatalogue()
	if err := s.store.SaveGrantCatalogue(ctx, catalogue); err != nil {
		return grants.Catalogue{}, err
	}
	return catalogue, nil
}

func (s *Server) grantCatalogue(w http.ResponseWriter, r *http.Request) {
	grantCatalogueMu.Lock()
	catalogue, err := s.loadGrantCatalogueLocked(r.Context())
	grantCatalogueMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, catalogue)
	if catalogueIsStale(catalogue) {
		go s.refreshGrantCatalogueBackground()
	}
}

func catalogueIsStale(catalogue grants.Catalogue) bool {
	if catalogue.RefreshedAt == "" {
		return true
	}
	updated, err := time.Parse(time.RFC3339, catalogue.RefreshedAt)
	if err != nil {
		return true
	}
	age := time.Since(updated)
	if age >= grantCatalogueRefreshInterval {
		return true
	}
	for _, source := range catalogue.Sources {
		if source.Error != "" && age >= time.Hour {
			return true
		}
	}
	for _, program := range catalogue.Programs {
		if program.SourceError != "" && age >= time.Hour {
			return true
		}
	}
	return false
}

func (s *Server) refreshGrantCatalogueBackground() {
	if !grantCatalogueRefreshMu.TryLock() {
		return
	}
	defer grantCatalogueRefreshMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	grantCatalogueMu.Lock()
	previous, err := s.loadGrantCatalogueLocked(ctx)
	grantCatalogueMu.Unlock()
	if err != nil {
		return
	}
	refreshed, refreshErr := grants.RefreshCatalogue(ctx, previous)
	if refreshErr != nil {
		for i := range refreshed.Sources {
			refreshed.Sources[i].Error = refreshErr.Error()
		}
	}
	grantCatalogueMu.Lock()
	defer grantCatalogueMu.Unlock()
	latest, err := s.loadGrantCatalogueLocked(ctx)
	if err != nil {
		return
	}
	refreshed.Programs = mergeCataloguePrograms(latest.Programs, refreshed.Programs)
	if err := s.store.SaveGrantCatalogue(ctx, refreshed); err != nil {
		return
	}
}

func mergeCataloguePrograms(first, second []grants.Program) []grants.Program {
	programs := make([]grants.Program, 0, len(first)+len(second))
	seen := make(map[string]int, len(first)+len(second))
	for _, group := range [][]grants.Program{first, second} {
		for _, program := range group {
			key := program.ID
			if key == "" {
				key = strings.ToLower(program.URL + "\n" + program.Name)
			}
			if index, exists := seen[key]; exists {
				programs[index] = program
			} else {
				seen[key] = len(programs)
				programs = append(programs, program)
			}
		}
	}
	return programs
}

func (s *Server) refreshGrantCatalogue(w http.ResponseWriter, r *http.Request) {
	if !grantCatalogueRefreshMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("catalogue refresh is already running"))
		return
	}
	defer grantCatalogueRefreshMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	grantCatalogueMu.Lock()
	previous, err := s.loadGrantCatalogueLocked(ctx)
	grantCatalogueMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	refreshed, err := grants.RefreshCatalogue(ctx, previous)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	grantCatalogueMu.Lock()
	defer grantCatalogueMu.Unlock()
	latest, err := s.loadGrantCatalogueLocked(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	refreshed.Programs = mergeCataloguePrograms(latest.Programs, refreshed.Programs)
	if err := s.store.SaveGrantCatalogue(ctx, refreshed); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, refreshed)
}

type customGrantProgram struct {
	Name    string `json:"name"`
	Agency  string `json:"agency"`
	Country string `json:"country"`
	URL     string `json:"url"`
}

func (s *Server) addGrantProgram(w http.ResponseWriter, r *http.Request) {
	var input customGrantProgram
	if !decodeJSON(w, r, 16<<10, &input) {
		return
	}
	input.Name, input.Agency, input.Country, input.URL = strings.TrimSpace(input.Name), strings.TrimSpace(input.Agency), strings.TrimSpace(input.Country), strings.TrimSpace(input.URL)
	if grants.ScrubCredentials(input.Name) != input.Name || grants.ScrubCredentials(input.Agency) != input.Agency || grants.ScrubCredentials(input.Country) != input.Country {
		writeError(w, http.StatusBadRequest, errors.New("programme metadata must not contain credentials"))
		return
	}
	parsed, err := url.Parse(input.URL)
	if input.Name == "" || input.Agency == "" || input.Country == "" || err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		writeError(w, http.StatusBadRequest, errors.New("name, agency, country and public HTTPS URL are required"))
		return
	}
	raw, err := grants.FetchPublicSource(r.Context(), input.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	text, err := grants.BoundedPublicSourceText(raw, 90<<10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	program := grants.Program{ID: strings.ToLower(input.Country + ":" + input.Agency + ":" + input.Name), Name: input.Name, Agency: input.Agency, Country: input.Country, URL: input.URL, SourceText: text, VerifiedAt: time.Now().UTC().Format(time.RFC3339), Eligibility: []string{}, Sections: []grants.SectionRequirement{}, Status: "candidate"}
	grantCatalogueMu.Lock()
	defer grantCatalogueMu.Unlock()
	catalogue, err := s.loadGrantCatalogueLocked(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, existing := range catalogue.Programs {
		if existing.ID == program.ID {
			writeError(w, http.StatusConflict, errors.New("programme already exists"))
			return
		}
	}
	catalogue.Programs = append(catalogue.Programs, program)
	catalogue.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.store.SaveGrantCatalogue(r.Context(), catalogue); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, catalogue)
}

func (c *Client) GrantCatalogue(ctx context.Context) (grants.Catalogue, error) {
	var catalogue grants.Catalogue
	err := c.do(ctx, http.MethodGet, "/v1/grants/catalogue", nil, &catalogue, http.StatusOK)
	return catalogue, err
}

func (c *Client) RefreshGrantCatalogue(ctx context.Context) (grants.Catalogue, error) {
	var catalogue grants.Catalogue
	err := c.doWithTimeout(ctx, 2*time.Minute, http.MethodPost, "/v1/grants/catalogue/refresh", struct{}{}, &catalogue, http.StatusOK)
	return catalogue, err
}

func (c *Client) AddGrantProgram(ctx context.Context, program grants.Program) (grants.Catalogue, error) {
	payload := customGrantProgram{Name: program.Name, Agency: program.Agency, Country: program.Country, URL: program.URL}
	var catalogue grants.Catalogue
	err := c.doWithTimeout(ctx, 15*time.Second, http.MethodPost, "/v1/grants/programmes", payload, &catalogue, http.StatusCreated)
	return catalogue, err
}

func (c *Client) doWithTimeout(ctx context.Context, timeout time.Duration, method, path string, payload, result any, want int) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	client := *c.http
	client.Timeout = timeout
	response, err := client.Do(request)
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
	return json.NewDecoder(response.Body).Decode(result)
}
