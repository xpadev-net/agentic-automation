package services_test

import (
	"agentic-automation/internal/services"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseIssueBlockers_SingleFormats_BlockedBy(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	cases := []struct {
		name   string
		body   string
		expect services.IssueReference
	}{
		{name: "hash-only", body: "blocked by #12", expect: services.IssueReference{Owner: "acme", Repo: "web", Number: 12}},
		{name: "owner-repo-hash", body: "blocked by owner/repo#34", expect: services.IssueReference{Owner: "owner", Repo: "repo", Number: 34}},
		{name: "issue-url", body: "blocked by https://github.com/acme/app/issues/56", expect: services.IssueReference{Owner: "acme", Repo: "app", Number: 56}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res, err := services.ParseIssueBlockers(c.body, ctx)
			assert.NoError(t, err)
			assert.Len(t, res.BlockedBy, 1)
			assert.Equal(t, c.expect, res.BlockedBy[0])
			assert.Len(t, res.Blocking, 0)
		})
	}
}

func TestParseIssueBlockers_MultiMixed_Blocking(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	body := "blocking #1, owner/repo#2 and https://github.com/o/r/issues/3"
	res, err := services.ParseIssueBlockers(body, ctx)
	assert.NoError(t, err)
	assert.Len(t, res.Blocking, 3)
	assert.Equal(t, services.IssueReference{Owner: "acme", Repo: "web", Number: 1}, res.Blocking[0])
	assert.Equal(t, services.IssueReference{Owner: "owner", Repo: "repo", Number: 2}, res.Blocking[1])
	assert.Equal(t, services.IssueReference{Owner: "o", Repo: "r", Number: 3}, res.Blocking[2])
	assert.Len(t, res.BlockedBy, 0)
}

func TestParseIssueBlockers_Multiline(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	body := "blocked by #10\nblocking #11"
	res, err := services.ParseIssueBlockers(body, ctx)
	assert.NoError(t, err)
	assert.Equal(t, []services.IssueReference{{Owner: "acme", Repo: "web", Number: 10}}, res.BlockedBy)
	assert.Equal(t, []services.IssueReference{{Owner: "acme", Repo: "web", Number: 11}}, res.Blocking)
}

func TestParseIssueBlockers_CaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	body := "Blocked By #9 and also BLOCKING #8"
	res, err := services.ParseIssueBlockers(body, ctx)
	assert.NoError(t, err)
	assert.Equal(t, []services.IssueReference{{Owner: "acme", Repo: "web", Number: 9}}, res.BlockedBy)
	assert.Equal(t, []services.IssueReference{{Owner: "acme", Repo: "web", Number: 8}}, res.Blocking)
}

func TestParseIssueBlockers_DuplicateCollapse(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "Owner", Repo: "Repo"}

	body := "blocked by #5, owner/repo#5, https://github.com/owner/repo/issues/5"
	res, err := services.ParseIssueBlockers(body, ctx)
	assert.NoError(t, err)
	assert.Len(t, res.BlockedBy, 1)
	// order preserves first occurrence which is ctx based
	assert.Equal(t, services.IssueReference{Owner: "Owner", Repo: "Repo", Number: 5}, res.BlockedBy[0])
}

func TestParseIssueBlockers_ContextFallback(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	body := "blocked by #7"
	res, err := services.ParseIssueBlockers(body, ctx)
	assert.NoError(t, err)
	assert.Equal(t, []services.IssueReference{{Owner: "acme", Repo: "web", Number: 7}}, res.BlockedBy)
}

func TestParseIssueBlockers_NoiseIgnore(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	body := "this is not blocking behavior, nor depends on #3"
	res, err := services.ParseIssueBlockers(body, ctx)
	assert.NoError(t, err)
	assert.Len(t, res.BlockedBy, 0)
	assert.Len(t, res.Blocking, 0)
}

func TestParseIssueBlockers_Robustness_BadInputs(t *testing.T) {
	t.Parallel()
	ctx := services.RepoContext{Owner: "acme", Repo: "web"}

	bodies := []string{
		"blocked by #1,",                                      // trailing comma
		"blocking owner/repo#2, #3",                           // mixed with comma
		"blocked by  https://github.com/acme/app/issues/4.",   // trailing punctuation
		"blocking #\t5 and #6",                                // tab between symbol and number won't match second
		"blocked by https://github.com/a/b/issues/notanumber", // malformed URL number
	}

	for _, body := range bodies {
		_, err := services.ParseIssueBlockers(body, ctx)
		assert.NoError(t, err)
	}
}
