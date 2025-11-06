package utils

import (
	"bytes"
	"unicode/utf8"

	"agentic-automation/internal/config"
)

// Keep a safe margin under MySQL TEXT max (65535 bytes)
const defaultDBOutputLimitBytes = 65520

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

	// If suffix itself exceeds maxBytes, return truncated suffix only
	if len(suffix) >= maxBytes {
		// cut suffix to maxBytes on rune boundary
		cut := maxBytes
		for cut > 0 {
			_, size := utf8.DecodeLastRuneInString(suffix[:cut])
			if size != 1 || suffix[cut-1] < utf8.RuneSelf {
				break
			}
			cut--
		}
		if cut < 0 {
			cut = 0
		}
		return suffix[:cut]
	}

	// We must leave space for suffix so that final length <= maxBytes
	headLimit := maxBytes - len(suffix)
	if headLimit < 0 {
		headLimit = 0
	}

	cut := headLimit
	// ensure we don't cut in the middle of a rune: backtrack until a valid boundary
	for cut > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:cut])
		if r != utf8.RuneError || size != 1 {
			break
		}
		cut--
	}
	if cut < 0 {
		cut = 0
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
