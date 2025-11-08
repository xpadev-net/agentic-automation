package parser

import (
	"regexp"
	"strings"
)

var (
	// planCreatedPattern matches <plan_created>...</plan_created> tags
	planCreatedPattern = regexp.MustCompile(`(?s)<plan_created>(.*?)</plan_created>`)
	// planRejectedPattern matches <plan_rejected>...</plan_rejected> tags
	planRejectedPattern = regexp.MustCompile(`(?s)<plan_rejected>(.*?)</plan_rejected>`)
)

// ParsePlanResult parses the agent output to extract plan content or rejection reason.
// The output can contain XML-like tags <plan_created>...</plan_created> or <plan_rejected>...</plan_rejected>.
// Text outside the tags is allowed and will be ignored.
// If both tags are present, the first one found is used.
// Returns planContent, rejected flag, rejectionReason.
func ParsePlanResult(output string) (string, bool, string) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", true, "プラン出力が空です"
	}

	// Find both tags and their positions
	createdMatches := planCreatedPattern.FindStringSubmatchIndex(trimmed)
	rejectedMatches := planRejectedPattern.FindStringSubmatchIndex(trimmed)

	// Determine which tag appears first
	createdPos := -1
	rejectedPos := -1
	if len(createdMatches) > 0 {
		createdPos = createdMatches[0]
	}
	if len(rejectedMatches) > 0 {
		rejectedPos = rejectedMatches[0]
	}

	// Use the first tag found
	if createdPos >= 0 && (rejectedPos < 0 || createdPos < rejectedPos) {
		// <plan_created> tag found first
		content := strings.TrimSpace(trimmed[createdMatches[2]:createdMatches[3]])
		if content == "" {
			return "", true, "プラン内容が空です"
		}
		return content, false, ""
	}

	if rejectedPos >= 0 {
		// <plan_rejected> tag found
		reason := strings.TrimSpace(trimmed[rejectedMatches[2]:rejectedMatches[3]])
		if reason == "" {
			reason = "プランが却下されました（理由不明）"
		}
		return "", true, reason
	}

	return "", true, "プラン出力の形式が不正です。'<plan_created>...</plan_created>'または'<plan_rejected>...</plan_rejected>'の形式で出力してください。"
}
