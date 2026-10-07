// Package logredact masks credentials in ingested runner logs before they
// are persisted and published to the WebUI. Pattern list mirrors
// agent-runner/pkg/redact; the separate operator module keeps its own copy
// so third-party shippers get the same protection.
package logredact

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

// Line returns s with known credential patterns replaced by ***.
func Line(s string) string {
	for _, re := range patterns {
		s = re.ReplaceAllString(s, "***")
	}
	return s
}
