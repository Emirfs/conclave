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
	// Quota is present only for providers that report their own allowance.
	Quota *Quota `json:"quota,omitempty"`
}

// Quota mirrors provider.Quota for transport. Utilisation is a fraction between
// 0 and 1; reset times are Unix seconds, zero when unknown.
type Quota struct {
	ShortLabel       string  `json:"short_label,omitempty"`
	ShortUtilization float64 `json:"short_utilization"`
	ShortResetsAt    int64   `json:"short_resets_at,omitempty"`
	LongLabel        string  `json:"long_label,omitempty"`
	LongUtilization  float64 `json:"long_utilization"`
	LongResetsAt     int64   `json:"long_resets_at,omitempty"`
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

// TurnRequest posts a message into an existing conversation. The providers are
// the conversation's own, so a client cannot widen the fan-out per message.
type TurnRequest struct {
	Prompt string `json:"prompt"`
}

type ChatResponse struct {
	ID       int64  `json:"id"`
	TurnID   int64  `json:"turn_id"`
	Provider string `json:"provider"`
	Status   Status `json:"status"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
	// Activity is a machine token for what the provider is doing right now,
	// empty once the response is finished. See provider.StreamUpdate.Activity.
	Activity string `json:"activity,omitempty"`
}

type ChatTurn struct {
	ID        int64          `json:"id"`
	Prompt    string         `json:"prompt"`
	CreatedAt time.Time      `json:"created_at"`
	Responses []ChatResponse `json:"responses"`
}

type ChatJob struct {
	ResponseID     int64
	TurnID         int64
	ConversationID int64
	Provider       string
	Prompt         string
}

// Conversation kinds. A solo conversation talks to exactly one provider; a
// group conversation broadcasts every turn to all of its providers.
const (
	KindSolo  = "solo"
	KindGroup = "group"
)

// Canvas node kinds.
const (
	NodeConversation = "conversation"
	NodeNote         = "note"
)

type Conversation struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Providers []string   `json:"providers"`
	CreatedAt time.Time  `json:"created_at"`
	Turns     []ChatTurn `json:"turns"`
}

// CanvasNode is presentation state the daemon owns so a layout survives a
// restart and is identical for every client.
type CanvasNode struct {
	ID             int64   `json:"id"`
	Kind           string  `json:"kind"`
	ConversationID *int64  `json:"conversation_id,omitempty"`
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	Width          float64 `json:"width"`
	Height         float64 `json:"height"`
	Z              int     `json:"z"`
	Color          string  `json:"color,omitempty"`
	Body           string  `json:"body,omitempty"`
}

// CanvasNodePatch carries only the fields a client wants to change. A nil field
// is left alone, which keeps a drag from clobbering a concurrent text edit.
type CanvasNodePatch struct {
	ID     int64    `json:"id"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`
	Z      *int     `json:"z,omitempty"`
	Color  *string  `json:"color,omitempty"`
	Body   *string  `json:"body,omitempty"`
}

type NewConversation struct {
	Title     string   `json:"title"`
	Kind      string   `json:"kind"`
	Providers []string `json:"providers"`
	X         float64  `json:"x"`
	Y         float64  `json:"y"`
}

type NewNote struct {
	Body  string  `json:"body"`
	Color string  `json:"color"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// Canvas is everything the desktop client needs to draw the board.
type Canvas struct {
	Conversations []Conversation `json:"conversations"`
	Nodes         []CanvasNode   `json:"nodes"`
}
