// Package redact masks credentials in agent output before it is persisted
// or shipped to the WebUI log stream.
package redact

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghs_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{48}`),
	regexp.MustCompile(`ANTHROPIC_API_KEY[=:\s]+[A-Za-z0-9-_]+`),
	regexp.MustCompile(`CURSOR_API_KEY[=:\s]+[A-Za-z0-9-_]+`),
	regexp.MustCompile(`CODEX_API_KEY[=:\s]+[A-Za-z0-9-_]+`),
	regexp.MustCompile(`OPENAI_API_KEY[=:\s]+[A-Za-z0-9-_]+`),
}

// String returns s with known credential patterns replaced by ***.
// Safe on single lines and multiline text alike.
func String(s string) string {
	for _, re := range patterns {
		s = re.ReplaceAllString(s, "***")
	}
	return s
}
