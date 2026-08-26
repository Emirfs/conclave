package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, arguments := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		command := exec.Command("git", arguments...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func commit(t *testing.T, dir, message string) {
	t.Helper()
	for _, arguments := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", message}} {
		command := exec.Command("git", arguments...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
}

func TestProjectStatusListsModifiedAndUntracked(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "kept.txt", "bir\n")
	commit(t, dir, "ilk")
	write(t, dir, "kept.txt", "bir\niki\n")
	write(t, dir, "yeni.txt", "taze\n")

	status, err := ProjectStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Available {
		t.Fatal("a git repository was reported as unavailable")
	}
	found := map[string]Change{}
	for _, change := range status.Changes {
		found[change.Path] = change
	}
	if change, ok := found["kept.txt"]; !ok || change.Untracked {
		t.Fatalf("kept.txt = %+v", change)
	}
	if change, ok := found["yeni.txt"]; !ok || !change.Untracked {
		t.Fatalf("yeni.txt = %+v", change)
	}
}

// A card may point at a plain directory; that is not an error.
func TestProjectStatusOnNonRepository(t *testing.T) {
	status, err := ProjectStatus(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Available {
		t.Fatal("a plain directory was reported as a repository")
	}
}

func TestProjectStatusOnMissingDirectory(t *testing.T) {
	_, err := ProjectStatus(context.Background(), filepath.Join(t.TempDir(), "yok"))
	if err == nil {
		t.Fatal("a missing directory should be an error")
	}
}

func TestFileDiffShowsModification(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "kod.go", "package main\n")
	commit(t, dir, "ilk")
	write(t, dir, "kod.go", "package main\n\nfunc main() {}\n")

	diff, err := FileDiff(context.Background(), dir, "kod.go")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff.Patch, "+func main() {}") {
		t.Fatalf("patch does not show the addition:\n%s", diff.Patch)
	}
}

// An untracked file has no diff against HEAD, so it must be rendered whole.
func TestFileDiffShowsUntrackedFile(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "kept.txt", "bir\n")
	commit(t, dir, "ilk")
	write(t, dir, "yeni.txt", "taze satir\n")

	diff, err := FileDiff(context.Background(), dir, "yeni.txt")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff.Patch, "+taze satir") {
		t.Fatalf("untracked file was not rendered:\n%s", diff.Patch)
	}
}

// The client supplies the path, so escaping the project must be refused.
func TestFileDiffRefusesEscapingPaths(t *testing.T) {
	dir := gitRepo(t)
	for _, path := range []string{"../gizli.txt", "..", filepath.Join(dir, "mutlak.txt")} {
		if _, err := FileDiff(context.Background(), dir, path); err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func TestParsePorcelainHandlesRenames(t *testing.T) {
	// A rename is two records: the entry, then the old path.
	output := "R  yeni.txt\x00eski.txt\x00 M baska.txt\x00"
	changes := ParsePorcelain(output)
	if len(changes) != 2 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].Path != "yeni.txt" || !changes[0].Staged {
		t.Fatalf("rename entry = %+v", changes[0])
	}
	if changes[1].Path != "baska.txt" {
		t.Fatalf("second entry = %+v", changes[1])
	}
}
