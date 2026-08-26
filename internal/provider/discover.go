package provider

import (
	"os/exec"

	"github.com/Emirfs/conclave/internal/domain"
)

type candidate struct {
	name    string
	kind    string
	aliases []string
}

var candidates = []candidate{
	{name: "claude", kind: "subscription-cli", aliases: []string{"claude", "claude.cmd"}},
	{name: "openai", kind: "subscription-cli", aliases: []string{"codex", "codex.cmd"}},
	{name: "gemini", kind: "subscription-cli", aliases: []string{"gemini", "gemini.cmd"}},
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
