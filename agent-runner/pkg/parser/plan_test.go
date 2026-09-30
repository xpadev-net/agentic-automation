package parser

import (
	"agent-runner/pkg/utils"
	"strings"
	"testing"
)

func TestParsePlanResult(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectPlan     string
		rejected       bool
		reasonContains string
	}{
		"plan created": {
			input:      "<plan_created>1. Fix bug\n2. Add test</plan_created>",
			expectPlan: "1. Fix bug\n2. Add test",
			rejected:   false,
		},
		"plan rejected": {
			input:          "<plan_rejected>Too risky</plan_rejected>",
			rejected:       true,
			reasonContains: "Too risky",
		},
		"empty output": {
			input:          "",
			rejected:       true,
			reasonContains: "空",
		},
		"missing tag": {
			input:          "Some random text",
			rejected:       true,
			reasonContains: "形式",
		},
		"created empty body": {
			input:          "<plan_created></plan_created>",
			rejected:       true,
			reasonContains: "空",
		},
		"created empty body with whitespace": {
			input:          "<plan_created>   </plan_created>",
			rejected:       true,
			reasonContains: "空",
		},
		"rejected empty reason": {
			input:          "<plan_rejected></plan_rejected>",
			rejected:       true,
			reasonContains: "理由",
		},
		"plan created with text outside": {
			input:      "これはプランです。\n<plan_created>1. Task 1\n2. Task 2</plan_created>\n以上です。",
			expectPlan: "1. Task 1\n2. Task 2",
			rejected:   false,
		},
		"plan rejected with text outside": {
			input:          "分析結果:\n<plan_rejected>リスクが高すぎます</plan_rejected>\n理由: 上記の通り",
			rejected:       true,
			reasonContains: "リスクが高すぎます",
		},
		"plan created with multiline content": {
			input:      "<plan_created>ステップ1: バグを修正\nステップ2: テストを追加\nステップ3: ドキュメントを更新</plan_created>",
			expectPlan: "ステップ1: バグを修正\nステップ2: テストを追加\nステップ3: ドキュメントを更新",
			rejected:   false,
		},
		"plan created with text before and after": {
			input:      "前置きの文章です。\n\n<plan_created>プラン内容</plan_created>\n\n後置きの文章です。",
			expectPlan: "プラン内容",
			rejected:   false,
		},
		"multiple tags - last valid one used": {
			input:      "<plan_rejected>最初のタグ</plan_rejected>\n<plan_created>2番目のタグ</plan_created>",
			expectPlan: "2番目のタグ",
			rejected:   false,
		},
		"multiple created tags - last one used": {
			input:      "<plan_created>最初のプラン</plan_created>\n<plan_created>最後のプラン</plan_created>",
			expectPlan: "最後のプラン",
			rejected:   false,
		},
		"example text in prompt should be ignored": {
			input:      "プランの形式:\n- プランを作成する場合: <plan_created>[プラン内容]</plan_created>\n\n実際のプラン:\n<plan_created>実際のプラン内容です</plan_created>",
			expectPlan: "実際のプラン内容です",
			rejected:   false,
		},
		"plan created with leading/trailing whitespace in tag": {
			input:      "<plan_created>\n  1. Task 1\n  2. Task 2\n</plan_created>",
			expectPlan: "1. Task 1\n  2. Task 2",
			rejected:   false,
		},
		"codex JSONL plan is decoded": {
			input:          "{\"type\":\"item.completed\",\"item\":{\"type\":\"reasoning\",\"text\":\"ignore\"}}\n{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"前置き \\\"quoted\\\"\"}}\n{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"<plan_created>1. READMEを更新\\n2. テストを実行</plan_created>\"}}",
			expectPlan:     "1. READMEを更新\n2. テストを実行",
			rejected:       false,
			reasonContains: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mode := utils.OutputPlainText
			if strings.HasPrefix(name, "codex JSONL") {
				mode = utils.OutputCodexJSONL
				tc.input += "\n{\"type\":\"turn.completed\"}"
			}
			plan, rejected, reason := ParsePlanResult(tc.input, mode)
			if rejected != tc.rejected {
				t.Fatalf("expected rejected=%v, got %v", tc.rejected, rejected)
			}
			if tc.expectPlan != "" && plan != tc.expectPlan {
				t.Fatalf("expected plan %q, got %q", tc.expectPlan, plan)
			}
			if tc.reasonContains != "" && !strings.Contains(reason, tc.reasonContains) {
				t.Fatalf("expected reason to contain %q, got %q", tc.reasonContains, reason)
			}
		})
	}
}

func TestPlanCannotComeFromToolOutputOrTruncatedStream(t *testing.T) {
	for _, output := range []string{
		`{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"<plan_created>tool output</plan_created>"}}` + "\n" + `{"type":"turn.completed"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"<plan_created>incomplete</plan_created>"}}`,
		`{"type":"error","message":"<plan_created>error text</plan_created>"}`,
	} {
		if plan, rejected, _ := ParsePlanResult(output, utils.OutputCodexJSONL); !rejected || plan != "" {
			t.Fatalf("unsafe plan accepted: %q", plan)
		}
	}
}
