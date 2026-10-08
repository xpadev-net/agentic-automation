// Package redact masks credentials in agent output before it is persisted
// or shipped to the WebUI log stream.
package redact

import (
	"regexp"
	"strings"
)

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
	// Credential-looking NAME assignments (NAME=value / NAME: value /
	// "NAME": "value" / NAME value): covers every secret injected into
	// runner jobs — OPERATOR_API_TOKEN, S3_SECRET_ACCESS_KEY,
	// GITHUB_PRIVATE_KEY, ANTHROPIC_API_KEY, NPM_TOKEN, ...
	// The name must END at a keyword boundary (\b) so usage counters
	// like "input_tokens": 12345 or "max_tokens": 8192 survive; the
	// separator accepts =, :, or any whitespace (tabs/newlines
	// included), plus whitespace AFTER it before the value starts
	// (`KEY= "v"`, `{"KEY": "v"}`) — without consuming it the
	// quoted alternatives can't anchor and only the first word is
	// masked. The value is either a quoted string (whitespace and
	// escaped delimiters included, matching quote required, optional
	// backslash-escaped quotes for JSONL) or an unquoted run of >=4
	// chars so prose like "token: is required" does not match.
	regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"']*\s*[=:\s]\s*(?:\\?"(?:[^"\\]|\\.)*\\?"|\\?'(?:[^'\\]|\\.)*\\?'|["'\\]*[^"'\s]{4,})`),
}

// pemComplete collapses an entire PEM block — JSONL escaped-newline
// single-line form and real multiline blocks alike ([^-] spans newlines
// because PEM base64 never contains '-'). Runs before the line pass so
// well-formed blocks never reach the BEGIN/END sentinel logic.
var pemComplete = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[^-]*-----END [A-Z0-9 ]*PRIVATE KEY-----`)

var (
	pemBeginMarker = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	pemEndMarker   = regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY-----`)
)

// Sentinel lines shared with the shipper and the ingest handler: a stored
// row equal to one of these marks the boundary of a masked PEM range, so
// server-side replay can keep masking body lines (including short or
// prefixed ones) that Line() alone cannot see across calls.
const (
	PEMBeginSentinel = "[REDACTED PRIVATE KEY BEGIN]"
	PEMEndSentinel   = "[REDACTED PRIVATE KEY END]"
	RedactedLine     = "[REDACTED]"
)

// base64Run matches PEM body material inside a line: a >=60-char run of
// base64 characters delimited by non-base64 characters or line edges.
var base64Run = regexp.MustCompile(`(?m)(^|[^A-Za-z0-9+/])[A-Za-z0-9+/]{60,}={0,2}($|[^A-Za-z0-9+/=])`)

// scrubPEMBlocks walks the text line by line, tracking PEM state. A line
// containing a BEGIN marker becomes PEMBeginSentinel, every line inside
// the block becomes RedactedLine, and the closing marker becomes
// PEMEndSentinel. Whole-line replacement keeps stored rows identical to
// the shipper's sentinels, letting the ingest-side state replay recognize
// them. An unterminated BEGIN masks to the end of the input (fail closed).
func scrubPEMBlocks(s string) string {
	if !strings.Contains(s, "PRIVATE KEY-----") {
		return s
	}
	lines := strings.Split(s, "\n")
	inPEM := false
	for i, ln := range lines {
		hasBegin := pemBeginMarker.MatchString(ln)
		hasEnd := pemEndMarker.MatchString(ln)
		switch {
		case hasBegin && hasEnd:
			// A complete block on one line that pemComplete could not
			// collapse — mask the whole line and do not leak state.
			lines[i] = "***"
			inPEM = false
		case hasBegin:
			lines[i] = PEMBeginSentinel
			inPEM = true
		case hasEnd:
			lines[i] = PEMEndSentinel
			inPEM = false
		case inPEM:
			lines[i] = RedactedLine
		}
	}
	return strings.Join(lines, "\n")
}

// scrubBase64Runs masks >=60-char base64 runs. Replacement runs to a
// fixpoint because a match consumes the shared delimiter that the next
// adjacent run needs as its leading boundary.
func scrubBase64Runs(s string) string {
	for {
		next := base64Run.ReplaceAllString(s, "${1}***${2}")
		if next == s {
			return s
		}
		s = next
	}
}

// String returns s with known credential patterns replaced by ***.
// Safe on single lines and multiline text alike; PEM state is tracked
// across lines inside the input.
func String(s string) string {
	s = pemComplete.ReplaceAllString(s, "***")
	s = scrubPEMBlocks(s)
	for _, re := range patterns {
		s = re.ReplaceAllString(s, "***")
	}
	return scrubBase64Runs(s)
}
