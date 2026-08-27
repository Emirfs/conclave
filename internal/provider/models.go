package provider

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/Emirfs/conclave/internal/domain"
)

// catalog is what each provider CLI is offered in the UI. It is a convenience,
// not a contract: a CLI gains and loses models without this build changing, so
// a card may always be given a name that is not listed here. Nothing validates
// a model against this list.
var catalog = map[string][]string{
	"claude": {"opus", "sonnet", "haiku"},
	"openai": {"gpt-5.1-codex-max", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1"},
	"gemini": {"gemini-3-pro", "gemini-3-flash", "gemini-2.5-pro", "gemini-2.5-flash"},
}

// Models lists what a provider can be asked for. Ollama is the only provider
// that can be asked what it actually holds, so its list is read from the
// machine rather than from the catalog; the others answer from the catalog and
// leave the default to the CLI's own configuration.
func Models(ctx context.Context, name string) (domain.ProviderModels, error) {
	if name == "ollama" {
		return ollamaModels(ctx)
	}
	models, known := catalog[name]
	if !known {
		return domain.ProviderModels{Provider: name, Models: []string{}}, nil
	}
	return domain.ProviderModels{Provider: name, Models: append([]string{}, models...)}, nil
}

// ollamaModels asks the local daemon what is pulled. A missing CLI or an
// unreachable ollama is an empty list, not an error: the card can still be
// given a model name by hand.
func ollamaModels(ctx context.Context) (domain.ProviderModels, error) {
	result := domain.ProviderModels{Provider: "ollama", Models: []string{}, Default: defaultOllamaModel()}
	path, err := executable("ollama")
	if err != nil {
		return result, nil
	}
	output, err := exec.CommandContext(ctx, path, "list").Output()
	if err != nil {
		return result, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.EqualFold(fields[0], "NAME") {
			continue
		}
		result.Models = append(result.Models, fields[0])
	}
	return result, nil
}

// defaultOllamaModel mirrors ChatInvocation: the same fallback has to be shown
// in the UI as the one a card without a model actually runs on.
func defaultOllamaModel() string {
	if model := os.Getenv("CONCLAVE_OLLAMA_MODEL"); model != "" {
		return model
	}
	return "qwen3:4b"
}
