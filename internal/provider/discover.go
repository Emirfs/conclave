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

// Access says how much the provider may do in the project directory.
type Access string

const (
	// AccessRead lets a provider look at the project but change nothing.
	AccessRead Access = "read"
	// AccessEdit lets a provider work as it does in its own terminal: read,
	// edit files and run commands inside the project, without stopping to ask.
	// Non-interactive runs cannot prompt, so this necessarily auto-approves.
	AccessEdit Access = "edit"
)

// Request is everything a provider needs for one turn.
type Request struct {
	Provider string
	Prompt   string
	Access   Access
	// SessionID continues a provider-side conversation when it is already known.
	SessionID string
	Model     string
}

// ChatInvocation builds the command line for one turn.
func ChatInvocation(request Request) (Invocation, error) {
	path, err := executable(request.Provider)
	if err != nil {
		return Invocation{}, err
	}
	edit := request.Access == AccessEdit
	switch request.Provider {
	case "claude":
		command := []string{path, "--print", "--output-format", "stream-json",
			"--include-partial-messages", "--verbose"}
		if edit {
			// Matches a normal session that accepts its own edits.
			command = append(command, "--permission-mode", "acceptEdits")
		} else {
			command = append(command, "--permission-mode", "plan", "--tools", "")
		}
		if request.Model != "" {
			command = append(command, "--model", request.Model)
		}
		if request.SessionID != "" {
			command = append(command, "--resume", request.SessionID)
		}
		return Invocation{Command: command, Stdin: request.Prompt, Stream: StreamClaude}, nil

	case "openai":
		sandbox := "read-only"
		if edit {
			sandbox = "workspace-write"
		}
		// "exec" and "exec resume" do not take the same options: resume has
		// neither --color nor --sandbox, and rejects them outright. The sandbox
		// is set through a config override there instead.
		var command []string
		if request.SessionID != "" {
			command = []string{path, "exec", "resume", request.SessionID,
				"--json", "--skip-git-repo-check", "-c", "sandbox_mode=" + sandbox}
		} else {
			command = []string{path, "exec",
				"--json", "--skip-git-repo-check", "--color", "never", "--sandbox", sandbox}
		}
		if request.Model != "" {
			command = append(command, "--model", request.Model)
		}
		// A lone dash makes codex read the prompt from stdin.
		command = append(command, "-")
		return Invocation{Command: command, Stdin: request.Prompt, Stream: StreamCodex}, nil

	case "gemini":
		command := []string{path, "--print", request.Prompt, "--output-format", "stream-json"}
		if edit {
			command = append(command, "--mode", "accept-edits")
		} else {
			command = append(command, "--mode", "plan")
		}
		if request.Model != "" {
			command = append(command, "--model", request.Model)
		}
		if request.SessionID != "" {
			command = append(command, "--conversation", request.SessionID)
		}
		return Invocation{Command: command, Stream: StreamAntigravity}, nil

	case "ollama":
		model := request.Model
		if model == "" {
			model = os.Getenv("CONCLAVE_OLLAMA_MODEL")
		}
		if model == "" {
			model = "qwen3:4b"
		}
		// ollama writes nothing to stdout until it finishes when stdout is not a
		// terminal, so there is no progress to report for this provider.
		return Invocation{
			Command: []string{path, "run", model, "--hidethinking"},
			Stdin:   request.Prompt,
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
