package redact

import (
	"strings"
	"testing"
)

func TestStringMasksRunnerJobSecrets(t *testing.T) {
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
		got := String(tc.in)
		for _, frag := range []string{"abc123def456", "wJalrXUtnFEMI", "-----BEGIN", "abcdef123456", "xyz987uvw654"} {
			if strings.Contains(got, frag) {
				t.Fatalf("%s: secret not redacted: %q", tc.name, got)
			}
		}
	}
}

func TestStringLeavesProseAlone(t *testing.T) {
	in := "token: is required"
	if got := String(in); got != in {
		t.Fatalf("prose was redacted: %q", got)
	}
}

// Usage counters end in the same keywords as credential names but are
// followed by more identifier characters — the keyword boundary must
// keep them intact.
func TestStringLeavesUsageCountersAlone(t *testing.T) {
	for _, in := range []string{
		`{"input_tokens": 12345, "output_tokens": 678}`,
		`"max_tokens": 8192`,
		`"access_token_count": 3`,
		`remaining_tokens 42`,
	} {
		if got := String(in); got != in {
			t.Fatalf("usage counter was redacted: %q -> %q", in, got)
		}
	}
}

// A token longer than the fixed-width prefix must not leak its tail.
func TestStringMasksWholeTokenNotJustPrefix(t *testing.T) {
	for _, in := range []string{
		"ghr_" + strings.Repeat("a", 60),
		"ghp_" + strings.Repeat("z", 50),
		"sk-" + strings.Repeat("a", 60),
	} {
		if got := String(in); got != "***" {
			t.Fatalf("token tail leaked: %q -> %q", in, got)
		}
	}
}

// A base64-looking PEM body line is masked even when a log prefix
// precedes the base64 run on the same line.
func TestStringMasksPrefixedBase64Line(t *testing.T) {
	body := strings.Repeat("T", 64)
	in := "INFO request body=" + body
	if got := String(in); strings.Contains(got, body) {
		t.Fatalf("prefixed base64 leaked: %q", got)
	}
}

// Two qualifying runs sharing a single delimiter must both be masked —
// the first match may not consume the boundary the second run needs.
func TestStringMasksAdjacentBase64Runs(t *testing.T) {
	in := strings.Repeat("A", 64) + " " + strings.Repeat("B", 64)
	if got := String(in); got != "*** ***" {
		t.Fatalf("adjacent base64 runs leaked: %q", got)
	}
}

// Quoted credential values keep whitespace and escaped quotes inside the
// mask — an early stop would leak most of the secret.
func TestStringMasksQuotedCredentialValues(t *testing.T) {
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
		got := String(in)
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
func TestStringMasksPrefixedMultilinePEM(t *testing.T) {
	long := strings.Repeat("Q", 64)
	short := strings.Repeat("x", 32)
	in := "2024-01-01T00:00:00Z -----BEGIN RSA PRIVATE KEY-----\n" +
		"2024-01-01T00:00:00Z " + long + "\n" +
		"2024-01-01T00:00:00Z " + short + "\n" +
		"2024-01-01T00:00:00Z -----END RSA PRIVATE KEY-----"
	got := String(in)
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
// line up with the shipper's format and server-side replay can track
// PEM state across entries.
func TestStringMarkerLineBecomesSentinel(t *testing.T) {
	if got := String("INFO -----BEGIN RSA PRIVATE KEY-----"); got != PEMBeginSentinel {
		t.Fatalf("begin marker not sentineled: %q", got)
	}
	if got := String("-----END RSA PRIVATE KEY----- trailing"); got != PEMEndSentinel {
		t.Fatalf("end marker not sentineled: %q", got)
	}
}

func TestStringMasksPEMBlock(t *testing.T) {
	body := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", 2)[:64]
	in := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----"
	got := String(in)
	for _, frag := range []string{"PRIVATE KEY", body} {
		if strings.Contains(got, frag) {
			t.Fatalf("PEM material leaked: %q", got)
		}
	}
}

// A PEM block emitted with JSONL-escaped newlines (literal \n) stays on one
// line: the BEGIN/END markers alone would leave the key body recoverable.
func TestStringMasksEscapedNewlinePEM(t *testing.T) {
	body := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", 2)[:64]
	in := `{"msg":"-----BEGIN RSA PRIVATE KEY-----\n` + body + `\n-----END RSA PRIVATE KEY-----\n"}`
	got := String(in)
	for _, frag := range []string{"PRIVATE KEY", body, "BEGIN"} {
		if strings.Contains(got, frag) {
			t.Fatalf("escaped PEM material leaked: %q", got)
		}
	}
}

func TestStringMasksBearerAndModernKeys(t *testing.T) {
	for _, in := range []string{
		"Authorization: Bearer abc123tokenXYZ",
		"authorization=bearer+T0k3n.V4lue-xyz",
		"CURSOR_API_KEY = xyz987uvw654",
		`{"o":"{\"OPERATOR_API_TOKEN\":\"secret12345\"}"}`,
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345",
		"sk-proj-AbCdEfGhIjKlMnOpQrStUvWx",
		"sk-oai-0123456789abcdefghij",
	} {
		if got := String(in); got == in {
			t.Fatalf("credential left unmasked: %q -> %q", in, got)
		}
	}
}
