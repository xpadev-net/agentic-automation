package parser

import (
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
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			plan, rejected, reason := ParsePlanResult(tc.input)
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
