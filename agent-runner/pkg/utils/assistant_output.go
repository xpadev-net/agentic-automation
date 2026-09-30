package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OutputMode is selected from the command contract, never inferred from whether
// assistant text happened to be found. JSONL must never fall back to raw text.
type OutputMode string

const (
	OutputPlainText   OutputMode = "plain_text"
	OutputCodexJSONL  OutputMode = "codex_jsonl"
	OutputCursorJSONL OutputMode = "cursor_jsonl"
)

func OutputModeForAgent(agentType string) OutputMode {
	switch agentType {
	case "claude-code":
		return OutputPlainText
	case "codex":
		return OutputCodexJSONL
	case "cursor-agent":
		return OutputCursorJSONL
	default:
		return ""
	}
}

// ExtractAssistantText accepts only the final assistant message of a completed,
// successful stream. Tool output, prompt echoes, and incomplete/error streams
// are never candidates. Plain output is supported deliberately for Claude CLI.
func ExtractAssistantText(output string, mode OutputMode) (string, error) {
	if mode == OutputPlainText {
		return output, nil
	}
	if mode != OutputCodexJSONL && mode != OutputCursorJSONL {
		return "", fmt.Errorf("unsupported agent output mode %q", mode)
	}
	candidate := ""
	completed := false
	for index, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type    string          `json:"type"`
			Subtype string          `json:"subtype"`
			IsError bool            `json:"is_error"`
			Error   json.RawMessage `json:"error"`
			Item    struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Message struct {
				Role    string           `json:"role"`
				Content []MessageContent `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type == "" {
			return "", fmt.Errorf("invalid JSONL event at line %d", index+1)
		}
		if completed {
			return "", fmt.Errorf("unexpected event after stream completion")
		}
		if event.IsError || (len(event.Error) > 0 && string(event.Error) != "null") || event.Type == "error" || event.Type == "turn.failed" {
			return "", fmt.Errorf("agent stream reported failure")
		}
		if mode == OutputCodexJSONL {
			switch event.Type {
			case "turn.started":
				candidate = ""
			case "item.started", "item.updated", "item.completed":
				if event.Type == "item.completed" && event.Item.Type == "agent_message" {
					candidate = event.Item.Text
				} else {
					candidate = ""
				}
			case "turn.completed":
				completed = true
			}
		} else {
			switch event.Type {
			case "assistant":
				if event.Message.Role != "" && event.Message.Role != "assistant" {
					return "", fmt.Errorf("invalid assistant role")
				}
				var parts []string
				for _, part := range event.Message.Content {
					if part.Type == "text" {
						parts = append(parts, part.Text)
					}
				}
				candidate = strings.Join(parts, "\n")
			case "tool_call", "user":
				candidate = ""
			case "result":
				if event.Subtype != "success" {
					return "", fmt.Errorf("agent stream did not succeed")
				}
				completed = true
			}
		}
	}
	if !completed {
		return "", fmt.Errorf("agent stream is incomplete")
	}
	if strings.TrimSpace(candidate) == "" {
		return "", fmt.Errorf("completed stream contains no final assistant message")
	}
	return candidate, nil
}
