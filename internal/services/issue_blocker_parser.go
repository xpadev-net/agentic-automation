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
			section := line[loc[1]:]
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
			section := line[loc[1]:]
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

func extractIssueReferences(text string, ctx RepoContext) []IssueReference {
	var out []IssueReference

	// First, URLs
	for _, m := range reIssueURL.FindAllStringSubmatch(text, -1) {
		owner := m[1]
		repo := m[2]
		num, _ := strconv.Atoi(m[3])
		out = append(out, IssueReference{Owner: owner, Repo: repo, Number: num})
	}

	// Then, owner/repo#num
	for _, m := range reOwnerRepo.FindAllStringSubmatch(text, -1) {
		owner := m[1]
		repo := m[2]
		num, _ := strconv.Atoi(m[3])
		out = append(out, IssueReference{Owner: owner, Repo: repo, Number: num})
	}

	// Finally, #num (use context)
	for _, m := range reLocalIssue.FindAllStringSubmatch(text, -1) {
		if ctx.Owner == "" || ctx.Repo == "" {
			// Without context, skip local references.
			continue
		}
		num, _ := strconv.Atoi(m[1])
		out = append(out, IssueReference{Owner: ctx.Owner, Repo: ctx.Repo, Number: num})
	}

	return out
}

func dedupeKey(r IssueReference) string {
	return strings.ToLower(r.Owner) + "/" + strings.ToLower(r.Repo) + "#" + strconv.Itoa(r.Number)
}
