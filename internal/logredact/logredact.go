// Package logredact masks credentials in ingested runner logs before they
// are persisted and published to the WebUI. Pattern list mirrors
// agent-runner/pkg/redact; the separate operator module keeps its own copy
// so third-party shippers get the same protection.
//
// Line is per-line: BEGIN/END marker lines are rewritten to the shared
// sentinel rows so the ingest handler can replay PEM state across stored
// entries and mask the body lines in between (including short or
// prefixed ones this per-line view cannot see).
package logredact

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
	// masked. The value is either a backslash-escaped quoted string
	// (one or more backslashes, for JSONL/doubly-encoded JSON; the
	// interior stops at the first backslash-run+quote so a closing
	// `\"` is never eaten as an escape and trailing JSON survives),
	// a plain quoted string, or an unquoted run of >=4 chars so prose
	// like "token: is required" does not match.
	regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"']*\s*[=:\s]\s*(?:\\+"(?:[^"\\]|\\+[^"\\])*\\+"|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|["'\\]*[^"'\s]{4,})`),
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

// Line returns s with known credential patterns replaced by *** (or the
// shared sentinel rows for PEM boundary lines).
func Line(s string) string {
	s = pemComplete.ReplaceAllString(s, "***")
	s = scrubPEMBlocks(s)
	for _, re := range patterns {
		s = re.ReplaceAllString(s, "***")
	}
	return scrubBase64Runs(s)
}

// credKeyTail matches a line that ENDS at a credential keyword plus
// trailing separators — `CURSOR_API_KEY` alone, `KEY=`, `"KEY":`. The
// assignment pattern's separator class includes newline, so in multiline
// Line() `KEY\nvalue` masks both lines; line-by-line callers must carry
// the same state, which is what Stream does.
var credKeyTail = regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"':=\s]*$`)

// credKeyOpenQuote matches `KEY <sep> <quote>` where the value opens a
// quote; when no matching close exists on the same line the secret runs
// into following lines.
var credKeyOpenQuote = regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"']*\s*[=:\s]\s*(\\*["'])`)

// Stream carries credential state across consecutive lines fed through
// Line: a bare `KEY` (or `KEY=`) masks the NEXT line entirely, and a
// `KEY="...` value left unclosed masks through the line that closes it.
// Mirrors agent-runner/pkg/redact.Stream; PEM sentinels are tracked
// separately (they survive as their own stored lines).
type Stream struct {
	pendingValue bool // previous line ended at a bare credential key
	openQuote    byte // '"' or '\'' — quote of an unclosed KEY= value; 0 = none
	openEscaped  bool // the open quote had a leading backslash (\"…)
}

// SeedPending primes cross-line state from the previously stored line: a
// bare credential key there means the next stored line continues its
// value. (An open quote can't be recovered — its lines were masked to
// "***" on the way in — so only the pending-key case can resume.)
func (s *Stream) SeedPending(prev string) {
	if credKeyTail.MatchString(prev) {
		s.pendingValue = true
	}
}

// Line redacts one line like the package-level Line, plus cross-line
// credential state. Pass lines in stream order.
func (s *Stream) Line(raw string) string {
	if s.openQuote != 0 {
		if i := indexCloseQuote(raw, s.openQuote, s.openEscaped); i >= 0 {
			s.openQuote = 0
			return "***" + raw[i+1:]
		}
		return "***"
	}
	if s.pendingValue {
		s.pendingValue = false
		return "***"
	}
	// KEY= with an unclosed quote on this line: mask from the quote and
	// remember the delimiter. Runs before Line because the unquoted
	// fallback would otherwise mask only the first fragment's tail.
	if loc := credKeyOpenQuote.FindStringSubmatchIndex(raw); loc != nil {
		if qEnd := loc[3]; indexCloseQuote(raw[qEnd:], raw[qEnd-1], qEnd-1 > loc[2]) < 0 {
			s.openQuote = raw[qEnd-1]
			s.openEscaped = qEnd-1 > loc[2]
			return raw[:loc[2]] + "***"
		}
	}
	out := Line(raw)
	if credKeyTail.MatchString(out) {
		s.pendingValue = true
	}
	return out
}

// indexCloseQuote finds where an open quote closes in s. For a plain
// quote the close is the quote char not preceded by an odd number of
// backslashes; for a backslash-escaped open the close is the matching
// escaped pair (index of the quote char is returned).
func indexCloseQuote(s string, quote byte, escaped bool) int {
	for i := 0; i < len(s); i++ {
		if s[i] != quote {
			continue
		}
		if escaped {
			if i > 0 && s[i-1] == '\\' {
				return i
			}
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return i
		}
	}
	return -1
}
