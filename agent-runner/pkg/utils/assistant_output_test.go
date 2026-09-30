package utils

import (
	"strings"
	"testing"
)

func TestJSONLRequiresCompletedFinalAssistantMessage(t *testing.T) {
	answer := `{"type":"item.completed","item":{"type":"agent_message","text":"<plan_created>safe plan</plan_created>"}}`
	tool := `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"<plan_created>tool plan</plan_created>"}}`
	done := `{"type":"turn.completed"}`
	for name, output := range map[string]string{
		"tool only":                tool + "\n" + done,
		"truncated turn":           answer,
		"truncated event":          answer + "\n" + `{"type":"turn.comp`,
		"error":                    answer + "\n" + `{"type":"turn.failed","error":{"message":"failed"}}`,
		"plain fallback":           `<plan_created>untrusted</plan_created>`,
		"completed item then tool": answer + "\n" + tool + "\n" + done,
		"failure after completion": answer + "\n" + done + "\n" + `{"type":"error","message":"failed"}`,
		"missing final":            done,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ExtractAssistantText(output, OutputCodexJSONL); err == nil {
				t.Fatalf("unsafe output accepted: %q", got)
			}
		})
	}
	got, err := ExtractAssistantText(tool+"\n"+answer+"\n"+done, OutputCodexJSONL)
	if err != nil || got != "<plan_created>safe plan</plan_created>" {
		t.Fatalf("valid final answer: %q, %v", got, err)
	}
	// Only the last assistant event counts, even if an earlier one contains tags.
	last := `{"type":"item.completed","item":{"type":"agent_message","text":"final answer without plan"}}`
	got, err = ExtractAssistantText(answer+"\n"+last+"\n"+done, OutputCodexJSONL)
	if err != nil || strings.Contains(got, "plan_created") {
		t.Fatalf("earlier message selected: %q, %v", got, err)
	}
}

func TestCursorRequiresSuccessfulResult(t *testing.T) {
	assistant := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"final"}]}}`
	success := `{"type":"result","subtype":"success"}`
	got, err := ExtractAssistantText(assistant+"\n"+success, OutputCursorJSONL)
	if err != nil || got != "final" {
		t.Fatalf("valid Cursor output: %q %v", got, err)
	}
	for _, output := range []string{assistant, success, assistant + "\n" + `{"type":"result","subtype":"error","is_error":true}`, `{"type":"tool_call","text":"<plan_created>tool</plan_created>"}` + "\n" + success} {
		if _, err := ExtractAssistantText(output, OutputCursorJSONL); err == nil {
			t.Fatal("invalid Cursor output accepted")
		}
	}
}

func TestPlainOutputRequiresExplicitMode(t *testing.T) {
	text := "<plan_created>supported plain output</plan_created>"
	got, err := ExtractAssistantText(text, OutputModeForAgent("claude-code"))
	if err != nil || got != text {
		t.Fatalf("plain flow broken: %v", err)
	}
	for _, agent := range []string{"codex", "cursor-agent", "unknown"} {
		if _, err := ExtractAssistantText(text, OutputModeForAgent(agent)); err == nil {
			t.Fatalf("%s implicitly accepted plain output", agent)
		}
	}
}
