package logredact

import (
	"strings"
	"testing"
)

func TestLineMasksRunnerJobSecrets(t *testing.T) {
	cases := []struct{ name, in string }{
		{"operator token env", "OPERATOR_API_TOKEN=abc123def456"},
		{"s3 secret env", "S3_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG"},
		{"github private key env", "GITHUB_PRIVATE_KEY=-----BEGIN"},
		{"json style", `"npm_token": "abcdef123456"`},
		{"space separated", "CURSOR_API_KEY xyz987uvw654"},
		{"tab separated", "CURSOR_API_KEY\txyz987uvw654"},
		{"newline separated", "CURSOR_API_KEY\nxyz987uvw654"},
	}
	for _, tc := range cases {
		got := Line(tc.in)
		for _, frag := range []string{"abc123def456", "wJalrXUtnFEMI", "-----BEGIN", "abcdef123456", "xyz987uvw654"} {
			if strings.Contains(got, frag) {
				t.Fatalf("%s: secret not redacted: %q", tc.name, got)
			}
		}
	}
}

func TestLineLeavesProseAlone(t *testing.T) {
	in := "token: is required"
	if got := Line(in); got != in {
		t.Fatalf("prose was redacted: %q", got)
	}
}

// Usage counters end in the same keywords as credential names but are
// followed by more identifier characters — the keyword boundary must
// keep them intact.
func TestLineLeavesUsageCountersAlone(t *testing.T) {
	for _, in := range []string{
		`{"input_tokens": 12345, "output_tokens": 678}`,
		`"max_tokens": 8192`,
		`"access_token_count": 3`,
		`remaining_tokens 42`,
	} {
		if got := Line(in); got != in {
			t.Fatalf("usage counter was redacted: %q -> %q", in, got)
		}
	}
}

// A token longer than the fixed-width prefix must not leak its tail.
func TestLineMasksWholeTokenNotJustPrefix(t *testing.T) {
	for _, in := range []string{
		"ghr_" + strings.Repeat("a", 60),
		"ghp_" + strings.Repeat("z", 50),
		"sk-" + strings.Repeat("a", 60),
	} {
		if got := Line(in); got != "***" {
			t.Fatalf("token tail leaked: %q -> %q", in, got)
		}
	}
}

// A base64-looking PEM body line is masked even when a log prefix
// precedes the base64 run on the same line.
func TestLineMasksPrefixedBase64Line(t *testing.T) {
	body := strings.Repeat("T", 64)
	in := "INFO request body=" + body
	if got := Line(in); strings.Contains(got, body) {
		t.Fatalf("prefixed base64 leaked: %q", got)
	}
}

// Two qualifying runs sharing a single delimiter must both be masked —
// the first match may not consume the boundary the second run needs.
func TestLineMasksAdjacentBase64Runs(t *testing.T) {
	in := strings.Repeat("A", 64) + " " + strings.Repeat("B", 64)
	if got := Line(in); got != "*** ***" {
		t.Fatalf("adjacent base64 runs leaked: %q", got)
	}
}

// Quoted credential values keep whitespace and escaped quotes inside the
// mask — an early stop would leak most of the secret.
func TestLineMasksQuotedCredentialValues(t *testing.T) {
	for _, in := range []string{
		`PASSWORD="correct horse battery staple"`,
		`SESSION_SECRET='multi word secret'`,
		`{"API_KEY":"value with spaces"}`,
		// Whitespace after the separator must not defeat the quoted
		// alternatives (Codex finding: only the first word was masked).
		`PASSWORD= "correct horse battery staple"`,
		`SESSION_SECRET:   'multi word secret'`,
		`{"API_KEY": "value with spaces"}`,
	} {
		got := Line(in)
		for _, frag := range []string{"horse", "battery", "word secret", "with spaces"} {
			if strings.Contains(got, frag) {
				t.Fatalf("quoted value leaked: %q -> %q", in, got)
			}
		}
	}
}

// A PEM block whose lines carry timestamp prefixes defeats the
// complete-block pattern; the line pass must still mask every body line,
// including a short final line below the base64 threshold.
func TestLineMasksPrefixedMultilinePEM(t *testing.T) {
	long := strings.Repeat("Q", 64)
	short := strings.Repeat("x", 32)
	in := "2024-01-01T00:00:00Z -----BEGIN RSA PRIVATE KEY-----\n" +
		"2024-01-01T00:00:00Z " + long + "\n" +
		"2024-01-01T00:00:00Z " + short + "\n" +
		"2024-01-01T00:00:00Z -----END RSA PRIVATE KEY-----"
	got := Line(in)
	for _, frag := range []string{long, short, "-----BEGIN", "-----END"} {
		if strings.Contains(got, frag) {
			t.Fatalf("prefixed PEM material leaked: %q", got)
		}
	}
	if !strings.Contains(got, PEMBeginSentinel) || !strings.Contains(got, PEMEndSentinel) {
		t.Fatalf("sentinels missing: %q", got)
	}
}

// A lone marker line is rewritten to the shared sentinel so stored rows
// line up with the shipper's format and the ingest handler's sentinel
// replay can track PEM state across entries.
func TestLineMarkerLineBecomesSentinel(t *testing.T) {
	if got := Line("INFO -----BEGIN RSA PRIVATE KEY-----"); got != PEMBeginSentinel {
		t.Fatalf("begin marker not sentineled: %q", got)
	}
	if got := Line("-----END RSA PRIVATE KEY----- trailing"); got != PEMEndSentinel {
		t.Fatalf("end marker not sentineled: %q", got)
	}
}

func TestLineMasksPEMBlock(t *testing.T) {
	body := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", 2)[:64]
	in := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----"
	got := Line(in)
	for _, frag := range []string{"PRIVATE KEY", body} {
		if strings.Contains(got, frag) {
			t.Fatalf("PEM material leaked: %q", got)
		}
	}
}

// A PEM block emitted with JSONL-escaped newlines (literal \n) stays on one
// line: the BEGIN/END markers alone would leave the key body recoverable.
func TestLineMasksEscapedNewlinePEM(t *testing.T) {
	body := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", 2)[:64]
	in := `{"msg":"-----BEGIN RSA PRIVATE KEY-----\n` + body + `\n-----END RSA PRIVATE KEY-----\n"}`
	got := Line(in)
	for _, frag := range []string{"PRIVATE KEY", body, "BEGIN"} {
		if strings.Contains(got, frag) {
			t.Fatalf("escaped PEM material leaked: %q", got)
		}
	}
}

func TestLineMasksBearerAndModernKeys(t *testing.T) {
	for _, in := range []string{
		"Authorization: Bearer abc123tokenXYZ",
		"authorization=bearer+T0k3n.V4lue-xyz",
		"CURSOR_API_KEY = xyz987uvw654",
		`{"o":"{\"OPERATOR_API_TOKEN\":\"secret12345\"}"}`,
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345",
		"sk-proj-AbCdEfGhIjKlMnOpQrStUvWx",
		"sk-oai-0123456789abcdefghij",
	} {
		if got := Line(in); got == in {
			t.Fatalf("credential left unmasked: %q -> %q", in, got)
		}
	}
}

// The escaped-quote alternative must stop at the matching \" delimiter:
// eating it as an interior escape ran on to the next plain quote and
// swallowed unrelated JSON diagnostics.
func TestLineStopsEscapedQuoteAtDelimiter(t *testing.T) {
	in := `{"output":"PASSWORD=\"correct horse\"\nfailed to load configuration"}`
	got := Line(in)
	if strings.Contains(got, "correct horse") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "failed to load configuration") {
		t.Fatalf("trailing diagnostics swallowed: %q", got)
	}
}

// Doubly-encoded JSON triples the quote escapes; the whole secret must
// still mask rather than falling back to the unquoted run.
func TestLineMasksMultiplyEscapedQuotedCredential(t *testing.T) {
	for _, in := range []string{
		`PASSWORD=\\\"correct horse battery staple\\\"`,
		`{"API_KEY":\\\"multi word secret\\\"}`,
	} {
		got := Line(in)
		for _, frag := range []string{"horse", "battery", "word secret"} {
			if strings.Contains(got, frag) {
				t.Fatalf("multiply-escaped secret leaked: %q -> %q", in, got)
			}
		}
	}
}

func TestStreamMasksValueOnNextLine(t *testing.T) {
	var s Stream
	if got := s.Line("CURSOR_API_KEY"); got != "CURSOR_API_KEY" {
		t.Fatalf("bare key line changed: %q", got)
	}
	if got := s.Line("xyz987uvw654"); got != "***" {
		t.Fatalf("continuation value not masked: %q", got)
	}
	if got := s.Line("following line"); got != "following line" {
		t.Fatalf("state leaked past one line: %q", got)
	}
}

func TestStreamMasksMultilineQuotedValue(t *testing.T) {
	var s Stream
	if got := s.Line(`PASSWORD="correct`); got != "PASSWORD=***" {
		t.Fatalf("open fragment not masked: %q", got)
	}
	if got := s.Line(`horse battery`); got != "***" {
		t.Fatalf("interior line not masked: %q", got)
	}
	if got := s.Line(`staple" trailing`); got != "*** trailing" {
		t.Fatalf("closing line: want suffix kept, got %q", got)
	}
	if got := s.Line("clean"); got != "clean" {
		t.Fatalf("state leaked past close: %q", got)
	}
}

func TestMasksEscapedQuotedValueWithInteriorEscapedQuote(t *testing.T) {
	got := Line(`PASSWORD=\"correct \\\"horse\\\" battery staple\" tail`)
	want := `*** tail`
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestStreamMasksRetainedPrefixBeforeUnclosedQuote(t *testing.T) {
	s := Stream{}
	got := s.Line(`OPERATOR_API_TOKEN=abc123def456 PASSWORD="correct`)
	want := `*** PASSWORD=***`
	if got != want {
		t.Fatalf("prefix: want %q, got %q", want, got)
	}
	got = s.Line(`staple" OPERATOR_API_TOKEN=abc123def456`)
	want = `*** ***`
	if got != want {
		t.Fatalf("suffix: want %q, got %q", want, got)
	}
}

func TestStreamFindsUnclosedQuoteAfterClosedAssignment(t *testing.T) {
	s := Stream{}
	got := s.Line(`FIRST_TOKEN="safe" PASSWORD="correct`)
	want := `*** PASSWORD=***`
	if got != want {
		t.Fatalf("line1: want %q, got %q", want, got)
	}
	if got := s.Line("horse battery"); got != "***" {
		t.Fatalf("line2: want %q, got %q", "***", got)
	}
	if got := s.Line(`staple" done`); got != "*** done" {
		t.Fatalf("line3: want %q, got %q", "*** done", got)
	}
}

func TestStreamEscapedQuoteTracksEncodingDepth(t *testing.T) {
	s := Stream{}
	got := s.Line(`PASSWORD=\"correct`)
	want := `PASSWORD=***`
	if got != want {
		t.Fatalf("line1: want %q, got %q", want, got)
	}
	if got := s.Line(`interior \\\" still masked`); got != "***" {
		t.Fatalf("line2: want %q, got %q", "***", got)
	}
	if got := s.Line(`closer \" free`); got != "*** free" {
		t.Fatalf("line3: want %q, got %q", "*** free", got)
	}
}

func TestStreamStateRoundTrip(t *testing.T) {
	s := Stream{}
	s.Line("CURSOR_API_KEY")
	if st := s.State(); st != "p" {
		t.Fatalf("pending state: want p, got %q", st)
	}
	s2 := Stream{}
	s2.Line(`MY_API_KEY="unclosed`)
	st := s2.State()
	if st == "" || st == "p" {
		t.Fatalf("open state not encoded: %q", st)
	}
	s3 := Stream{}
	s3.SetState(st)
	if got := s3.Line("inside value"); got != "***" {
		t.Fatalf("restored open state did not mask: %q", got)
	}
}

func TestMasksUnterminatedQuotedCredential(t *testing.T) {
	got := Line(`PASSWORD="correct horse battery staple`)
	want := `PASSWORD=***`
	if got != want {
		t.Fatalf("single-line: want %q, got %q", want, got)
	}
	got = Line("line1\n" + `PASSWORD="unclosed a` + "\nb")
	want = "line1\nPASSWORD=***"
	if got != want {
		t.Fatalf("multiline: want %q, got %q", want, got)
	}
	got = Line(`FIRST_TOKEN="ok" PASSWORD="tail`)
	want = `*** PASSWORD=***`
	if got != want {
		t.Fatalf("mixed: want %q, got %q", want, got)
	}
}

func TestStreamPendingQuotedContinuation(t *testing.T) {
	s := Stream{}
	if got := s.Line("PASSWORD="); got != "PASSWORD=" {
		t.Fatalf("key line: %q", got)
	}
	if got := s.Line(`"correct`); got != "***" {
		t.Fatalf("open: %q", got)
	}
	if got := s.Line("horse battery"); got != "***" {
		t.Fatalf("body: %q", got)
	}
	if got := s.Line(`staple" after`); got != "*** after" {
		t.Fatalf("close: %q", got)
	}
}

func TestStreamSetStateReplaces(t *testing.T) {
	s := Stream{}
	s.SetState("p")
	s.SetState("")
	if got := s.Line("ordinary line"); got != "ordinary line" {
		t.Fatalf("idle restore still masked: %q", got)
	}
	s2 := Stream{}
	s2.SetState("q34:0")
	s2.SetState("")
	if got := s2.Line("next"); got != "next" {
		t.Fatalf("quote state lingered: %q", got)
	}
}
