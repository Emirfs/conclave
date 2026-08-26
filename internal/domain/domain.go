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
	ProjectPath    string
	Access         string
	SessionID      string
	Model          string
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
	// ProjectPath is the directory the providers run in. Empty means an
	// isolated scratch directory with no project to work on.
	ProjectPath string `json:"project_path,omitempty"`
	// Access is "read" or "edit"; see provider.Access.
	Access string `json:"access,omitempty"`
	// TestCommand runs in the project after every turn. Empty disables the
	// loop. TestRounds bounds how many times a failure is fed back.
	TestCommand string `json:"test_command,omitempty"`
	TestRounds  int    `json:"test_rounds,omitempty"`
}

// TestLoopRequest configures a card's after-each-turn command.
type TestLoopRequest struct {
	Command string `json:"command"`
	Rounds  int    `json:"rounds"`
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
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Providers   []string `json:"providers"`
	ProjectPath string   `json:"project_path,omitempty"`
	Access      string   `json:"access,omitempty"`
	X           float64  `json:"x"`
	Y           float64  `json:"y"`
}

// ProjectRequest repoints a card at a directory and access level.
type ProjectRequest struct {
	ProjectPath string `json:"project_path"`
	Access      string `json:"access"`
}

type NewNote struct {
	Body  string  `json:"body"`
	Color string  `json:"color"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// CanvasLink relays a card's finished answer into another card as its next
// message, which is how two providers hold a conversation with each other.
type CanvasLink struct {
	ID        int64  `json:"id"`
	SourceID  int64  `json:"source_id"`
	TargetID  int64  `json:"target_id"`
	Mode      string `json:"mode"`
	MaxRounds int    `json:"max_rounds"`
}

// Link modes decide how a relayed answer is presented to the receiving card.
const (
	// LinkRelay hands the answer over verbatim: a plain handoff.
	LinkRelay = "relay"
	// LinkDialogue frames it as the other card speaking, inviting a reply. Use
	// it in both directions to make two cards converse.
	LinkDialogue = "dialogue"
	// LinkReview asks the receiving card to critique what it was given.
	LinkReview = "review"
)

// maxLinkRounds is the ceiling on any link's round budget, so a mistyped value
// cannot turn a pair of cards into an unbounded loop.
const maxLinkRounds = 12

type LinkOptions struct {
	Mode      string `json:"mode"`
	MaxRounds int    `json:"max_rounds"`
}

// Normalised fills in defaults and clamps the round budget.
func (o LinkOptions) Normalised() LinkOptions {
	switch o.Mode {
	case LinkRelay, LinkDialogue, LinkReview:
	default:
		o.Mode = LinkRelay
	}
	if o.MaxRounds < 1 {
		o.MaxRounds = 3
	}
	if o.MaxRounds > maxLinkRounds {
		o.MaxRounds = maxLinkRounds
	}
	return o
}

type NewLink struct {
	SourceID int64  `json:"source_id"`
	TargetID int64  `json:"target_id"`
	Mode     string `json:"mode,omitempty"`
	MaxRounds int   `json:"max_rounds,omitempty"`
	// Pair also creates the reverse link, so the two cards answer each other.
	Pair bool `json:"pair,omitempty"`
}

// Canvas is everything the desktop client needs to draw the board.
type Canvas struct {
	Conversations []Conversation `json:"conversations"`
	Nodes         []CanvasNode   `json:"nodes"`
	Links         []CanvasLink   `json:"links"`
}
