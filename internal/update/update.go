// Package update reports whether a newer Conclave release exists.
//
// It only ever looks; nothing here downloads or installs anything. Replacing a
// running binary is the installer's job, and doing it behind the user's back is
// not something a local tool should do.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// releasesURL is the anonymous GitHub endpoint for the newest published
// release. Anonymous requests are rate limited per IP, which is ample for a
// check made once a day.
const releasesURL = "https://api.github.com/repos/Emirfs/conclave/releases/latest"

// Status is what the daemon knows about available releases. It is a cached
// answer: the check runs on a timer, never on the request that reads it.
type Status struct {
	// Current is the running build's version.
	Current string `json:"current"`
	// Latest is the newest published release, empty until a check succeeds.
	Latest string `json:"latest,omitempty"`
	// Available reports whether Latest is newer than Current.
	Available bool `json:"available"`
	// URL is where the release can be read about and downloaded.
	URL string `json:"url,omitempty"`
	// CheckedAt is when the last successful check finished.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// Error explains why the last check failed, if it did. A machine that is
	// offline is the ordinary case, not a fault worth interrupting anyone over.
	Error string `json:"error,omitempty"`
}

// Checker holds the last known release status and refreshes it on a timer.
type Checker struct {
	current string
	client  *http.Client
	// endpoint is releasesURL outside tests; it exists so a test can answer
	// without reaching GitHub.
	endpoint string
	interval time.Duration

	mutex  sync.RWMutex
	status Status
}

// NewChecker returns a checker for the given running version.
func NewChecker(current string, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &Checker{
		current:  current,
		endpoint: releasesURL,
		interval: interval,
		// A release check must never be the reason a daemon hangs.
		client: &http.Client{Timeout: 10 * time.Second},
		status: Status{Current: current},
	}
}

// Status returns the last known answer without touching the network.
func (c *Checker) Status() Status {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.status
}

// Run checks once at startup and then on the interval, until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	c.checkOnce(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkOnce(ctx)
		}
	}
}

// CheckNow refreshes the status immediately, for a user who asks rather than
// waits.
func (c *Checker) CheckNow(ctx context.Context) Status {
	c.checkOnce(ctx)
	return c.Status()
}

func (c *Checker) checkOnce(ctx context.Context) {
	latest, url, err := c.fetchLatest(ctx)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if err != nil {
		// The previous answer is still the best one available; only the error
		// is replaced, so a transient outage does not erase a known release.
		c.status.Error = err.Error()
		return
	}
	c.status = Status{
		Current:   c.current,
		Latest:    latest,
		Available: Newer(c.current, latest),
		URL:       url,
		CheckedAt: time.Now().UTC(),
	}
}

func (c *Checker) fetchLatest(ctx context.Context) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "conclave/"+c.current)
	response, err := c.client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		// A repository with no published release yet. Not an error worth
		// showing anyone: there is simply nothing newer.
		return "", "", errors.New("no published release")
	}
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github returned %s", response.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Draft   bool   `json:"draft"`
		Pre     bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", "", err
	}
	if payload.Draft || payload.Pre {
		return "", "", errors.New("newest release is not final")
	}
	if payload.TagName == "" {
		return "", "", errors.New("release has no tag")
	}
	return strings.TrimPrefix(payload.TagName, "v"), payload.HTMLURL, nil
}

// Newer reports whether candidate is a later version than current.
//
// Versions are compared field by field as numbers, so 0.10.0 correctly beats
// 0.9.0 — the comparison a string would get wrong. Anything unparseable is
// treated as not newer: offering an update on a version nobody can read is
// worse than staying quiet.
func Newer(current, candidate string) bool {
	left, ok := parse(current)
	if !ok {
		return false
	}
	right, ok := parse(candidate)
	if !ok {
		return false
	}
	for index := range left {
		if right[index] != left[index] {
			return right[index] > left[index]
		}
	}
	return false
}

// parse reads major.minor.patch, tolerating a leading "v" and a trailing
// pre-release or build suffix.
func parse(version string) ([3]int, bool) {
	var parsed [3]int
	version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(version), "v"))
	if version == "" {
		return parsed, false
	}
	// Drop "-rc1" or "+build" so a suffixed tag still compares by its numbers.
	if cut := strings.IndexAny(version, "-+"); cut != -1 {
		version = version[:cut]
	}
	parts := strings.Split(version, ".")
	if len(parts) > 3 {
		return parsed, false
	}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return parsed, false
		}
		parsed[index] = value
	}
	return parsed, true
}
