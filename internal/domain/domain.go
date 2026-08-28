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
	// StatusCanceled is a run a person stopped. It is a finished state like
	// any other, so a canceled turn never blocks a relay waiting for it.
	StatusCanceled Status = "canceled"
)

// UsageReport is what the board has spent, per provider, over a window. It is
// the addable counterpart to Quota: a provider's own allowance report says how
// full a window is right now, while this says what was actually done.
type UsageReport struct {
	// Days is the window the totals cover, counted back from now.
	Days      int             `json:"days"`
	Providers []ProviderUsage `json:"providers"`
}

type ProviderUsage struct {
	Provider string `json:"provider"`
	// Turns is how many responses this provider produced in the window,
	// finished or failed.
	Turns        int `json:"turns"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// Cards is how many different cards the provider worked on.
	Cards int `json:"cards"`
	// Quota is the provider's own last allowance report, when it offers one.
	Quota *Quota `json:"quota,omitempty"`
}

// BoardExportVersion is the shape of an exported board. It is written into
// every export and checked on import: a file from a newer build describes
// things this one has no place to put.
const BoardExportVersion = 1

// BoardExport is a whole board as a file: every card with its transcript, every
// note, every pipeline, and the links between them. Node ids are the export's
// own internal references — an import maps them onto new ones.
type BoardExport struct {
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exported_at"`
	Nodes      []ExportedNode `json:"nodes"`
	Links      []ExportedLink `json:"links"`
}

// ExportedNode is one card, note or pipeline. Which fields carry meaning is
// decided by Kind; the rest are absent.
type ExportedNode struct {
	NodeID int64   `json:"node_id"`
	Kind   string  `json:"kind"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Color  string  `json:"color,omitempty"`
	Body   string  `json:"body,omitempty"`

	Title            string            `json:"title,omitempty"`
	ConversationKind string            `json:"conversation_kind,omitempty"`
	Providers        []string          `json:"providers,omitempty"`
	ProjectPath      string            `json:"project_path,omitempty"`
	Access           string            `json:"access,omitempty"`
	Role             string            `json:"role,omitempty"`
	Models           map[string]string `json:"models,omitempty"`
	Turns            []ExportedTurn    `json:"turns,omitempty"`

	Stages []PipelineStage `json:"stages,omitempty"`

	// A trigger's schedule. Carried so a routine survives an export: without
	// it a board comes back with the wiring but nothing to start it.
	Prompt          string `json:"prompt,omitempty"`
	TriggerMode     string `json:"trigger_mode,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	AtTime          string `json:"at_time,omitempty"`
	Enabled         bool   `json:"enabled,omitempty"`
}

type ExportedTurn struct {
	Prompt    string             `json:"prompt"`
	Kind      string             `json:"kind"`
	CreatedAt time.Time          `json:"created_at"`
	Responses []ExportedResponse `json:"responses"`
}

type ExportedResponse struct {
	Provider string `json:"provider"`
	Status   Status `json:"status"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ExportedLink struct {
	SourceNodeID int64  `json:"source_node_id"`
	TargetNodeID int64  `json:"target_node_id"`
	Mode         string `json:"mode"`
	MaxRounds    int    `json:"max_rounds"`
	UntilDone    bool   `json:"until_done"`
	Briefing     string `json:"briefing,omitempty"`
}

// ImportResult reports what an import actually added. An import never replaces
// the board: it puts the file's contents alongside what is already there.
type ImportResult struct {
	Nodes int `json:"nodes"`
	Links int `json:"links"`
}

// SearchHit is one place on the board where a query was found. NodeID is what
// the canvas needs to bring it into view; everything else is what the result
// list shows, so a person can tell one hit from another without jumping to it.
type SearchHit struct {
	NodeID         int64  `json:"node_id"`
	ConversationID int64  `json:"conversation_id,omitempty"`
	TurnID         int64  `json:"turn_id,omitempty"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	// Provider is set only on an answer, where which provider said it matters.
	Provider string `json:"provider,omitempty"`
	// Where is which part of the board matched: title, role, prompt, answer or
	// note. The wording of it belongs to the UI.
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

// SplitCommand turns a typed command line into an argument array. Quoting is
// honoured so a path with spaces survives, but nothing is evaluated: there is
// no shell here, and no expansion of any kind. Card cycles and pipelines both
// take a typed line, and both must read it the same way.
func SplitCommand(line string) []string {
	var parts []string
	var current strings.Builder
	quote := rune(0)
	for _, symbol := range line {
		switch {
		case quote != 0:
			if symbol == quote {
				quote = 0
			} else {
				current.WriteRune(symbol)
			}
		case symbol == '\'' || symbol == '"':
			quote = symbol
		case symbol == ' ' || symbol == '\t':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(symbol)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// CancelResult reports how many of a card's responses a stop request reached.
// Zero means the card was already idle, which the UI treats as nothing to say.
type CancelResult struct {
	Stopped int `json:"stopped"`
}

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
	// NodePipeline is a card that runs an ordered list of commands in a project
	// and keeps its results. It has no provider and no transcript: a pipeline
	// is deterministic work, which is exactly what a conversation is not.
	NodePipeline = "pipeline"
	// NodeJoin waits for every line feeding it and hands on what they all said
	// as one message. Without it two paths reaching the same card arrive as two
	// separate turns, and the card answers each in ignorance of the other.
	NodeJoin = "join"
	// NodeTrigger starts a flow on its own: on a timer, at a time of day, or
	// when a person presses it. Everything reachable from it is what it runs,
	// which is what turns a board of cards into recurring work.
	NodeTrigger = "trigger"
)

// How a trigger decides it is due.
const (
	// TriggerManual only ever fires when someone presses it.
	TriggerManual = "manual"
	// TriggerInterval fires every so often, measured from the last firing.
	TriggerInterval = "interval"
	// TriggerDaily fires once a day at a wall-clock time, in local time: a
	// routine is something a person schedules against their own day.
	TriggerDaily = "daily"
)

// A flow run is one journey of a message across the board: what a person sent,
// and everything that followed from it. Turns carry the run they belong to, so
// a spreading exchange can be counted, followed and stopped as one thing.
const (
	RunRunning = "running"
	RunDone    = "done"
)

// FlowRun is one such journey.
type FlowRun struct {
	ID int64 `json:"id"`
	// OriginConversationID is the card a person spoke to. Zero once that card
	// is gone: a run outlives the card it started from.
	OriginConversationID int64  `json:"origin_conversation_id,omitempty"`
	Status               string `json:"status"`
	// Steps is how many turns this run has produced so far, the first included.
	Steps     int    `json:"steps"`
	StartedAt string `json:"started_at"`
}

// JoinNode is a waiting point on the board. It has no provider and no
// transcript of its own; it exists to hold a run until every line reaching it
// has spoken.
type JoinNode struct {
	NodeID int64  `json:"node_id"`
	Title  string `json:"title"`
	// Waiting is how many inputs the current run has delivered so far, and
	// Expected how many lines feed this node. Shown so a stalled join says
	// which side has not answered rather than merely looking idle.
	Waiting  int      `json:"waiting"`
	Expected int      `json:"expected"`
	Sources  []string `json:"sources,omitempty"`
}

// PipelineStage is one step of a pipeline as the user typed it. The command is
// a line rather than an argument array because that is what a person writes;
// it is split with SplitCommand and never handed to a shell.
type PipelineStage struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Pipeline is an ordered command list a card owns. Stages run in order and stop
// at the first failure.
type Pipeline struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// ProjectPath is the directory the stages run in. A pipeline with no
	// project has nothing to run against and is refused rather than run
	// somewhere arbitrary.
	ProjectPath string          `json:"project_path,omitempty"`
	Stages      []PipelineStage `json:"stages"`
	// Runs are this pipeline's most recent results, newest first.
	Runs []Run `json:"runs,omitempty"`
}

// NewPipeline creates a pipeline together with the canvas node that shows it.
type NewPipeline struct {
	Title string  `json:"title"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// PipelineConfig replaces everything about a pipeline in one write, the way a
// card's step list is saved.
type PipelineConfig struct {
	Title       string          `json:"title"`
	ProjectPath string          `json:"project_path"`
	Stages      []PipelineStage `json:"stages"`
}

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
	// Models is the model chosen per provider for this card, keyed by provider
	// name. A provider missing from the map runs on its own default, which is
	// what the CLI would pick in a terminal.
	Models map[string]string `json:"models,omitempty"`
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
	// TurnTrigger is a message a trigger sent. It reads like a user turn but
	// nobody typed it, and a card's transcript should not pretend otherwise.
	TurnTrigger = "trigger"
)

// Trigger is a node that starts work by itself. The flow it runs is whatever
// the board links to it, so a trigger is a starting point rather than a
// separate description of a pipeline.
type Trigger struct {
	ID     int64  `json:"id"`
	NodeID int64  `json:"node_id"`
	Title  string `json:"title"`
	// Prompt is what the cards linked to it receive when it fires.
	Prompt          string `json:"prompt"`
	Mode            string `json:"mode"`
	IntervalSeconds int    `json:"interval_seconds"`
	// AtTime is "HH:MM" in local time, used by the daily mode.
	AtTime      string `json:"at_time"`
	Enabled     bool   `json:"enabled"`
	DueAt       string `json:"due_at,omitempty"`
	LastFiredAt string `json:"last_fired_at,omitempty"`
	LastRunID   int64  `json:"last_run_id,omitempty"`
	// Working reports that the run this trigger last started is still going.
	// A routine that overruns its own interval must not start again on top of
	// itself.
	Working bool `json:"working"`
}

type NewTrigger struct {
	Title string  `json:"title"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// TriggerConfig replaces everything about a trigger in one write.
type TriggerConfig struct {
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	Mode            string `json:"mode"`
	IntervalSeconds int    `json:"interval_seconds"`
	AtTime          string `json:"at_time"`
	Enabled         bool   `json:"enabled"`
}

// CanvasNode is presentation state the daemon owns so a layout survives a
// restart and is identical for every client.
type CanvasNode struct {
	ID             int64   `json:"id"`
	Kind           string  `json:"kind"`
	ConversationID *int64  `json:"conversation_id,omitempty"`
	PipelineID     *int64  `json:"pipeline_id,omitempty"`
	TriggerID      *int64  `json:"trigger_id,omitempty"`
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

// ModelRequest picks the model one provider of a card runs on. An empty model
// hands the choice back to the provider's own default.
type ModelRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Normalised trims the model name. It is passed to a CLI as a single argument,
// so whitespace and an over-long value are rejected rather than carried.
func (r ModelRequest) Normalised() ModelRequest {
	r.Provider = strings.TrimSpace(r.Provider)
	r.Model = strings.TrimSpace(r.Model)
	return r
}

// Model is one entry of a provider's own model list. ID is what the CLI is
// given; Label is what that provider calls it, so a card offers the same names
// the provider's own client does.
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// ProviderModels is the model list one provider offers, plus which of them it
// falls back to when a card picks nothing.
type ProviderModels struct {
	Provider string  `json:"provider"`
	Models   []Model `json:"models"`
	// Default is the model the CLI uses on its own; empty when only the
	// provider knows.
	Default string `json:"default,omitempty"`
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
	Pipelines     []Pipeline     `json:"pipelines"`
	Nodes         []CanvasNode   `json:"nodes"`
	Links         []CanvasLink   `json:"links"`
	// Joins are the waiting points on the board, with what each is holding.
	Joins []JoinNode `json:"joins"`
	// Triggers are the starting points that fire on their own.
	Triggers []Trigger `json:"triggers"`
	// Runs are the journeys still in flight: what the board is busy with, as
	// one thing per message someone sent rather than one per card.
	Runs []FlowRun `json:"runs"`
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
