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
		if text(event, "type") != "content_block_delta" {
			return StreamUpdate{}
		}
		delta, _ := event["delta"].(map[string]any)
		if text(delta, "type") != "text_delta" {
			return StreamUpdate{}
		}
		return StreamUpdate{Delta: text(delta, "text")}
	case "result":
		update := StreamUpdate{Final: text(payload, "result"), SessionID: text(payload, "session_id")}
		if failed, _ := payload["is_error"].(bool); failed {
			update.Failure = update.Final
			update.Final = ""
			if update.Failure == "" {
				update.Failure = "provider reported an error"
			}
		}
		return update
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
	case "item.completed":
		item, _ := payload["item"].(map[string]any)
		if text(item, "type") != "agent_message" {
			return StreamUpdate{}
		}
		// Codex emits the whole message at once rather than in deltas.
		return StreamUpdate{Final: text(item, "text")}
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
		return StreamUpdate{Delta: text(step, "text_delta")}
	case "result":
		result, _ := payload["result"].(map[string]any)
		update := StreamUpdate{
			Final:     text(result, "response"),
			SessionID: text(result, "conversation_id"),
		}
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
