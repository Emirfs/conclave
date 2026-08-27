package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Emirfs/conclave/internal/domain"
)

// Models lists what a provider can be asked for, as that provider itself
// reports it. Codex and Antigravity both publish their catalogue, and ollama
// knows what is pulled, so a card offers the same names their own clients do
// rather than a list this build would have to be rebuilt to keep current.
//
// A CLI that cannot be asked (Claude Code) falls back to its documented model
// aliases. Nothing here is a constraint: a name that appears in no list is
// still accepted, because a provider gains models between releases.
func Models(ctx context.Context, name string) (domain.ProviderModels, error) {
	if cached, fresh := cachedModels(name); fresh {
		return cached, nil
	}
	// A CLI that hangs must not hold the request past the daemon's own write
	// timeout: an empty list the user can type past beats a dead request.
	ctx, cancel := context.WithTimeout(ctx, askBudget)
	defer cancel()
	var result domain.ProviderModels
	switch name {
	case "claude":
		result = domain.ProviderModels{Provider: name, Models: claudeAliases()}
	case "openai":
		result = codexModels(ctx)
	case "gemini":
		result = antigravityModels(ctx)
	case "ollama":
		result = ollamaModels(ctx)
	default:
		return domain.ProviderModels{Provider: name, Models: []domain.Model{}}, nil
	}
	result.Provider = name
	if result.Models == nil {
		result.Models = []domain.Model{}
	}
	rememberModels(name, result)
	return result, nil
}

// Asking a CLI costs a process launch, and one of them goes to the network for
// its answer. The list is opened far more often than it changes.
const modelsTTL = 10 * time.Minute

// askBudget stays under the daemon's 10s write timeout, so a slow CLI ends as
// an empty list rather than a request the client never gets an answer to.
const askBudget = 8 * time.Second

var (
	modelsMutex sync.Mutex
	modelsSeen  = map[string]modelsEntry{}
)

type modelsEntry struct {
	value domain.ProviderModels
	at    time.Time
}

func cachedModels(name string) (domain.ProviderModels, bool) {
	modelsMutex.Lock()
	defer modelsMutex.Unlock()
	entry, known := modelsSeen[name]
	if !known || time.Since(entry.at) > modelsTTL {
		return domain.ProviderModels{}, false
	}
	return entry.value, true
}

// rememberModels only caches an answer worth repeating. An empty list usually
// means the CLI was busy, logged out or missing, and that must not be held on
// to for ten minutes.
func rememberModels(name string, value domain.ProviderModels) {
	if len(value.Models) == 0 {
		return
	}
	modelsMutex.Lock()
	defer modelsMutex.Unlock()
	modelsSeen[name] = modelsEntry{value: value, at: time.Now()}
}

// claudeAliases are the aliases `claude --help` documents for --model. Claude
// Code has no command that lists models, so this is the one provider whose list
// this build carries; a full name still works when typed in.
func claudeAliases() []domain.Model {
	return []domain.Model{
		{ID: "fable", Label: "Fable"},
		{ID: "opus", Label: "Opus"},
		{ID: "sonnet", Label: "Sonnet"},
		{ID: "haiku", Label: "Haiku"},
	}
}

// codexModels reads the catalogue codex renders for itself. The entries it
// hides from its own picker are hidden here too, and its priority ordering is
// kept, so the list reads the way it does inside codex.
func codexModels(ctx context.Context) domain.ProviderModels {
	result := domain.ProviderModels{Models: []domain.Model{}}
	path, err := executable("openai")
	if err != nil {
		return result
	}
	output, err := exec.CommandContext(ctx, path, "debug", "models").Output()
	if err != nil {
		return result
	}
	result.Models = parseCodexCatalogue(output)
	return result
}

// parseCodexCatalogue keeps the entries codex shows in its own picker, in its
// own order, and drops the ones it hides.
func parseCodexCatalogue(output []byte) []domain.Model {
	models := []domain.Model{}
	var catalogue struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
		} `json:"models"`
	}
	if err := json.Unmarshal(output, &catalogue); err != nil {
		return models
	}
	listed := catalogue.Models[:0]
	for _, model := range catalogue.Models {
		if model.Visibility == "list" && model.Slug != "" {
			listed = append(listed, model)
		}
	}
	sort.SliceStable(listed, func(first, second int) bool {
		return listed[first].Priority < listed[second].Priority
	})
	for _, model := range listed {
		models = append(models, domain.Model{ID: model.Slug, Label: model.DisplayName})
	}
	return models
}

// antigravityModels reads `agy models`, which prints one tab-separated
// "id<TAB>label" per line under a progress line it writes first.
func antigravityModels(ctx context.Context) domain.ProviderModels {
	result := domain.ProviderModels{Models: []domain.Model{}}
	path, err := executable("gemini")
	if err != nil {
		return result
	}
	output, err := exec.CommandContext(ctx, path, "models").Output()
	if err != nil {
		return result
	}
	result.Models = parseAntigravityModels(string(output))
	return result
}

// parseAntigravityModels reads the "id<TAB>label" lines and ignores every other
// line, which is how the progress line agy writes first is skipped.
func parseAntigravityModels(output string) []domain.Model {
	models := []domain.Model{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		id, label, tabbed := strings.Cut(scanner.Text(), "\t")
		if !tabbed {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		models = append(models, domain.Model{ID: id, Label: strings.TrimSpace(label)})
	}
	return models
}

// ollamaModels asks the local daemon what is pulled. A missing CLI or an
// unreachable ollama is an empty list, not an error: the card can still be
// given a model name by hand.
func ollamaModels(ctx context.Context) domain.ProviderModels {
	result := domain.ProviderModels{Models: []domain.Model{}, Default: defaultOllamaModel()}
	path, err := executable("ollama")
	if err != nil {
		return result
	}
	output, err := exec.CommandContext(ctx, path, "list").Output()
	if err != nil {
		return result
	}
	result.Models = parseOllamaList(string(output))
	return result
}

// parseOllamaList takes the first column of the table ollama prints, minus its
// header row.
func parseOllamaList(output string) []domain.Model {
	models := []domain.Model{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.EqualFold(fields[0], "NAME") {
			continue
		}
		models = append(models, domain.Model{ID: fields[0]})
	}
	return models
}

// defaultOllamaModel mirrors ChatInvocation: the same fallback has to be shown
// in the UI as the one a card without a model actually runs on.
func defaultOllamaModel() string {
	if model := os.Getenv("CONCLAVE_OLLAMA_MODEL"); model != "" {
		return model
	}
	return "qwen3:4b"
}
