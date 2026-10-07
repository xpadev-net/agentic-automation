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
