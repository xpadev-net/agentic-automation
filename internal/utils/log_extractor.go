package utils

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var importantLinePattern = regexp.MustCompile(`(?i)(error|fatal|panic|exception|traceback|exit\s*code|command\s*failed)`)

func ExtractErrorSummary(allLogs string, maxLines, maxBytes int) (string, string) {
	normalized := normalizeNewlines(allLogs)
	lines := strings.Split(normalized, "\n")

	important := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := normalizeLine(line)
		if trimmed == "" {
			continue
		}
		if importantLinePattern.MatchString(trimmed) {
			important = append(important, trimmed)
		}
	}

	if len(important) == 0 {
		// fallback: take last few lines as summary when nothing matched
		tail := tailLines(lines, minInt(maxLines, 10))
		summary := trimUTF8Bytes(strings.Join(tail, "\n"), maxBytes)
		excerpt := trimUTF8Bytes(strings.Join(tailLines(lines, LogExcerptMaxLines), "\n"), LogExcerptMaxBytes)
		return summary, excerpt
	}

	if len(important) > maxLines {
		important = important[:maxLines]
	}
	summary := trimUTF8Bytes(strings.Join(important, "\n"), maxBytes)
	excerpt := trimUTF8Bytes(strings.Join(tailLines(lines, LogExcerptMaxLines), "\n"), LogExcerptMaxBytes)
	return summary, excerpt
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func normalizeLine(s string) string {
	// collapse tabs and multiple spaces lightly; remove control chars except tab
	b := strings.Builder{}
	for _, r := range s {
		if r == '\t' || r == '\n' {
			b.WriteRune(r)
			continue
		}
		if r < 32 {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	// collapse consecutive spaces
	out = spaceCollapse(out)
	return out
}

func spaceCollapse(s string) string {
	var prevSpace bool
	b := strings.Builder{}
	for _, r := range s {
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteByte(' ')
		} else {
			prevSpace = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tailLines(lines []string, n int) []string {
	if n <= 0 {
		return []string{}
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func trimUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// ensure we do not cut in the middle of a rune
	// walk backwards until we find a valid boundary
	bs := []byte(s)
	if max < 0 {
		max = 0
	}
	if max > len(bs) {
		max = len(bs)
	}
	i := max
	for i > 0 && !utf8.FullRune(bs[:i]) {
		i--
	}
	if i == 0 {
		return ""
	}
	return string(bs[:i])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
