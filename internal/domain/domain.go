package domain

import (
	"strings"
	"time"
)

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
	ID     int64  `json:"id"`
	Prompt string `json:"prompt"`
	// Kind says where the prompt came from: a person, another card's answer, or
	// the nudge that pushes a stalled exchange on. A transcript that does not
	// separate these reads as though the user wrote everything.
	Kind      string         `json:"kind,omitempty"`
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
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Kind      string     `json:"kind"`
	Providers []string   `json:"providers"`
	CreatedAt time.Time  `json:"created_at"`
	Turns     []ChatTurn `json:"turns"`
	// ProjectPath is the directory the providers run in. Empty means an
	// isolated scratch directory with no project to work on.
	ProjectPath string `json:"project_path,omitempty"`
	// Access is "read" or "edit"; see provider.Access.
	Access string `json:"access,omitempty"`
	// Loop is the card's step cycle and how it repeats.
	Loop LoopConfig `json:"loop"`
	// LoopRunning reports whether the cycle is currently armed.
	LoopRunning bool `json:"loop_running"`
	// Runs are the most recent cycle results, newest first.
	Runs []CardRun `json:"runs,omitempty"`
	// Role is what this card is supposed to do in an exchange with another
	// card. It goes into the briefing, so the two do not both wait to be led.
	Role string `json:"role,omitempty"`
	// DialogueState reports how the last exchange ended: empty while it runs,
	// DialogueDone when the work was finished, DialogueWaiting when a card
	// stopped for a decision only a person can make.
	DialogueState string `json:"dialogue_state,omitempty"`
}

// How a dialogue ended. A finished exchange and a stalled one look the same on
// the wire — both stop relaying — but only one of them wants the user back.
const (
	// DialogueDone means a card reported the work finished and verified.
	DialogueDone = "done"
	// DialogueWaiting means a card asked for a decision and got no answer, so
	// the exchange is parked rather than complete.
	DialogueWaiting = "waiting"
)

// Turn kinds. A turn is either something a person sent, an answer relayed from
// a linked card, or the nudge that pushes a stalled exchange one step further.
const (
	TurnUser  = "user"
	TurnRelay = "relay"
	TurnNudge = "nudge"
)

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

// BranchRequest forks an answer into new cards, one per provider, each starting
// from that answer.
type BranchRequest struct {
	Answer    string   `json:"answer"`
	Providers []string `json:"providers"`
}

// RoleRequest sets what a card is expected to do when it works with another.
type RoleRequest struct {
	Role string `json:"role"`
}

// Normalised trims the role and keeps it short enough to stay a role rather
// than becoming a second prompt.
func (r RoleRequest) Normalised() RoleRequest {
	r.Role = strings.TrimSpace(r.Role)
	if len(r.Role) > maxRoleBytes {
		r.Role = r.Role[:maxRoleBytes]
	}
	return r
}

const maxRoleBytes = 500

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
	UntilDone bool   `json:"until_done"`
	Briefing  string `json:"briefing,omitempty"`
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

// maxLinkRounds is the ceiling on a bounded link's round budget. Unbounded work
// requires the separate, explicit UntilDone flag and its completion protocol.
const maxLinkRounds = 12

type LinkOptions struct {
	Mode      string `json:"mode"`
	MaxRounds int    `json:"max_rounds"`
	UntilDone bool   `json:"until_done"`
	// Briefing is the shared goal, given to each card once before its first
	// relayed message rather than repeated on every hop.
	Briefing string `json:"briefing,omitempty"`
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
	if o.Mode != LinkDialogue {
		o.UntilDone = false
	}
	if len(o.Briefing) > maxBriefingBytes {
		o.Briefing = o.Briefing[:maxBriefingBytes]
	}
	return o
}

// maxBriefingBytes keeps a briefing to something a card can actually act on.
// It is prepended to a real message, so it competes with the work for room.
const maxBriefingBytes = 4000

type NewLink struct {
	SourceID  int64  `json:"source_id"`
	TargetID  int64  `json:"target_id"`
	Mode      string `json:"mode,omitempty"`
	MaxRounds int    `json:"max_rounds,omitempty"`
	UntilDone bool   `json:"until_done,omitempty"`
	Briefing  string `json:"briefing,omitempty"`
	// Pair also creates the reverse link, so the two cards answer each other.
	Pair bool `json:"pair,omitempty"`
}

// Canvas is everything the desktop client needs to draw the board.
type Canvas struct {
	Conversations []Conversation `json:"conversations"`
	Nodes         []CanvasNode   `json:"nodes"`
	Links         []CanvasLink   `json:"links"`
}

// Loop modes for a card's step list.
const (
	// LoopOff never runs the steps.
	LoopOff = "off"
	// LoopUntilPass runs after each turn and stops once every step succeeds.
	LoopUntilPass = "until_pass"
	// LoopContinuous keeps cycling regardless of the outcome. This is what a
	// hardware rig needs: flash, listen, check, wait, repeat.
	LoopContinuous = "continuous"
)

// CardStep is one command in a card's cycle. Commands are argument arrays and
// never reach a shell.
type CardStep struct {
	Name string `json:"name"`
	// Command is a command line split on whitespace, honouring quotes.
	Command string `json:"command"`
	// TimeoutSeconds bounds a step that would otherwise never exit, such as a
	// serial listener. Zero falls back to the daemon's stage timeout.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// LoopConfig is how a card's cycle is set up.
type LoopConfig struct {
	Mode            string     `json:"mode"`
	IntervalSeconds int        `json:"interval_seconds"`
	Steps           []CardStep `json:"steps"`
	// NotifyOnFailure feeds a failing step's output back to the card.
	NotifyOnFailure bool `json:"notify_on_failure"`
}

// CardRun is one completed cycle of a card's steps.
type CardRun struct {
	ID         int64  `json:"id"`
	Status     Status `json:"status"`
	StepName   string `json:"step_name,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// maxLoopSteps and the interval bounds keep a mistyped configuration from
// turning into an unbounded workload.
const (
	maxLoopSteps       = 20
	maxLoopInterval    = 3600
	maxStepTimeoutSecs = 3600
)

// Normalised clamps a loop configuration into supported ranges.
func (c LoopConfig) Normalised() LoopConfig {
	switch c.Mode {
	case LoopUntilPass, LoopContinuous:
	default:
		c.Mode = LoopOff
	}
	if c.IntervalSeconds < 0 {
		c.IntervalSeconds = 0
	}
	if c.IntervalSeconds > maxLoopInterval {
		c.IntervalSeconds = maxLoopInterval
	}
	if len(c.Steps) > maxLoopSteps {
		c.Steps = c.Steps[:maxLoopSteps]
	}
	cleaned := make([]CardStep, 0, len(c.Steps))
	for _, step := range c.Steps {
		if step.Command == "" {
			continue
		}
		if step.TimeoutSeconds < 0 {
			step.TimeoutSeconds = 0
		}
		if step.TimeoutSeconds > maxStepTimeoutSecs {
			step.TimeoutSeconds = maxStepTimeoutSecs
		}
		cleaned = append(cleaned, step)
	}
	c.Steps = cleaned
	if len(c.Steps) == 0 {
		c.Mode = LoopOff
	}
	return c
}
