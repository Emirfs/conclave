package provider

import (
	"encoding/json"
	"strings"
)

// StreamFormat says how a provider's stdout should be read. Only the JSON
// formats can report progress before the answer is finished; a plain provider
// simply prints its answer.
type StreamFormat string

const (
	StreamPlain       StreamFormat = "plain"
	StreamClaude      StreamFormat = "claude"
	StreamCodex       StreamFormat = "codex"
	StreamAntigravity StreamFormat = "antigravity"
)

// Quota is a provider's own view of how much of its allowance is spent.
// Utilisation is a fraction between 0 and 1; reset times are Unix seconds.
type Quota struct {
	ShortLabel        string  `json:"short_label,omitempty"`
	ShortUtilization  float64 `json:"short_utilization"`
	ShortResetsAt     int64   `json:"short_resets_at,omitempty"`
	LongLabel         string  `json:"long_label,omitempty"`
	LongUtilization   float64 `json:"long_utilization"`
	LongResetsAt      int64   `json:"long_resets_at,omitempty"`
}

// StreamUpdate is what one line of provider output contributed.
type StreamUpdate struct {
	// Delta is text to append to the answer as it is produced.
	Delta string
	// Final is an authoritative complete answer; it wins over accumulated deltas.
	Final string
	// SessionID identifies the provider-side conversation, for later resuming.
	SessionID string
	// Failure is a provider-reported error.
	Failure string
	// Quota is the provider's remaining-allowance report, when it offers one.
	Quota *Quota
	// Activity is a stable machine token describing what the provider is doing
	// right now: "requesting", "thinking", "writing", or "tool:<name>". It is
	// deliberately not human text, so the client owns the wording.
	Activity string
	// ContextTokens is how much context the provider carried into this turn:
	// everything on the input side, cached or not. It grows with the session,
	// which is what makes it a usable measure of how full the window is.
	ContextTokens int
	// InputTokens and OutputTokens are what this one turn cost. Unlike
	// ContextTokens they do not grow with the session, which is what makes them
	// addable: a week of turns sums to a week of usage.
	InputTokens  int
	OutputTokens int
}

// DecodeStreamLine interprets a single line of provider output. Unrecognised
// lines yield an empty update: a provider is free to emit events we do not
// model, and guessing at them would corrupt the answer.
func DecodeStreamLine(format StreamFormat, line string) StreamUpdate {
	line = strings.TrimSpace(line)
	if line == "" {
		return StreamUpdate{}
	}
	if format == StreamPlain {
		return StreamUpdate{Delta: line + "\n"}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return StreamUpdate{}
	}
	switch format {
	case StreamClaude:
		return decodeClaude(payload)
	case StreamCodex:
		return decodeCodex(payload)
	case StreamAntigravity:
		return decodeAntigravity(payload)
	default:
		return StreamUpdate{}
	}
}

func decodeClaude(payload map[string]any) StreamUpdate {
	switch text(payload, "type") {
	case "stream_event":
		event, _ := payload["event"].(map[string]any)
		switch text(event, "type") {
		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			if text(delta, "type") != "text_delta" {
				return StreamUpdate{}
			}
			return StreamUpdate{Delta: text(delta, "text"), Activity: "writing"}
		case "content_block_start":
			block, _ := event["content_block"].(map[string]any)
			if text(block, "type") == "tool_use" {
				return StreamUpdate{Activity: "tool:" + text(block, "name")}
			}
			return StreamUpdate{Activity: "writing"}
		}
		return StreamUpdate{}
	case "result":
		update := StreamUpdate{Final: text(payload, "result"), SessionID: text(payload, "session_id")}
		usage, _ := payload["usage"].(map[string]any)
		update.ContextTokens = int(number(usage, "input_tokens") +
			number(usage, "cache_read_input_tokens") +
			number(usage, "cache_creation_input_tokens"))
		update.InputTokens = update.ContextTokens
		update.OutputTokens = int(number(usage, "output_tokens"))
		if failed, _ := payload["is_error"].(bool); failed {
			update.Failure = update.Final
			update.Final = ""
			if update.Failure == "" {
				update.Failure = "provider reported an error"
			}
		}
		return update
	case "system":
		if text(payload, "subtype") == "status" {
			if status := text(payload, "status"); status != "" {
				return StreamUpdate{Activity: status}
			}
		}
		return StreamUpdate{}
	case "rate_limit_event":
		info, _ := payload["rate_limit_info"].(map[string]any)
		windows, _ := info["unifiedWindows"].(map[string]any)
		short, _ := windows["five_hour"].(map[string]any)
		long, _ := windows["seven_day"].(map[string]any)
		if short == nil && long == nil {
			return StreamUpdate{}
		}
		return StreamUpdate{Quota: &Quota{
			ShortLabel: "5 saat", ShortUtilization: number(short, "utilization"),
			ShortResetsAt: int64(number(short, "resetsAt")),
			LongLabel:     "7 gün", LongUtilization: number(long, "utilization"),
			LongResetsAt:  int64(number(long, "resetsAt")),
		}}
	}
	return StreamUpdate{}
}

func decodeCodex(payload map[string]any) StreamUpdate {
	switch text(payload, "type") {
	case "thread.started":
		return StreamUpdate{SessionID: text(payload, "thread_id")}
	case "turn.started":
		return StreamUpdate{Activity: "thinking"}
	case "item.started", "item.completed":
		item, _ := payload["item"].(map[string]any)
		kind := text(item, "type")
		if kind != "agent_message" {
			// Not the answer, but it still says what codex is busy with.
			return StreamUpdate{Activity: codexActivity(kind)}
		}
		if text(payload, "type") == "item.started" {
			return StreamUpdate{Activity: "writing"}
		}
		// Codex emits the whole message at once rather than in deltas.
		return StreamUpdate{Final: text(item, "text"), Activity: "writing"}
	case "turn.completed":
		// Codex counts cached input inside input_tokens, so this is already the
		// whole input side of the turn.
		usage, _ := payload["usage"].(map[string]any)
		input := int(number(usage, "input_tokens"))
		return StreamUpdate{
			ContextTokens: input,
			InputTokens:   input,
			OutputTokens:  int(number(usage, "output_tokens")),
		}
	case "error":
		return StreamUpdate{Failure: text(payload, "message")}
	}
	return StreamUpdate{}
}

func decodeAntigravity(payload map[string]any) StreamUpdate {
	switch text(payload, "event") {
	case "init":
		return StreamUpdate{SessionID: text(payload, "conversation_id")}
	case "step_update":
		step, _ := payload["step_update"].(map[string]any)
		update := StreamUpdate{Delta: text(step, "text_delta")}
		if tool := text(step, "tool_name"); tool != "" {
			if text(step, "state") == "ACTIVE" {
				update.Activity = "tool:" + tool
			}
		} else if text(step, "step_type") == "agent_response" {
			update.Activity = "writing"
		}
		return update
	case "result":
		result, _ := payload["result"].(map[string]any)
		update := StreamUpdate{
			Final:     text(result, "response"),
			SessionID: text(result, "conversation_id"),
		}
		// Antigravity reports cache reads outside input_tokens, so the window
		// only adds up with both.
		usage, _ := result["usage"].(map[string]any)
		update.ContextTokens = int(number(usage, "input_tokens") + number(usage, "cache_read_tokens"))
		update.InputTokens = update.ContextTokens
		update.OutputTokens = int(number(usage, "output_tokens"))
		if status := text(result, "status"); status != "" && status != "SUCCESS" {
			update.Failure = "provider reported status " + status
			update.Final = ""
		}
		return update
	}
	return StreamUpdate{}
}

func text(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return value
}

func number(payload map[string]any, key string) float64 {
	if payload == nil {
		return 0
	}
	value, _ := payload[key].(float64)
	return value
}

// codexActivity maps a codex item type onto the shared activity vocabulary.
func codexActivity(kind string) string {
	switch kind {
	case "reasoning":
		return "thinking"
	case "command_execution":
		return "tool:command"
	case "file_change", "patch_apply":
		return "tool:edit"
	case "web_search":
		return "tool:search"
	case "":
		return ""
	default:
		return "tool:" + kind
	}
}
