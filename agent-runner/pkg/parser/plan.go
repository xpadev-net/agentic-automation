package parser

import (
	"fmt"
	"strings"
)

const (
	planCreatedMarker  = "PLAN_CREATED"
	planRejectedMarker = "PLAN_REJECTED"
)

// ParsePlanResult parses the agent output to extract plan content or rejection reason.
// Returns planContent, rejected flag, rejectionReason.
func ParsePlanResult(output string) (string, bool, string) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", true, "プラン出力が空です"
	}

	if strings.HasPrefix(trimmed, planCreatedMarker) {
		content := strings.TrimSpace(strings.TrimPrefix(trimmed, planCreatedMarker))
		content = strings.TrimLeft(content, "\n")
		content = strings.TrimSpace(content)
		if content == "" {
			return "", true, "プラン内容が空です"
		}
		return content, false, ""
	}

	if strings.HasPrefix(trimmed, planRejectedMarker) {
		reason := strings.TrimSpace(strings.TrimPrefix(trimmed, planRejectedMarker))
		reason = strings.TrimLeft(reason, "\n")
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "プランが却下されました（理由不明）"
		}
		return "", true, reason
	}

	return "", true, fmt.Sprintf("プラン出力の形式が不正です。'%s'または'%s'で開始してください。", planCreatedMarker, planRejectedMarker)
}
