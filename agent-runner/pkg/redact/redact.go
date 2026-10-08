// Package redact masks credentials in agent output before it is persisted
// or shipped to the WebUI log stream.
package redact

import "regexp"

var patterns = []*regexp.Regexp{
	// GitHub tokens — open-ended so a longer token (e.g. refresh tokens)
	// can't leak its tail through a fixed-width prefix match.
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
	// sk- keys — the modern variable-length class covers the legacy
	// 48-char form too, so no fixed-width prefix can match first and
	// leave a longer key's tail exposed.
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	// Bearer authorization headers — agent output can echo the runner's
	// OPERATOR_API_TOKEN or GitHub tokens inside HTTP traces.
	regexp.MustCompile(`(?i)bearer[\s:=+]+[A-Za-z0-9._~+/-]{8,}`),
	// A complete PEM block — agents printing JSONL emit the private key
	// with newlines escaped as literal \n; [^-] also spans raw newlines
	// (PEM base64 never contains '-'), so multiline blocks match too.
	// Runs before the bare marker patterns so no partial match can
	// destroy the BEGIN/END markers.
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[^-]*-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	// Credential-looking NAME assignments (NAME=value / NAME: value /
	// "NAME": "value" / NAME value): covers every secret injected into
	// runner jobs — OPERATOR_API_TOKEN, S3_SECRET_ACCESS_KEY,
	// GITHUB_PRIVATE_KEY, ANTHROPIC_API_KEY, NPM_TOKEN, ...
	// The name must END at a keyword boundary (\b) so usage counters
	// like "input_tokens": 12345 or "max_tokens": 8192 survive; the
	// separator accepts =, :, or any whitespace (tabs/newlines
	// included); the value requires >=4 chars so prose like
	// "token: is required" does not match; backslash-escaped quotes are
	// tolerated so JSONL-escaped assignments (\"KEY\":\"value\") still
	// match.
	regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"']*\s*[=:\s]["'\\\s]*[^"'\s]{4,}`),
	// Multiline PEM values (e.g. GITHUB_PRIVATE_KEY) spill across lines:
	// mask the BEGIN/END markers and any base64 body run (even when a
	// log prefix precedes it). The complete-block pattern above already
	// handles both the escaped-newline single-line form and real
	// multiline blocks.
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
	regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY-----`),
}

// base64Run matches PEM body material inside a line: a >=60-char run of
// base64 characters delimited by non-base64 characters or line edges.
// It needs its own replacement (boundary groups preserved), so it lives
// outside the uniform *** list.
var base64Run = regexp.MustCompile(`(?m)(^|[^A-Za-z0-9+/])[A-Za-z0-9+/]{60,}={0,2}($|[^A-Za-z0-9+/=])`)

// String returns s with known credential patterns replaced by ***.
// Safe on single lines and multiline text alike.
func String(s string) string {
	for _, re := range patterns {
		s = re.ReplaceAllString(s, "***")
	}
	return base64Run.ReplaceAllString(s, "${1}***${2}")
}
