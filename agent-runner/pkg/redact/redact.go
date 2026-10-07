// Package redact masks credentials in agent output before it is persisted
// or shipped to the WebUI log stream.
package redact

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghs_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghu_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`ghr_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{48}`),
	// Credential-looking NAME assignments (NAME=value / NAME: value /
	// "NAME": "value" / NAME value): covers every secret injected into
	// runner jobs — OPERATOR_API_TOKEN, S3_SECRET_ACCESS_KEY,
	// GITHUB_PRIVATE_KEY, ANTHROPIC_API_KEY, NPM_TOKEN, ...
	// The value requires >=4 chars so prose like "token: is required"
	// does not match.
	regexp.MustCompile(`(?i)[A-Za-z0-9_]*(API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)[A-Za-z0-9_]*["']?[=:\s]["'\s]*[^"'\s]{4,}`),
	// Multiline PEM values (e.g. GITHUB_PRIVATE_KEY) spill across lines:
	// mask the BEGIN/END markers and the standalone base64 body lines
	// (64-char columns) between them.
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
	regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?m)^[A-Za-z0-9+/]{60,80}={0,2}$`),
}

// String returns s with known credential patterns replaced by ***.
// Safe on single lines and multiline text alike.
func String(s string) string {
	for _, re := range patterns {
		s = re.ReplaceAllString(s, "***")
	}
	return s
}
