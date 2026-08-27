package provider

import (
	"strings"
	"testing"
)

// The fixtures below are real lines captured from each CLI, so a format change
// upstream shows up here rather than as a silently empty answer.

func TestClaudeStreamDeltaAndResult(t *testing.T) {
	delta := DecodeStreamLine(StreamClaude,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"1"}}}`)
	if delta.Delta != "1" {
		t.Fatalf("delta = %q, want %q", delta.Delta, "1")
	}

	final := DecodeStreamLine(StreamClaude,
		`{"type":"result","is_error":false,"result":"1\n2\n3","session_id":"df974cc4-661a-48f6-a83e-b5a1fce22475"}`)
	if final.Final != "1\n2\n3" {
		t.Fatalf("final = %q", final.Final)
	}
	if final.SessionID != "df974cc4-661a-48f6-a83e-b5a1fce22475" {
		t.Fatalf("session = %q", final.SessionID)
	}
}

func TestClaudeStreamReportsFailure(t *testing.T) {
	update := DecodeStreamLine(StreamClaude, `{"type":"result","is_error":true,"result":"kota bitti"}`)
	if update.Failure != "kota bitti" {
		t.Fatalf("failure = %q", update.Failure)
	}
	if update.Final != "" {
		t.Fatalf("a failed result must not be used as content, got %q", update.Final)
	}
}

func TestClaudeStreamCarriesQuota(t *testing.T) {
	update := DecodeStreamLine(StreamClaude,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.16,"resetsAt":1787758200},"seven_day":{"utilization":0.13,"resetsAt":1787954400}}}}`)
	if update.Quota == nil {
		t.Fatal("quota was not decoded")
	}
	if update.Quota.ShortUtilization != 0.16 || update.Quota.LongUtilization != 0.13 {
		t.Fatalf("utilisation = %v / %v", update.Quota.ShortUtilization, update.Quota.LongUtilization)
	}
	if update.Quota.ShortResetsAt != 1787758200 {
		t.Fatalf("reset = %d", update.Quota.ShortResetsAt)
	}
}

func TestCodexStreamThreadAndMessage(t *testing.T) {
	started := DecodeStreamLine(StreamCodex, `{"type":"thread.started","thread_id":"01a03daf-a059-7291-9ccb-f3fe05a3536d"}`)
	if started.SessionID != "01a03daf-a059-7291-9ccb-f3fe05a3536d" {
		t.Fatalf("session = %q", started.SessionID)
	}

	message := DecodeStreamLine(StreamCodex,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"1\n2\n3\n4\n5"}}`)
	if message.Final != "1\n2\n3\n4\n5" {
		t.Fatalf("final = %q", message.Final)
	}
}

// Codex reports non-message items too; they must not become the answer.
func TestCodexStreamIgnoresOtherItems(t *testing.T) {
	update := DecodeStreamLine(StreamCodex,
		`{"type":"item.completed","item":{"id":"item_1","type":"reasoning","text":"dusunuyorum"}}`)
	if update.Final != "" || update.Delta != "" {
		t.Fatalf("reasoning item leaked into the answer: %+v", update)
	}
}

func TestAntigravityStreamDeltaAndResult(t *testing.T) {
	delta := DecodeStreamLine(StreamAntigravity,
		`{"event":"step_update","step_update":{"conversation_id":"e799741e","step_index":2,"text_delta":"merhaba"}}`)
	if delta.Delta != "merhaba" {
		t.Fatalf("delta = %q", delta.Delta)
	}

	final := DecodeStreamLine(StreamAntigravity,
		`{"event":"result","result":{"conversation_id":"e799741e","status":"SUCCESS","response":"tamam"}}`)
	if final.Final != "tamam" || final.SessionID != "e799741e" {
		t.Fatalf("final = %+v", final)
	}
}

func TestAntigravityStreamReportsNonSuccess(t *testing.T) {
	update := DecodeStreamLine(StreamAntigravity,
		`{"event":"result","result":{"conversation_id":"x","status":"ERROR","response":"yarim cevap"}}`)
	if update.Failure == "" {
		t.Fatal("a non-SUCCESS status must be a failure")
	}
	if update.Final != "" {
		t.Fatalf("content = %q, want empty", update.Final)
	}
}

// Every provider names its token counts differently, and reading the wrong
// field fails silently as a zero — which would leave a full window unnoticed.
// The payloads below are trimmed copies of real output from each CLI.
func TestEveryStreamFormatReportsItsContextSize(t *testing.T) {
	claude := DecodeStreamLine(StreamClaude,
		`{"type":"result","result":"tamam","session_id":"s","usage":{"input_tokens":2,`+
			`"cache_creation_input_tokens":17694,"cache_read_input_tokens":12227,"output_tokens":38}}`)
	if claude.ContextTokens != 2+17694+12227 {
		t.Fatalf("claude context = %d", claude.ContextTokens)
	}

	// Codex counts cached input inside input_tokens, so it must not be added.
	codex := DecodeStreamLine(StreamCodex,
		`{"type":"turn.completed","usage":{"input_tokens":23390,"cached_input_tokens":6912,"output_tokens":12}}`)
	if codex.ContextTokens != 23390 {
		t.Fatalf("codex context = %d", codex.ContextTokens)
	}

	// Antigravity reports cache reads outside input_tokens.
	antigravity := DecodeStreamLine(StreamAntigravity,
		`{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","response":"tamam",`+
			`"usage":{"input_tokens":20159,"output_tokens":1059,"cache_read_tokens":16297}}}`)
	if antigravity.ContextTokens != 20159+16297 {
		t.Fatalf("antigravity context = %d", antigravity.ContextTokens)
	}
}

// A turn that reports no usage must not read as an empty window, which would
// look like a session that had just been reset.
func TestMissingUsageReportsNoContextSize(t *testing.T) {
	for _, line := range []string{
		`{"type":"result","result":"tamam","session_id":"s"}`,
		`{"type":"turn.completed"}`,
	} {
		format := StreamClaude
		if strings.Contains(line, "turn.completed") {
			format = StreamCodex
		}
		if update := DecodeStreamLine(format, line); update.ContextTokens != 0 {
			t.Fatalf("%s: context = %d, want 0", line, update.ContextTokens)
		}
	}
}

func TestPlainStreamKeepsLines(t *testing.T) {
	update := DecodeStreamLine(StreamPlain, "Ankara")
	if update.Delta != "Ankara\n" {
		t.Fatalf("delta = %q", update.Delta)
	}
}

// Providers emit events we do not model, and some lines are not JSON at all.
// Neither may corrupt the answer.
func TestUnknownLinesAreIgnored(t *testing.T) {
	for _, line := range []string{
		`{"type":"system","subtype":"init","cwd":"C:\tmp"}`,
		`not json at all`,
		``,
		`{"event":"init","conversation_id":""}`,
	} {
		for _, format := range []StreamFormat{StreamClaude, StreamCodex, StreamAntigravity} {
			update := DecodeStreamLine(format, line)
			if update.Delta != "" || update.Final != "" || update.Failure != "" {
				t.Fatalf("%s produced %+v for %q", format, update, line)
			}
		}
	}
}

// Activity must be reported even by events that carry no answer text: a
// provider running a tool is busy but silent.
func TestClaudeReportsToolActivity(t *testing.T) {
	update := DecodeStreamLine(StreamClaude,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"Read"}}}`)
	if update.Activity != "tool:Read" {
		t.Fatalf("activity = %q", update.Activity)
	}
	if update.Delta != "" {
		t.Fatalf("a tool block must not add text, got %q", update.Delta)
	}
}

func TestClaudeReportsRequestStatus(t *testing.T) {
	update := DecodeStreamLine(StreamClaude, `{"type":"system","subtype":"status","status":"requesting"}`)
	if update.Activity != "requesting" {
		t.Fatalf("activity = %q", update.Activity)
	}
}

func TestAntigravityReportsActiveTool(t *testing.T) {
	active := DecodeStreamLine(StreamAntigravity,
		`{"event":"step_update","step_update":{"step_type":"tool","state":"ACTIVE","tool_name":"write_to_file"}}`)
	if active.Activity != "tool:write_to_file" {
		t.Fatalf("activity = %q", active.Activity)
	}
	// A finished tool step is not what it is busy with any more.
	done := DecodeStreamLine(StreamAntigravity,
		`{"event":"step_update","step_update":{"step_type":"tool","state":"DONE","tool_name":"write_to_file"}}`)
	if done.Activity != "" {
		t.Fatalf("a DONE step reported activity %q", done.Activity)
	}
}

func TestCodexReportsReasoningActivity(t *testing.T) {
	update := DecodeStreamLine(StreamCodex,
		`{"type":"item.completed","item":{"id":"item_1","type":"reasoning","text":"..."}}`)
	if update.Activity != "thinking" {
		t.Fatalf("activity = %q", update.Activity)
	}
	if update.Final != "" {
		t.Fatalf("reasoning leaked into the answer: %q", update.Final)
	}
}
