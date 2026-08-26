package provider

import (
	"slices"
	"strings"
	"testing"
)

// requireProvider skips when the CLI is not installed, so the suite still runs
// on a machine that has only some of them.
func requireProvider(t *testing.T, name string) {
	t.Helper()
	if _, err := executable(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}

func invoke(t *testing.T, request Request) []string {
	t.Helper()
	invocation, err := ChatInvocation(request)
	if err != nil {
		t.Fatalf("invocation: %v", err)
	}
	return invocation.Command
}

func has(command []string, flag string) bool {
	return slices.Contains(command, flag)
}

// value returns the argument following flag, or "" when it is absent.
func value(command []string, flag string) string {
	for index, item := range command {
		if item == flag && index+1 < len(command) {
			return command[index+1]
		}
	}
	return ""
}

// `codex exec` and `codex exec resume` accept different options: resume has no
// --color and no --sandbox and errors out when given either.
func TestCodexResumeOmitsFlagsItRejects(t *testing.T) {
	requireProvider(t, "openai")
	command := invoke(t, Request{
		Provider: "openai", Prompt: "merhaba", Access: AccessEdit,
		SessionID: "01a03e06-e213-7332-8277-913117fef2b1",
	})
	if has(command, "--color") {
		t.Fatalf("resume was given --color, which it rejects: %v", command)
	}
	if has(command, "--sandbox") {
		t.Fatalf("resume was given --sandbox, which it rejects: %v", command)
	}
	if !has(command, "resume") {
		t.Fatalf("resume subcommand missing: %v", command)
	}
	// The session id is positional and must come before the options.
	idAt := slices.Index(command, "01a03e06-e213-7332-8277-913117fef2b1")
	jsonAt := slices.Index(command, "--json")
	if idAt == -1 || jsonAt == -1 || idAt > jsonAt {
		t.Fatalf("session id is not positioned before the options: %v", command)
	}
	if got := value(command, "-c"); got != "sandbox_mode=workspace-write" {
		t.Fatalf("sandbox override = %q: %v", got, command)
	}
}

func TestCodexResumeReadAccessStaysReadOnly(t *testing.T) {
	requireProvider(t, "openai")
	command := invoke(t, Request{
		Provider: "openai", Prompt: "merhaba", Access: AccessRead, SessionID: "abc",
	})
	if got := value(command, "-c"); got != "sandbox_mode=read-only" {
		t.Fatalf("sandbox override = %q: %v", got, command)
	}
}

// A fresh session uses the flags that only `codex exec` has.
func TestCodexFreshSessionUsesSandboxFlag(t *testing.T) {
	requireProvider(t, "openai")
	command := invoke(t, Request{Provider: "openai", Prompt: "merhaba", Access: AccessEdit})
	if has(command, "resume") {
		t.Fatalf("a fresh session should not resume: %v", command)
	}
	if got := value(command, "--sandbox"); got != "workspace-write" {
		t.Fatalf("--sandbox = %q: %v", got, command)
	}
	if got := value(command, "--color"); got != "never" {
		t.Fatalf("--color = %q: %v", got, command)
	}
	if has(command, "-c") {
		t.Fatalf("a fresh session needs no config override: %v", command)
	}
}

func TestClaudeResumesBySessionID(t *testing.T) {
	requireProvider(t, "claude")
	fresh := invoke(t, Request{Provider: "claude", Prompt: "merhaba", Access: AccessEdit})
	if has(fresh, "--resume") {
		t.Fatalf("a fresh session should not resume: %v", fresh)
	}
	if got := value(fresh, "--permission-mode"); got != "acceptEdits" {
		t.Fatalf("edit access permission mode = %q", got)
	}

	resumed := invoke(t, Request{
		Provider: "claude", Prompt: "merhaba", Access: AccessEdit, SessionID: "sid",
	})
	if got := value(resumed, "--resume"); got != "sid" {
		t.Fatalf("--resume = %q: %v", got, resumed)
	}
}

// Read access must keep every provider out of write mode.
func TestReadAccessNeverGrantsWriting(t *testing.T) {
	cases := map[string][]string{
		"claude": {"--permission-mode", "plan"},
		"openai": {"--sandbox", "read-only"},
		"gemini": {"--mode", "plan"},
	}
	for name, want := range cases {
		if _, err := executable(name); err != nil {
			continue
		}
		command := invoke(t, Request{Provider: name, Prompt: "merhaba", Access: AccessRead})
		if got := value(command, want[0]); got != want[1] {
			t.Fatalf("%s: %s = %q, want %q (%v)", name, want[0], got, want[1], command)
		}
	}
}

// Every provider must stream, so answers appear while they are produced.
func TestEveryProviderDeclaresAStreamFormat(t *testing.T) {
	for _, name := range []string{"claude", "openai", "gemini", "ollama"} {
		if _, err := executable(name); err != nil {
			continue
		}
		invocation, err := ChatInvocation(Request{Provider: name, Prompt: "merhaba"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if invocation.Stream == "" {
			t.Fatalf("%s declares no stream format", name)
		}
	}
}

// The prompt must never be pasted into a shell-style string.
func TestPromptIsNeverConcatenatedIntoOneArgument(t *testing.T) {
	requireProvider(t, "claude")
	command := invoke(t, Request{Provider: "claude", Prompt: "rm -rf / ; echo pwned"})
	for _, item := range command {
		if strings.Contains(item, ";") {
			t.Fatalf("an argument carries shell punctuation: %q", item)
		}
	}
}
