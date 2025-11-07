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
			input:      "PLAN_CREATED\n\n1. Fix bug\n2. Add test",
			expectPlan: "1. Fix bug\n2. Add test",
			rejected:   false,
		},
		"plan rejected": {
			input:          "PLAN_REJECTED\n\nToo risky",
			rejected:       true,
			reasonContains: "Too risky",
		},
		"empty output": {
			input:          "",
			rejected:       true,
			reasonContains: "空",
		},
		"missing marker": {
			input:          "Some random text",
			rejected:       true,
			reasonContains: "形式",
		},
		"created empty body": {
			input:          "PLAN_CREATED\n\n",
			rejected:       true,
			reasonContains: "空",
		},
		"rejected empty reason": {
			input:          "PLAN_REJECTED",
			rejected:       true,
			reasonContains: "理由",
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
