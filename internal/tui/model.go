package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Emirfs/conclave/internal/api"
	"github.com/Emirfs/conclave/internal/domain"
)

var (
	canvas    = lipgloss.NewStyle().Background(lipgloss.Color("#0B0D12")).Foreground(lipgloss.Color("#D7DBE8"))
	panel     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#343A4A")).Padding(0, 1)
	active    = panel.BorderForeground(lipgloss.Color("#8CB4FF"))
	brand     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#B9F27C"))
	muted     = lipgloss.NewStyle().Foreground(lipgloss.Color("#737B91"))
	good      = lipgloss.NewStyle().Foreground(lipgloss.Color("#76D6A3"))
	bad       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A90"))
	highlight = lipgloss.NewStyle().Foreground(lipgloss.Color("#8CB4FF"))
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F4C95D"))
)

type snapshotMessage struct {
	snapshot domain.Snapshot
	err      error
}

type sendMessage struct {
	prompt string
	err    error
}
type tickMessage time.Time

type Model struct {
	client         *api.Client
	snapshot       domain.Snapshot
	err            error
	width          int
	height         int
	loading        bool
	input          textinput.Model
	inputFocused   bool
	selected       map[string]bool
	providerCursor int
	sending        bool
}

func New(client *api.Client) Model {
	input := textinput.New()
	input.Placeholder = "Ask the selected providers..."
	input.Prompt = "> "
	input.CharLimit = 20_000
	input.Width = 60
	input.Focus()
	return Model{
		client: client, loading: true, input: input, inputFocused: true,
		selected: make(map[string]bool),
	}
}

func (m Model) Init() tea.Cmd { return tea.Batch(textinput.Blink, m.refresh(), tick()) }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case tea.KeyMsg:
		if value.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.inputFocused {
			switch value.String() {
			case "esc":
				m.inputFocused = false
				m.input.Blur()
				return m, nil
			case "enter":
				if m.sending {
					return m, nil
				}
				prompt := strings.TrimSpace(m.input.Value())
				providers := m.selectedProviders()
				if prompt == "" {
					return m, nil
				}
				if len(providers) == 0 {
					m.err = fmt.Errorf("select at least one available provider")
					return m, nil
				}
				m.err = nil
				m.sending = true
				return m, m.send(prompt, providers)
			}
			var command tea.Cmd
			m.input, command = m.input.Update(value)
			return m, command
		}
		switch value.String() {
		case "q":
			return m, tea.Quit
		case "i", "enter", "tab":
			m.inputFocused = true
			return m, m.input.Focus()
		case "up", "k":
			m.providerCursor = max(0, m.providerCursor-1)
		case "down", "j":
			m.providerCursor = min(max(0, len(m.chatProviders())-1), m.providerCursor+1)
		case " ":
			providers := m.chatProviders()
			if m.providerCursor >= 0 && m.providerCursor < len(providers) && providers[m.providerCursor].Available {
				name := providers[m.providerCursor].Name
				m.selected[name] = !m.selected[name]
			}
		case "r":
			if !m.loading {
				m.loading = true
				return m, m.refresh()
			}
		}
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
		m.input.Width = max(20, value.Width-8)
	case snapshotMessage:
		m.loading = false
		m.err = value.err
		if value.err == nil {
			m.snapshot = value.snapshot
			m.providerCursor = min(m.providerCursor, max(0, len(m.chatProviders())-1))
			for _, item := range m.chatProviders() {
				if _, known := m.selected[item.Name]; !known {
					m.selected[item.Name] = item.Available
				}
			}
		}
	case sendMessage:
		m.sending = false
		m.err = value.err
		if value.err == nil && strings.TrimSpace(m.input.Value()) == value.prompt {
			m.input.SetValue("")
		}
		if !m.loading {
			m.loading = true
			return m, m.refresh()
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
	height := max(m.height, 24)
	header := brand.Render("CONCLAVE") + "  " + muted.Render("parallel AI conversation desk")
	status := good.Render("● daemon online")
	if m.err != nil {
		status = bad.Render("● " + truncate(m.err.Error(), 60))
	}
	header = lipgloss.JoinHorizontal(lipgloss.Top, header,
		strings.Repeat(" ", max(1, width-lipgloss.Width(header)-lipgloss.Width(status)-2)), status)

	leftWidth := max(24, width/4-2)
	rightWidth := max(50, width-leftWidth-5)
	contentHeight := max(12, height-9)
	providerStyle := panel
	if !m.inputFocused {
		providerStyle = active
	}
	providers := providerStyle.Width(leftWidth).Height(contentHeight).Render(
		highlight.Render("PROVIDERS") + "\n\n" + m.providerRows() +
			"\n\n" + highlight.Render("PIPELINES") + "\n" + m.pipelineSummary())
	chat := panel.Width(rightWidth).Height(contentHeight).Render(
		highlight.Render("CONVERSATION") + "\n\n" + m.chatRows(rightWidth-4, contentHeight-3))
	body := lipgloss.JoinHorizontal(lipgloss.Top, providers, "  ", chat)

	inputStyle := panel.Width(width - 4)
	if m.inputFocused {
		inputStyle = active.Width(width - 4)
	}
	input := inputStyle.Render(m.input.View())
	footer := muted.Render("Enter send   Esc providers   ↑/↓ select   Space toggle   i write   Ctrl+C quit")
	return canvas.Width(width).Height(height).Render(header + "\n" + body + "\n" + input + "\n" + footer)
}

func (m Model) providerRows() string {
	providers := m.chatProviders()
	if len(providers) == 0 {
		return muted.Render("No AI providers detected")
	}
	rows := make([]string, 0, len(providers))
	for index, item := range providers {
		cursor := "  "
		if !m.inputFocused && index == m.providerCursor {
			cursor = "> "
		}
		checked := "[ ]"
		if m.selected[item.Name] {
			checked = "[x]"
		}
		state := bad.Render("offline")
		if item.Available {
			state = good.Render("ready")
		}
		rows = append(rows, fmt.Sprintf("%s%s %-8s %s", cursor, checked, item.Name, state))
	}
	return strings.Join(rows, "\n")
}

func (m Model) pipelineSummary() string {
	if len(m.snapshot.Runs) == 0 {
		return muted.Render("No active pipelines")
	}
	rows := make([]string, 0, min(3, len(m.snapshot.Runs)))
	for _, run := range m.snapshot.Runs[:min(3, len(m.snapshot.Runs))] {
		rows = append(rows, fmt.Sprintf("#%d %s", run.ID, run.Status))
	}
	return muted.Render(strings.Join(rows, "\n"))
}

func (m Model) chatRows(width, height int) string {
	if len(m.snapshot.Turns) == 0 {
		return muted.Render("Write a message below. Selected providers answer in parallel.")
	}
	var blocks []string
	limit := min(6, len(m.snapshot.Turns))
	for index := limit - 1; index >= 0; index-- {
		turn := m.snapshot.Turns[index]
		blocks = append(blocks, userStyle.Render("YOU")+"\n"+wrap(truncate(turn.Prompt, 1200), width))
		for _, response := range turn.Responses {
			label := strings.ToUpper(response.Provider) + "  " + string(response.Status)
			body := response.Content
			if response.Error != "" {
				body = bad.Render(response.Error)
			} else if body == "" {
				body = muted.Render("Working...")
			}
			blocks = append(blocks, highlight.Render(label)+"\n"+wrap(truncate(body, 2400), width))
		}
	}
	lines := strings.Split(strings.Join(blocks, "\n\n"), "\n")
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	return strings.Join(lines, "\n")
}

func (m Model) chatProviders() []domain.Provider {
	providers := make([]domain.Provider, 0, 4)
	for _, item := range m.snapshot.Providers {
		if item.Kind != "memory" {
			providers = append(providers, item)
		}
	}
	return providers
}

func (m Model) selectedProviders() []string {
	var selected []string
	for _, item := range m.chatProviders() {
		if item.Available && m.selected[item.Name] {
			selected = append(selected, item.Name)
		}
	}
	return selected
}

func (m Model) refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		snapshot, err := m.client.Snapshot(ctx)
		return snapshotMessage{snapshot: snapshot, err: err}
	}
}

func (m Model) send(prompt string, providers []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := m.client.CreateChatTurn(ctx, domain.ChatRequest{Prompt: prompt, Providers: providers})
		return sendMessage{prompt: prompt, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg { return tickMessage(now) })
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n[truncated]"
}

func wrap(value string, width int) string {
	return lipgloss.NewStyle().Width(max(10, width)).Render(value)
}
