package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Emirfs/conclave/internal/api"
	"github.com/Emirfs/conclave/internal/domain"
)

var (
	canvas    = lipgloss.NewStyle().Background(lipgloss.Color("#0B0D12")).Foreground(lipgloss.Color("#D7DBE8"))
	panel     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#343A4A")).Padding(0, 1)
	brand     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#B9F27C"))
	muted     = lipgloss.NewStyle().Foreground(lipgloss.Color("#737B91"))
	good      = lipgloss.NewStyle().Foreground(lipgloss.Color("#76D6A3"))
	bad       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A90"))
	highlight = lipgloss.NewStyle().Foreground(lipgloss.Color("#8CB4FF"))
)

type snapshotMessage struct {
	snapshot domain.Snapshot
	err      error
}

type tickMessage time.Time

type Model struct {
	client   *api.Client
	snapshot domain.Snapshot
	err      error
	width    int
	height   int
	loading  bool
}

func New(client *api.Client) Model { return Model{client: client, loading: true} }

func (m Model) Init() tea.Cmd { return tea.Batch(m.refresh(), tick()) }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case tea.KeyMsg:
		switch value.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.loading {
				return m, nil
			}
			m.loading = true
			return m, m.refresh()
		}
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
	case snapshotMessage:
		m.loading = false
		m.err = value.err
		if value.err == nil {
			m.snapshot = value.snapshot
		}
	case tickMessage:
		if m.loading {
			return m, tick()
		}
		m.loading = true
		return m, tea.Batch(m.refresh(), tick())
	}
	return m, nil
}

func (m Model) View() string {
	width := max(m.width, 80)
	header := brand.Render("CONCLAVE") + "  " + muted.Render("local agent operations desk")
	status := good.Render("● daemon online")
	if m.err != nil {
		status = bad.Render("● daemon offline") + "  " + muted.Render(m.err.Error())
	}
	header = lipgloss.JoinHorizontal(lipgloss.Top, header, strings.Repeat(" ", max(1, width-lipgloss.Width(header)-lipgloss.Width(status)-2)), status)

	leftWidth := max(25, width/3-3)
	rightWidth := max(45, width-leftWidth-5)
	providers := panel.Width(leftWidth).Render(highlight.Render("PROVIDERS") + "\n\n" + m.providerRows())
	runs := panel.Width(rightWidth).Render(highlight.Render("PIPELINES") + "\n\n" + m.runRows())
	body := lipgloss.JoinHorizontal(lipgloss.Top, providers, "  ", runs)
	footer := muted.Render("r refresh   q detach   daemon keeps working")
	return canvas.Width(width).Height(max(m.height, 20)).Render(header + "\n\n" + body + "\n\n" + footer)
}

func (m Model) providerRows() string {
	if len(m.snapshot.Providers) == 0 {
		return muted.Render("No provider data")
	}
	rows := make([]string, 0, len(m.snapshot.Providers))
	for _, provider := range m.snapshot.Providers {
		state := bad.Render("offline")
		if provider.Available {
			state = good.Render("ready")
		}
		rows = append(rows, fmt.Sprintf("%-10s %s\n%s", provider.Name, state, muted.Render(provider.Kind)))
	}
	return strings.Join(rows, "\n\n")
}

func (m Model) runRows() string {
	if len(m.snapshot.Runs) == 0 {
		return muted.Render("No pipelines yet. Submit one with `conclave run`.")
	}
	rows := make([]string, 0, len(m.snapshot.Runs))
	for _, run := range m.snapshot.Runs {
		rows = append(rows, fmt.Sprintf("#%-4d %-9s  %s\n%s", run.ID, run.Status, filepathBase(run.Project), stageLine(run.Stages)))
	}
	return strings.Join(rows, "\n\n")
}

func (m Model) refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		snapshot, err := m.client.Snapshot(ctx)
		return snapshotMessage{snapshot: snapshot, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(now time.Time) tea.Msg { return tickMessage(now) })
}

func stageLine(stages []domain.Stage) string {
	parts := make([]string, 0, len(stages))
	for _, stage := range stages {
		parts = append(parts, fmt.Sprintf("%s:%s", stage.Name, stage.Status))
	}
	return muted.Render(strings.Join(parts, "  "))
}

func filepathBase(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}
