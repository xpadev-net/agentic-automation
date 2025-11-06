package services

import (
	"regexp"
	"strconv"
	"strings"
)

// RepoContext represents the repository context used to resolve shorthand issue references like #123.
// When parsing `#123`, the owner/repo is taken from this context.
type RepoContext struct {
	Owner string
	Repo  string
}

// IssueReference is a normalized reference to a GitHub issue.
type IssueReference struct {
	Owner  string
	Repo   string
	Number int
}

// ParseResult holds parsed references categorized by relation.
// BlockedBy: issues that block the current one ("blocked by").
// Blocking: issues that are blocked by the current one ("blocking").
type ParseResult struct {
	BlockedBy []IssueReference
	Blocking  []IssueReference
}

var (
	// phrase detectors (case-insensitive)
	reBlockedBy = regexp.MustCompile(`(?i)\bblocked by\b`)
	reBlocking  = regexp.MustCompile(`(?i)\bblocking\b`)

	// reference extractors
	reIssueURL   = regexp.MustCompile(`https?://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/(\d+)`)
	reOwnerRepo  = regexp.MustCompile(`([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#(\d+)`)
	reLocalIssue = regexp.MustCompile(`#(\d+)`)
)

// ParseIssueBlockers parses the given issue body and extracts issue references that appear after
// the phrases "blocked by" and "blocking" until the end of the same line. The function supports
// three reference formats: #123, owner/repo#123, and https://github.com/owner/repo/issues/123.
// Duplicate references are removed while preserving first-seen order.
func ParseIssueBlockers(body string, ctx RepoContext) (ParseResult, error) {
	result := ParseResult{}
	if strings.TrimSpace(body) == "" {
		return result, nil
	}

	// Dedup maps
	seenBlockedBy := make(map[string]struct{})
	seenBlocking := make(map[string]struct{})

	lines := strings.Split(body, "\n")
	for _, line := range lines {
		// Process all occurrences of the phrases within the same line.
		// blocked by
		for _, loc := range reBlockedBy.FindAllStringIndex(line, -1) {
			section := sliceSection(line, loc[1:])
			refs := extractIssueReferences(section, ctx)
			for _, r := range refs {
				key := dedupeKey(r)
				if _, ok := seenBlockedBy[key]; ok {
					continue
				}
				seenBlockedBy[key] = struct{}{}
				result.BlockedBy = append(result.BlockedBy, r)
			}
		}

		// blocking
		for _, loc := range reBlocking.FindAllStringIndex(line, -1) {
			section := sliceSection(line, loc[1:])
			refs := extractIssueReferences(section, ctx)
			for _, r := range refs {
				key := dedupeKey(r)
				if _, ok := seenBlocking[key]; ok {
					continue
				}
				seenBlocking[key] = struct{}{}
				result.Blocking = append(result.Blocking, r)
			}
		}
	}

	return result, nil
}

// sliceSection returns substring starting at given start index until earliest of:
// next comma, next phrase occurrence (blocked by/blocking), or line end.
func sliceSection(line string, startIdx []int) string {
	start := startIdx[0]
	// Default end: next phrase occurrence or end of line
	end := len(line)
	if loc := reBlockedBy.FindStringIndex(line[start:]); loc != nil {
		if start+loc[0] < end {
			end = start + loc[0]
		}
	}
	if loc := reBlocking.FindStringIndex(line[start:]); loc != nil {
		if start+loc[0] < end {
			end = start + loc[0]
		}
	}

	// Consider comma as boundary ONLY if there is no reference before the comma
	if i := strings.IndexByte(line[start:end], ','); i >= 0 {
		commaEnd := start + i
		segment := line[start:commaEnd]
		hasRef := reIssueURL.MatchString(segment) || reOwnerRepo.MatchString(segment) || reLocalIssue.MatchString(segment)
		if !hasRef {
			end = commaEnd
		}
	}

	if end < start {
		end = start
	}
	return line[start:end]
}

func extractIssueReferences(text string, ctx RepoContext) []IssueReference {
	var out []IssueReference

	// Collect spans for owner/repo# and URL to mask before local search
	type span struct{ s, e int }
	var ownerRepoSpans []span
	var urlSpans []span
	for _, loc := range reOwnerRepo.FindAllStringIndex(text, -1) {
		ownerRepoSpans = append(ownerRepoSpans, span{loc[0], loc[1]})
	}
	for _, loc := range reIssueURL.FindAllStringIndex(text, -1) {
		urlSpans = append(urlSpans, span{loc[0], loc[1]})
	}

	// Mask those spans
	b := []byte(text)
	for _, sp := range append(append([]span{}, ownerRepoSpans...), urlSpans...) {
		for i := sp.s; i < sp.e && i < len(b); i++ {
			b[i] = ' '
		}
	}

	// 1) Local #num (preferred order)
	for _, m := range reLocalIssue.FindAllSubmatchIndex(b, -1) {
		if ctx.Owner == "" || ctx.Repo == "" {
			continue
		}
		numStr := string(b[m[2]:m[3]])
		num, _ := strconv.Atoi(numStr)
		out = append(out, IssueReference{Owner: ctx.Owner, Repo: ctx.Repo, Number: num})
	}

	// 2) owner/repo#num in original text order
	for _, m := range reOwnerRepo.FindAllStringSubmatch(text, -1) {
		owner := m[1]
		repo := m[2]
		num, _ := strconv.Atoi(m[3])
		out = append(out, IssueReference{Owner: owner, Repo: repo, Number: num})
	}

	// 3) URLs in original text order
	for _, m := range reIssueURL.FindAllStringSubmatch(text, -1) {
		owner := m[1]
		repo := m[2]
		num, _ := strconv.Atoi(m[3])
		out = append(out, IssueReference{Owner: owner, Repo: repo, Number: num})
	}

	return out
}

func dedupeKey(r IssueReference) string {
	return strings.ToLower(r.Owner) + "/" + strings.ToLower(r.Repo) + "#" + strconv.Itoa(r.Number)
}
