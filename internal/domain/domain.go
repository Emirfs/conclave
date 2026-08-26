package domain

import "time"

type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	StatusBlocked Status = "blocked"
)

type Provider struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	Command   string `json:"command,omitempty"`
}

type StageSpec struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

type RunRequest struct {
	Project string      `json:"project"`
	Stages  []StageSpec `json:"stages"`
}

type Stage struct {
	ID       int64    `json:"id"`
	RunID    int64    `json:"run_id"`
	Position int      `json:"position"`
	Name     string   `json:"name"`
	Command  []string `json:"command"`
	Status   Status   `json:"status"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Output   string   `json:"output,omitempty"`
}

type Run struct {
	ID        int64     `json:"id"`
	Project   string    `json:"project"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Stages    []Stage   `json:"stages,omitempty"`
}

type Snapshot struct {
	Healthy   bool       `json:"healthy"`
	Version   string     `json:"version"`
	Providers []Provider `json:"providers"`
	Runs      []Run      `json:"runs"`
	Turns     []ChatTurn `json:"turns"`
}

type ChatRequest struct {
	Prompt    string   `json:"prompt"`
	Providers []string `json:"providers"`
}

type ChatResponse struct {
	ID       int64  `json:"id"`
	TurnID   int64  `json:"turn_id"`
	Provider string `json:"provider"`
	Status   Status `json:"status"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ChatTurn struct {
	ID        int64          `json:"id"`
	Prompt    string         `json:"prompt"`
	CreatedAt time.Time      `json:"created_at"`
	Responses []ChatResponse `json:"responses"`
}

type ChatJob struct {
	ResponseID int64
	TurnID     int64
	Provider   string
	Prompt     string
}
