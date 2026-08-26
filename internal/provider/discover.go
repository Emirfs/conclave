package provider

import (
	"errors"
	"os"
	"os/exec"

	"github.com/Emirfs/conclave/internal/domain"
)

type candidate struct {
	name    string
	kind    string
	aliases []string
}

type Invocation struct {
	Command []string
	Stdin   string
	// Stream says how the daemon should read stdout. JSON formats let an answer
	// be shown while it is still being written.
	Stream StreamFormat
}

var candidates = []candidate{
	{name: "claude", kind: "subscription-cli", aliases: []string{"claude", "claude.cmd"}},
	{name: "openai", kind: "subscription-cli", aliases: []string{"codex", "codex.cmd"}},
	{name: "gemini", kind: "subscription-cli", aliases: []string{"agy", "agy.exe"}},
	{name: "ollama", kind: "local", aliases: []string{"ollama", "ollama.exe"}},
	{name: "mnemo", kind: "memory", aliases: []string{"mnemo", "mnemo.exe"}},
}

func Discover() []domain.Provider {
	providers := make([]domain.Provider, 0, len(candidates))
	for _, item := range candidates {
		provider := domain.Provider{Name: item.name, Kind: item.kind}
		for _, alias := range item.aliases {
			path, err := exec.LookPath(alias)
			if err == nil {
				provider.Available = true
				provider.Command = path
				break
			}
		}
		providers = append(providers, provider)
	}
	return providers
}

func ChatInvocation(name, prompt string) (Invocation, error) {
	path, err := executable(name)
	if err != nil {
		return Invocation{}, err
	}
	switch name {
	case "claude":
		return Invocation{
			Command: []string{path, "--print", "--output-format", "stream-json", "--include-partial-messages", "--verbose", "--permission-mode", "plan", "--tools", "", "--safe-mode", "--no-session-persistence"},
			Stdin:   prompt,
			Stream:  StreamClaude,
		}, nil
	case "openai":
		return Invocation{
			Command: []string{path, "exec", "--json", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check", "--color", "never", "-"},
			Stdin:   prompt,
			Stream:  StreamCodex,
		}, nil
	case "gemini":
		return Invocation{
			Command: []string{path, "--print", prompt, "--mode", "plan", "--output-format", "stream-json"},
			Stream:  StreamAntigravity,
		}, nil
	case "ollama":
		model := os.Getenv("CONCLAVE_OLLAMA_MODEL")
		if model == "" {
			model = "qwen3:4b"
		}
		// ollama writes nothing to stdout until it finishes when stdout is not a
		// terminal, so there is no progress to report for this provider.
		return Invocation{
			Command: []string{path, "run", model, "--hidethinking"},
			Stdin:   prompt,
			Stream:  StreamPlain,
		}, nil
	default:
		return Invocation{}, errors.New("provider does not support chat")
	}
}

func executable(name string) (string, error) {
	for _, item := range candidates {
		if item.name != name || item.kind == "memory" {
			continue
		}
		for _, alias := range item.aliases {
			if path, err := exec.LookPath(alias); err == nil {
				return path, nil
			}
		}
		return "", errors.New("provider CLI is not available")
	}
	return "", errors.New("unknown provider")
}
