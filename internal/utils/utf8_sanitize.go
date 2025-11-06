package utils

import (
	"bytes"
	"unicode/utf8"

	"agentic-automation/internal/config"
)

const defaultDBOutputLimitBytes = 64 * 1024

// SanitizeUTF8 converts invalid UTF-8 sequences into the replacement character.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string(bytes.ToValidUTF8([]byte(s), []byte("�")))
}

// TruncateWithSuffix truncates the input to maxBytes and appends suffix if truncated.
func TruncateWithSuffix(s string, maxBytes int, suffix string) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	// ensure we don't cut in the middle of a rune
	for cut > 0 && (s[cut-1]&0xC0) == 0x80 {
		cut--
	}
	if cut <= 0 {
		cut = maxBytes
	}
	return s[:cut] + suffix
}

// GetDBOutputLimitBytes returns the configured DB output size limit in bytes.
func GetDBOutputLimitBytes() int {
	v := config.GetEnvInt("AGENT_OUTPUT_DB_LIMIT_BYTES", defaultDBOutputLimitBytes)
	if v <= 0 {
		return defaultDBOutputLimitBytes
	}
	return v
}
