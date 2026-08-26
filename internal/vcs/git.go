// Package vcs reports what has changed in a project working tree. It shells out
// to git with argument arrays, never through a shell, and only ever reads.
package vcs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxDiffBytes caps a single diff so one enormous file cannot flood a client.
const maxDiffBytes = 512 * 1024

const commandTimeout = 20 * time.Second

// Change is one path with pending modifications.
type Change struct {
	Path string `json:"path"`
	// Status is the two-letter porcelain code, e.g. " M", "??", "A ".
	Status string `json:"status"`
	// Staged and Untracked are derived from Status for the client's convenience.
	Staged    bool `json:"staged"`
	Untracked bool `json:"untracked"`
}

// Status is the change set of one project.
type Status struct {
	Project string   `json:"project"`
	Branch  string   `json:"branch,omitempty"`
	Changes []Change `json:"changes"`
	// Available is false when the directory is not a git repository.
	Available bool `json:"available"`
}

// Diff is the unified diff of a single path.
type Diff struct {
	Path      string `json:"path"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

func run(ctx context.Context, dir string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.String(), errors.New(message)
	}
	return stdout.String(), nil
}

// ProjectStatus lists the pending changes of a working tree. A directory that
// is not a repository is reported as unavailable rather than as an error: not
// every project a card points at is under version control.
func ProjectStatus(ctx context.Context, project string) (Status, error) {
	status := Status{Project: project, Changes: []Change{}}
	if project == "" {
		return status, nil
	}
	info, err := os.Stat(project)
	if err != nil || !info.IsDir() {
		return status, errors.New("project directory is not available")
	}
	if _, err := run(ctx, project, "rev-parse", "--is-inside-work-tree"); err != nil {
		return status, nil
	}
	status.Available = true
	if branch, err := run(ctx, project, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		status.Branch = strings.TrimSpace(branch)
	}
	// -z keeps paths intact when they contain spaces or non-ASCII bytes.
	output, err := run(ctx, project, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return status, err
	}
	status.Changes = ParsePorcelain(output)
	return status, nil
}

// ParsePorcelain reads NUL-separated `git status --porcelain -z` records.
// Renames occupy two records: the entry itself and then its old path.
func ParsePorcelain(output string) []Change {
	changes := []Change{}
	fields := strings.Split(output, "\x00")
	for index := 0; index < len(fields); index++ {
		entry := fields[index]
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		path := entry[3:]
		if code[0] == 'R' || code[0] == 'C' {
			// Skip the trailing old-path record that belongs to this entry.
			index++
		}
		changes = append(changes, Change{
			Path:      path,
			Status:    code,
			Staged:    code[0] != ' ' && code[0] != '?',
			Untracked: code == "??",
		})
	}
	return changes
}

// FileDiff returns the unified diff for one path. An untracked file has no
// diff against HEAD, so its contents are rendered as one addition instead.
func FileDiff(ctx context.Context, project, path string) (Diff, error) {
	diff := Diff{Path: path}
	if project == "" || path == "" {
		return diff, errors.New("project and path are required")
	}
	// Refuse to look outside the project, whatever the client asked for.
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return diff, errors.New("path must be inside the project")
	}

	patch, err := run(ctx, project, "diff", "HEAD", "--", clean)
	if err != nil {
		return diff, err
	}
	if strings.TrimSpace(patch) == "" {
		// git diff --no-index exits non-zero whenever it finds a difference,
		// which here is the normal case, so its output is what matters.
		patch, _ = run(ctx, project, "diff", "--no-index", "--", os.DevNull, clean)
	}
	if len(patch) > maxDiffBytes {
		patch = patch[:maxDiffBytes]
		diff.Truncated = true
	}
	diff.Patch = patch
	return diff, nil
}
