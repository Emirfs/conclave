package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// String comparison gets 0.10.0 against 0.9.0 wrong, which is exactly the
// version pair a project reaches on its tenth release.
func TestNewerComparesNumbersNotText(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.9.0", "0.10.0", true},
		{"0.10.0", "0.9.0", false},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "0.9.9", false},
		{"0.1.0", "v0.1.1", true},
		{"0.1", "0.1.1", true},
		{"1.2.3", "1.2.4-rc1", true},
		// Anything unreadable must never present itself as an update.
		{"0.1.0", "sonraki", false},
		{"gelistirme", "0.2.0", false},
		{"0.1.0", "", false},
	}
	for _, item := range cases {
		if got := Newer(item.current, item.candidate); got != item.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", item.current, item.candidate, got, item.want)
		}
	}
}

func TestCheckReportsAvailableRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"tag_name":"v0.4.0","html_url":"https://example.test/r/0.4.0"}`))
	}))
	defer server.Close()

	checker := checkerFor(t, "0.1.0", server.URL)
	status := checker.CheckNow(context.Background())
	if !status.Available {
		t.Fatalf("status did not report the newer release: %+v", status)
	}
	if status.Latest != "0.4.0" {
		t.Fatalf("latest = %q, want 0.4.0", status.Latest)
	}
	if status.URL != "https://example.test/r/0.4.0" {
		t.Fatalf("url = %q", status.URL)
	}
	if status.Error != "" {
		t.Fatalf("unexpected error: %q", status.Error)
	}
}

func TestCheckOnTheNewestVersionOffersNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"tag_name":"v0.1.0","html_url":"https://example.test/r/0.1.0"}`))
	}))
	defer server.Close()

	status := checkerFor(t, "0.1.0", server.URL).CheckNow(context.Background())
	if status.Available {
		t.Fatalf("an update was offered on the current version: %+v", status)
	}
}

// Drafts and pre-releases are not what a user running the stable build wants
// to be told about.
func TestCheckIgnoresDraftsAndPreReleases(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v9.0.0","draft":true}`,
		`{"tag_name":"v9.0.0","prerelease":true}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write([]byte(body))
		}))
		status := checkerFor(t, "0.1.0", server.URL).CheckNow(context.Background())
		server.Close()
		if status.Available {
			t.Fatalf("%s was offered as an update", body)
		}
	}
}

// A machine with no network is the ordinary case, not a fault. The last known
// answer has to survive it.
func TestFailedCheckKeepsTheLastKnownRelease(t *testing.T) {
	var fail bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if fail {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = response.Write([]byte(`{"tag_name":"v0.5.0","html_url":"https://example.test/r"}`))
	}))
	defer server.Close()

	checker := checkerFor(t, "0.1.0", server.URL)
	if status := checker.CheckNow(context.Background()); !status.Available {
		t.Fatalf("first check did not find the release: %+v", status)
	}
	fail = true
	status := checker.CheckNow(context.Background())
	if status.Latest != "0.5.0" || !status.Available {
		t.Fatalf("a failed check erased the known release: %+v", status)
	}
	if status.Error == "" {
		t.Fatal("the failure was not reported at all")
	}
}

// A repository with no releases yet must not look like an error the user has
// to do something about.
func TestRepositoryWithoutReleasesOffersNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	status := checkerFor(t, "0.1.0", server.URL).CheckNow(context.Background())
	if status.Available || status.Latest != "" {
		t.Fatalf("an update was invented out of nothing: %+v", status)
	}
}

// checkerFor builds a Checker pointed at a test server instead of GitHub.
func checkerFor(t *testing.T, current, url string) *Checker {
	t.Helper()
	checker := NewChecker(current, time.Hour)
	checker.endpoint = url
	return checker
}
