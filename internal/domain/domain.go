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
}
