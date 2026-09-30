package redact

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	// All values are deliberately synthetic fixtures, never real credentials.
	t.Setenv("EXAMPLE_API_KEY", "fixture-runtime-value-1234")
	t.Setenv("GITHUB_PRIVATE_KEY", "fixture line one\nfixture line two")
	cases := []string{
		"fixture-runtime-value-1234",
		`{"access_token":"fixture-json-token"}`,
		"Authorization: Bearer fixture-bearer-token",
		"Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==",
		"https://fixture-user:fixture-password@example.invalid/repo",
		"ghs_fixtureInstallationToken123456789",
		"github_pat_fixturePersonalToken12345",
		"sk-proj-fixtureOpenAIKey123456789",
		"-----BEGIN RSA PRIVATE KEY-----\nfixture-key-data\n-----END RSA PRIVATE KEY-----",
		"-----BEGIN PRIVATE KEY-----\nfixture-truncated-data",
		"fixture line one\nfixture line two",
	}
	for _, input := range cases {
		t.Run(input[:min(len(input), 24)], func(t *testing.T) {
			got := Text(input)
			if got == input || strings.Contains(got, "fixture-") || strings.Contains(got, "fixture line") {
				t.Fatalf("fixture was not redacted: %q", got)
			}
		})
	}
	encoded, _ := json.Marshal("fixture line one\nfixture line two")
	if strings.Contains(Text(string(encoded)), "fixture line") {
		t.Fatal("JSON-escaped runtime secret was not redacted")
	}
	if got := Text("exit status 1: compilation failed"); got != "exit status 1: compilation failed" {
		t.Fatalf("ordinary diagnostic changed: %q", got)
	}
}

func TestFprintfRedactsBeforeWriting(t *testing.T) {
	var out bytes.Buffer
	t.Setenv("TEST_SECRET", "fixture-boundary-secret")
	_, err := Fprintf(&out, "failure: %s\n", "fixture-boundary-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "fixture-boundary-secret") {
		t.Fatal("secret reached sink")
	}
}
