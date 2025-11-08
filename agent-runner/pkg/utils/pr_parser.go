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

// extractAssistantText extracts all assistant entry text from cursor-agent's JSON stream output.
// It parses each line as JSON and combines all assistant entry messages.
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

	return strings.Join(texts, "\n"), nil
}

// parseXMLTitleAndBody parses XML format to extract title and body.
// Expected format: <title>...</title><body>...</body>
// The XML tags may be on separate lines or on the same line.
func parseXMLTitleAndBody(text string) (string, string, error) {
	// Use regex to extract title and body
	// Pattern matches <title>...</title> and <body>...</body> with any content (including newlines)
	titlePattern := regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	bodyPattern := regexp.MustCompile(`(?s)<body>(.*?)</body>`)

	titleMatch := titlePattern.FindStringSubmatch(text)
	bodyMatch := bodyPattern.FindStringSubmatch(text)

	if len(titleMatch) < 2 {
		return "", "", fmt.Errorf("title tag not found in output")
	}
	if len(bodyMatch) < 2 {
		return "", "", fmt.Errorf("body tag not found in output")
	}

	title := strings.TrimSpace(titleMatch[1])
	body := strings.TrimSpace(bodyMatch[1])

	return title, body, nil
}
