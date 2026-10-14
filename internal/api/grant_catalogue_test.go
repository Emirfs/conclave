package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Emirfs/conclave/internal/grants"
)

func TestAddGrantProgramRejectsUnsafeSourceURLs(t *testing.T) {
	for _, rawURL := range []string{"http://example.org", "https://127.0.0.1", "https://user:pass@example.org"} {
		body := `{"name":"Call","agency":"Agency","country":"Country","url":"` + rawURL + `"}`
		req := httptest.NewRequest("POST", "/v1/grants/programmes", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		(&Server{}).addGrantProgram(response, req)
		if response.Code != 400 {
			t.Errorf("URL %q returned %d", rawURL, response.Code)
		}
	}
}

func TestManualProgrammeAdditionCannotPostponeSourceRefresh(t *testing.T) {
	catalogue := grants.Catalogue{UpdatedAt: time.Now().Format(time.RFC3339), RefreshedAt: time.Now().Add(-25 * time.Hour).Format(time.RFC3339)}
	if !catalogueIsStale(catalogue) {
		t.Fatal("manual programme update postponed official source refresh")
	}
	catalogue.RefreshedAt = ""
	if !catalogueIsStale(catalogue) {
		t.Fatal("never-checked official sources treated as fresh")
	}
	catalogue.RefreshedAt = time.Now().Format(time.RFC3339)
	catalogue.Sources = []grants.CatalogueSource{{Error: "source unavailable"}}
	if catalogueIsStale(catalogue) {
		t.Fatal("failed source retried before its bounded retry interval")
	}
	catalogue.RefreshedAt = time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	if !catalogueIsStale(catalogue) {
		t.Fatal("failed source was never eligible for refresh")
	}
}
