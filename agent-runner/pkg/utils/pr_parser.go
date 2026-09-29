package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// ParsePRTitleAndBody parses the PR title and body from cursor-agent's output.
// It extracts assistant entries from the JSON stream output and parses XML format.
// Returns title and body, or an error if parsing fails.
func ParsePRTitleAndBody(output string) (string, string, error) {
	// Extract all assistant entries from the output
	assistantText, err := extractAssistantText(output)
	if err != nil {
		return "", "", fmt.Errorf("failed to extract assistant text: %w", err)
	}

	if assistantText == "" {
		return "", "", fmt.Errorf("no assistant text found in output")
	}

	// Parse XML format: <title>...</title><body>...</body>
	title, body, err := parseXMLTitleAndBody(assistantText)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse XML: %w", err)
	}

	// Validate that title and body are not empty
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)

	if title == "" {
		return "", "", fmt.Errorf("title is empty after parsing")
	}
	if body == "" {
		return "", "", fmt.Errorf("body is empty after parsing")
	}

	return title, body, nil
}

// extractAssistantText extracts all assistant entry text from the agent's output.
// For cursor-agent it parses each line of the JSON stream and combines assistant entry messages.
// For agents that emit plain text (e.g. codex), it falls back to the raw output when
// no assistant entries were found.
func extractAssistantText(output string) (string, error) {
	var texts []string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := ParseLogEntry([]byte(line))
		if err != nil {
			// Skip lines that are not valid log entries
			continue
		}

		// Extract text from assistant entries
		if assistantEntry, ok := entry.(*AssistantEntry); ok {
			for _, content := range assistantEntry.Message.Content {
				if content.Type == "text" && content.Text != "" {
					texts = append(texts, content.Text)
				}
			}
		}
	}

	if len(texts) == 0 {
		// Plain-text agents (e.g. codex exec) print the final message verbatim;
		// downstream XML parsing operates on the whole output.
		return output, nil
	}

	return strings.Join(texts, "\n"), nil
}

// parseXMLTitleAndBody parses XML format to extract title and body.
// Expected format: <title>...</title><body>...</body>
// The XML tags may be on separate lines or on the same line.
func parseXMLTitleAndBody(text string) (string, string, error) {
	// Use regex to extract title and body
	// Pattern matches <title>...</title> and <body>...</body> with any content (including newlines).
	// The LAST match wins: plain-text agents (e.g. codex exec) echo the prompt — which
	// itself contains example tags — before their answer.
	titlePattern := regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	bodyPattern := regexp.MustCompile(`(?s)<body>(.*?)</body>`)

	titleIndexes := titlePattern.FindAllStringSubmatchIndex(text, -1)
	bodyIndexes := bodyPattern.FindAllStringSubmatchIndex(text, -1)

	if len(titleIndexes) == 0 {
		return "", "", fmt.Errorf("title tag not found in output")
	}
	if len(bodyIndexes) == 0 {
		return "", "", fmt.Errorf("body tag not found in output")
	}

	// Body: last match. Title: last match that ends before that body's start —
	// literal <title> mentions inside the markdown body are not candidates.
	lastBody := bodyIndexes[len(bodyIndexes)-1]
	body := strings.TrimSpace(text[lastBody[2]:lastBody[3]])
	bodyStart := lastBody[0]
	title := ""
	for _, m := range titleIndexes {
		// m[2]:m[3] spans the captured content; m[0]:m[1] the whole match
		if m[1] <= bodyStart {
			title = strings.TrimSpace(text[m[2]:m[3]])
		}
	}
	if title == "" {
		title = strings.TrimSpace(text[titleIndexes[len(titleIndexes)-1][2]:titleIndexes[len(titleIndexes)-1][3]])
	}

	return title, body, nil
}

// ParseCommitMessage parses the commit message from cursor-agent's output.
// It extracts assistant entries from the JSON stream output and parses XML format.
// Returns commit message, or an error if parsing fails.
func ParseCommitMessage(output string) (string, error) {
	// Extract all assistant entries from the output
	assistantText, err := extractAssistantText(output)
	if err != nil {
		return "", fmt.Errorf("failed to extract assistant text: %w", err)
	}

	if assistantText == "" {
		return "", fmt.Errorf("no assistant text found in output")
	}

	// Parse XML format: <commit_message>...</commit_message>
	commitMsg, err := parseXMLCommitMessage(assistantText)
	if err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	// Validate that commit message is not empty
	commitMsg = strings.TrimSpace(commitMsg)
	if commitMsg == "" {
		return "", fmt.Errorf("commit message is empty after parsing")
	}

	return commitMsg, nil
}

// parseXMLCommitMessage parses XML format to extract commit message.
// Expected format: <commit_message>...</commit_message>
// The XML tags may be on separate lines or on the same line.
func parseXMLCommitMessage(text string) (string, error) {
	// Use regex to extract commit message
	// Pattern matches <commit_message>...</commit_message> with any content (including newlines).
	// The LAST match wins: plain-text agents (e.g. codex exec) echo the prompt — which
	// itself contains example tags — before their answer.
	commitMsgPattern := regexp.MustCompile(`(?s)<commit_message>(.*?)</commit_message>`)

	commitMsgMatches := commitMsgPattern.FindAllStringSubmatch(text, -1)

	if len(commitMsgMatches) == 0 {
		return "", fmt.Errorf("commit_message tag not found in output")
	}

	commitMsg := strings.TrimSpace(commitMsgMatches[len(commitMsgMatches)-1][1])

	return commitMsg, nil
}
