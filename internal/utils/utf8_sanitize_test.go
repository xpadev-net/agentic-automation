package utils

import (
	"testing"
	"unicode/utf8"
)

func TestSanitizeUTF8_InvalidBytes(t *testing.T) {
	// 0xE7 alone is invalid start
	in := string([]byte{0x41, 0xE7, 0x42}) // A <invalid> B
	out := SanitizeUTF8(in)
	if !utf8.ValidString(out) {
		t.Fatalf("output should be valid UTF-8, got: %q", out)
	}
}

func TestTruncateWithSuffix_BoundaryRune(t *testing.T) {
	s := "こんにちは" // multibyte
	// choose a byte limit that may cut in the middle of a rune
	out := TruncateWithSuffix(s, 5, "...")
	if !utf8.ValidString(out) {
		t.Fatalf("truncated output should be valid UTF-8, got: %q", out)
	}
}

func TestGetDBOutputLimitBytes_Default(t *testing.T) {
	if v := GetDBOutputLimitBytes(); v <= 0 {
		t.Fatalf("expected positive default, got %d", v)
	}
}
