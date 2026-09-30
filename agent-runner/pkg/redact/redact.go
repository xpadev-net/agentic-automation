// Package redact is the common boundary for runner diagnostics and reports.
// It is defense in depth: raw agent/tool streams must not be used as progress logs.
package redact

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const Replacement = "[REDACTED]"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|sk-(?:proj-|ant-)?[A-Za-z0-9_-]{12,})`),
	regexp.MustCompile(`(?i)(?:Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)["']?(?:[A-Za-z0-9_]*(?:api_key|token|password|secret|private_key|access_key)[A-Za-z0-9_]*)["']?\s*[:=]\s*(?:"(?:\\.|[^"\\])*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`(?i)https?://[^\s/@:]+:[^\s/@]+@`),
}

// Text redacts known runtime secret values and common credential formats.
// Additional secrets (such as a client's token) need not be environment-backed.
func Text(value string, additional ...string) string {
	secrets := append([]string(nil), additional...)
	for _, entry := range os.Environ() {
		key, secret, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if ok && len(secret) >= 4 && (strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "API_KEY") || strings.Contains(upper, "PRIVATE_KEY") || strings.Contains(upper, "ACCESS_KEY")) {
			secrets = append(secrets, secret)
		}
	}
	// Longest values first so a shorter prefix cannot reveal the remaining suffix.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		value = strings.ReplaceAll(value, secret, Replacement)
		encoded, _ := json.Marshal(secret)
		if len(encoded) > 2 {
			value = strings.ReplaceAll(value, string(encoded[1:len(encoded)-1]), Replacement)
		}
	}
	for _, pattern := range patterns {
		value = pattern.ReplaceAllString(value, Replacement)
	}
	return value
}

type safeError struct{ err error }

func (e safeError) Error() string { return Text(e.err.Error()) }
func (e safeError) Unwrap() error { return e.err }
func Error(err error) error {
	if err == nil {
		return nil
	}
	return safeError{err}
}

// Fprintf sanitizes a complete diagnostic before any bytes reach the log sink.
func Fprintf(w io.Writer, format string, args ...any) (int, error) {
	return io.WriteString(w, Text(fmt.Sprintf(format, args...)))
}
