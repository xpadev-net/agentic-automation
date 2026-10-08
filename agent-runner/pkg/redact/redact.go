// Package redact masks credentials in agent output before it is persisted
// or shipped to the WebUI log stream.
package redact

import (
	"fmt"
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
	// (one or more backslashes, for JSONL/doubly-encoded JSON; a
	// `\\{2,}"` run inside is an interior escaped quote — a deeper
	// encoding — so only a single-backslash `\"` closes it),
	// a plain quoted string, or an unquoted run of >=4 chars so prose
	// like "token: is required" does not match.
	regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"']*\s*[=:\s]\s*(?:\\+"(?:[^"\\]|\\+[^"\\]|\\{2,}")*\\+"|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|["'\\]*[^"'\s]{4,})`),
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

// credKeyTail matches a line that ENDS at a credential keyword plus
// trailing separators — `CURSOR_API_KEY` alone, `KEY=`, `"KEY":`. The
// assignment pattern's separator class includes newline, so in multiline
// String() `KEY\nvalue` masks both lines; line-by-line callers must
// carry the same state, which is what Stream does.
var credKeyTail = regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"':=\s]*$`)

// credKeyOpenQuote matches `KEY <sep> <quote>` where the value opens a
// quote; when no matching close exists on the same line the secret runs
// into following lines.
var credKeyOpenQuote = regexp.MustCompile(`(?i)[A-Za-z0-9_]*(?:API_KEY|TOKEN|SECRET|PASSWORD|PRIVATE_KEY|_KEY)\b[\\"']*\s*[=:\s]\s*(\\*["'])`)

// Stream carries credential state across consecutive lines fed through
// Line: a bare `KEY` (or `KEY=`) masks the NEXT line entirely, and a
// `KEY="...` value left unclosed masks through the line that closes it.
// Shipper and ingest-side replay share these semantics — PEM sentinels
// are tracked separately (they survive as their own stored lines).
type Stream struct {
	pendingValue bool // previous line ended at a bare credential key
	openQuote    byte // '"' or '\'' — quote of an unclosed KEY= value; 0 = none
	openEscCount int  // backslashes before the open quote (0 = plain)
}

// SeedPending primes cross-line state from a previously processed line:
// a bare credential key there means the next line continues its value.
// (An open quote can't be recovered — its lines were masked to "***" —
// so only the pending-key case can resume; persisted rows carry the full
// State() instead.)
func (s *Stream) SeedPending(prev string) {
	if credKeyTail.MatchString(prev) {
		s.pendingValue = true
	}
}

// State encodes the live cross-line state so a caller can persist it per
// stored row and restore it exactly in a later batch. "p" = pending
// value, "q<quote>:<esc>" = open quote; combined as "p;q34:1" when both
// hold. "" = idle.
func (s *Stream) State() string {
	var st string
	if s.pendingValue {
		st = "p"
	}
	if s.openQuote != 0 {
		if st != "" {
			st += ";"
		}
		st += fmt.Sprintf("q%d:%d", s.openQuote, s.openEscCount)
	}
	return st
}

// SetState restores a State() encoding; unknown input leaves the stream
// idle (fail open is fine — the stored lines themselves were masked).
func (s *Stream) SetState(st string) {
	for _, part := range strings.Split(st, ";") {
		if part == "p" {
			s.pendingValue = true
			continue
		}
		if strings.HasPrefix(part, "q") {
			var q, esc int
			if n, _ := fmt.Sscanf(part[1:], "%d:%d", &q, &esc); n == 2 && q > 0 && q < 256 {
				s.openQuote = byte(q)
				s.openEscCount = esc
			}
		}
	}
}

// Line redacts one line like String, plus cross-line credential state.
// Pass lines in stream order; sentinel lines pass through untouched
// (they never contain the keyword tail).
func (s *Stream) Line(raw string) string {
	if s.openQuote != 0 {
		if i := indexCloseQuote(raw, s.openQuote, s.openEscCount); i >= 0 {
			s.openQuote = 0
			s.openEscCount = 0
			// The retained suffix can hold further credentials — even a
			// new unclosed value — so run it through the same path.
			return "***" + s.Line(raw[i+1:])
		}
		return "***"
	}
	if s.pendingValue {
		s.pendingValue = false
		return "***"
	}
	// Every credential assignment on the line must be checked for an
	// unclosed quote — a closed first match (`FIRST="ok" PASS="x`) can't
	// hide a leaking later one. Runs before String so the whole tail
	// stays masked even when the fragment is short.
	for _, loc := range credKeyOpenQuote.FindAllStringSubmatchIndex(raw, -1) {
		qEnd := loc[3]
		esc := qEnd - 1 - loc[2] // backslashes preceding the quote char
		if indexCloseQuote(raw[qEnd:], raw[qEnd-1], esc) < 0 {
			s.openQuote = raw[qEnd-1]
			s.openEscCount = esc
			// The retained prefix can hold its own credentials.
			return String(raw[:loc[2]]) + "***"
		}
	}
	out := String(raw)
	if credKeyTail.MatchString(out) {
		s.pendingValue = true
	}
	return out
}

// indexCloseQuote finds where an open quote closes in s, returning the
// index of the quote char. escCount is the backslash run that preceded
// the OPENING quote: for a plain quote (0) the close is a quote not
// preceded by an odd backslash run; for an escaped open the close needs
// the same run length — a longer run (e.g. `\\\"` under a `\"` opener)
// is an interior escaped quote at a deeper encoding, not the delimiter.
func indexCloseQuote(s string, quote byte, escCount int) int {
	for i := 0; i < len(s); i++ {
		if s[i] != quote {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			bs++
		}
		if escCount == 0 {
			if bs%2 == 0 {
				return i
			}
		} else if bs == escCount {
			return i
		}
	}
	return -1
}
