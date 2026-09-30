package parser

import (
	"regexp"
	"strings"

	"agent-runner/pkg/utils"
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
// If multiple tags are present, the last one found (from the end of the output) is used.
// This ensures that the actual ASSISTANT output is used instead of example text from the prompt.
// Returns planContent, rejected flag, rejectionReason.
func ParsePlanResult(output string, mode utils.OutputMode) (string, bool, string) {
	assistantText, err := utils.ExtractAssistantText(output, mode)
	if err != nil {
		return "", true, "プラン出力の解析に失敗しました"
	}
	trimmed := strings.TrimSpace(assistantText)
	if trimmed == "" {
		return "", true, "プラン出力が空です"
	}

	// Find all matches for both tags (search from the end)
	allCreatedMatches := planCreatedPattern.FindAllStringSubmatchIndex(trimmed, -1)
	allRejectedMatches := planRejectedPattern.FindAllStringSubmatchIndex(trimmed, -1)

	// Get the last match for each tag type (if any)
	var lastCreatedMatch []int
	var lastRejectedMatch []int
	if len(allCreatedMatches) > 0 {
		lastCreatedMatch = allCreatedMatches[len(allCreatedMatches)-1]
	}
	if len(allRejectedMatches) > 0 {
		lastRejectedMatch = allRejectedMatches[len(allRejectedMatches)-1]
	}

	// Determine which tag appears last (closer to the end of the output)
	createdPos := -1
	rejectedPos := -1
	if len(lastCreatedMatch) > 0 {
		createdPos = lastCreatedMatch[0]
	}
	if len(lastRejectedMatch) > 0 {
		rejectedPos = lastRejectedMatch[0]
	}

	// Use the tag that appears last (closer to the end)
	if createdPos >= 0 && (rejectedPos < 0 || createdPos > rejectedPos) {
		// <plan_created> tag found last
		content := strings.TrimSpace(trimmed[lastCreatedMatch[2]:lastCreatedMatch[3]])
		if content == "" {
			return "", true, "プラン内容が空です"
		}
		return content, false, ""
	}

	if rejectedPos >= 0 {
		// <plan_rejected> tag found last
		reason := strings.TrimSpace(trimmed[lastRejectedMatch[2]:lastRejectedMatch[3]])
		if reason == "" {
			reason = "プランが却下されました（理由不明）"
		}
		return "", true, reason
	}

	return "", true, "プラン出力の形式が不正です。'<plan_created>...</plan_created>'または'<plan_rejected>...</plan_rejected>'の形式で出力してください。"
}
