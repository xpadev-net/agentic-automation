package services_test

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"
	testmocks "agentic-automation/tests/mocks"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotifyPRCreated_NewPostsToIssueAndPR(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	owner, repo := "org", "repo"
	issueNum := 12
	prNum := 34
	prURL := "https://github.com/org/repo/pull/34"
	branch := "feature/x"
	sha := "0123456789abcdef"
	idem := "delivery-1"

	err := svc.NotifyPRCreated(ctx, owner, repo, issueNum, prNum, prURL, branch, sha, idem)
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 2)
	// Expect one post to issue and one to PR
	seenIssue := false
	seenPR := false
	for _, p := range posts {
		if p.Number == issueNum {
			seenIssue = true
			require.Contains(t, p.Body, "<!-- agent:pr-created:"+idem+" -->")
			require.Contains(t, p.Body, "PR created: #34 ("+prURL+") branch="+branch+" sha=0123456")
		}
		if p.Number == prNum {
			seenPR = true
			require.Contains(t, p.Body, "<!-- agent:pr-created:"+idem+" -->")
			require.Contains(t, p.Body, "PR created: #34 ("+prURL+") branch="+branch+" sha=0123456")
		}
	}
	require.True(t, seenIssue)
	require.True(t, seenPR)
}

func TestNotifyPRCreated_Idempotent_IssueHasMarkerOnly(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	// Seed marker in issue thread only
	idem := "same-key"
	marker := "<!-- agent:pr-created:" + idem + " -->"
	mock.Seed(100, marker+"\npre-existing")

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 100, 200, "https://github.com/o/r/pull/200", "b", "abcdef0", idem)
	require.NoError(t, err)

	posts := mock.Posts()
	// Should skip issue, but post to PR
	require.Len(t, posts, 1)
	require.Equal(t, 200, posts[0].Number)
}

func TestNotifyPRCreated_Idempotent_BothHaveMarker(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	idem := "k"
	marker := "<!-- agent:pr-created:" + idem + " -->"
	mock.Seed(10, marker+"\nissue")
	mock.Seed(20, marker+"\npr")

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 10, 20, "https://github.com/o/r/pull/20", "b", "abcdef0", idem)
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 0)
}

func TestNotifyPRCreated_SkipWhenMissingPRNumber(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 10, 0, "", "b", "abcd12", "id1")
	require.NoError(t, err)
	require.Len(t, mock.Posts(), 0)
}

func TestNotifyPRCreated_IssueList500_ReturnsErrorButPRPosts(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()
	mock.SetErrorMode(testmocks.ErrorMode{List500For: map[int]bool{10: true}})

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 10, 20, "https://github.com/o/r/pull/20", "b", "abcdef0", "idX")
	require.Error(t, err)

	posts := mock.Posts()
	// Should still attempt to post to PR thread
	// One POST to PR even if issue list failed
	foundPR := false
	for _, p := range posts {
		if p.Number == 20 {
			foundPR = true
		}
	}
	require.True(t, foundPR)
}

func TestNotifyPRCreated_PRPost500_ReturnsErrorButIssuePosts(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()
	mock.SetErrorMode(testmocks.ErrorMode{Post500For: map[int]bool{20: true}})

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 10, 20, "https://github.com/o/r/pull/20", "b", "abcdef0", "idX")
	require.Error(t, err)

	posts := mock.Posts()
	// Should have at least issue post
	foundIssue := false
	for _, p := range posts {
		if p.Number == 10 {
			foundIssue = true
		}
	}
	require.True(t, foundIssue)
}

func TestNotifyPRCreated_ShortSHABoundary(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())
	ctx := context.Background()

	// 6 chars -> unchanged
	_ = svc.NotifyPRCreated(ctx, "o", "r", 1, 2, "https://github.com/o/r/pull/2", "b", "123456", "i1")
	// 7 chars -> unchanged
	_ = svc.NotifyPRCreated(ctx, "o", "r", 3, 4, "https://github.com/o/r/pull/4", "b", "1234567", "i2")
	// 8 chars -> trimmed to 7
	_ = svc.NotifyPRCreated(ctx, "o", "r", 5, 6, "https://github.com/o/r/pull/6", "b", "12345678", "i3")

	posts := mock.Posts()
	// We made 3 invocations, each should post 2 comments (issue+pr) = 6 posts
	require.Len(t, posts, 6)

	// Find third invocation posts (issue 5 and pr 6) and check sha=1234567
	var bodies []string
	for _, p := range posts {
		if p.Number == 5 || p.Number == 6 {
			bodies = append(bodies, p.Body)
		}
	}
	require.Len(t, bodies, 2)
	require.Contains(t, bodies[0], "sha=1234567")
	require.Contains(t, bodies[1], "sha=1234567")
}

func TestNotifyPRCreated_NoIssueNumber_PROnly(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 0, 20, "https://github.com/o/r/pull/20", "b", "abcdef0", "idX")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 1)
	require.Equal(t, 20, posts[0].Number)
}

// -----------------------------------------------------------------------------
// Retry Progress Notification Tests (US3 T102)
// -----------------------------------------------------------------------------

func TestTruncateErrorReason_ShortMessage(t *testing.T) {
	result := services.TruncateErrorReason("Short error", 100)
	require.Equal(t, "Short error", result)
}

func TestTruncateErrorReason_LongMessage(t *testing.T) {
	longMsg := strings.Repeat("a", 150)
	result := services.TruncateErrorReason(longMsg, 100)
	require.Len(t, result, 103) // 100 chars + "..."
	require.Equal(t, strings.Repeat("a", 100)+"...", result)
}

func TestTruncateErrorReason_EmptyString(t *testing.T) {
	result := services.TruncateErrorReason("", 100)
	require.Equal(t, "", result)
}

func TestTruncateErrorReason_DefaultMaxLength(t *testing.T) {
	longMsg := strings.Repeat("a", 150)
	result := services.TruncateErrorReason(longMsg, 0)
	require.Len(t, result, 103) // 100 chars + "..."
}

func TestFormatRetryProgressMessage_ProgressBar(t *testing.T) {
	marker := "<!-- agent:retry-progress:test -->"
	result := services.FormatRetryProgressMessage(5, 10, "Test error", marker)

	require.Contains(t, result, marker)
	require.Contains(t, result, "🔄 Retry Progress")
	require.Contains(t, result, "**Attempt**: 5/10")
	require.Contains(t, result, "**Progress**: [█████░░░░░] 50%")
	require.Contains(t, result, "**Reason**: Test error")
	require.Contains(t, result, "Retrying with feedback...")
}

func TestFormatRetryProgressMessage_ProgressBar_0Percent(t *testing.T) {
	marker := "<!-- agent:retry-progress:test -->"
	result := services.FormatRetryProgressMessage(0, 50, "Error", marker)
	require.Contains(t, result, "**Progress**: [░░░░░░░░░░] 0%")
}

func TestFormatRetryProgressMessage_ProgressBar_100Percent(t *testing.T) {
	marker := "<!-- agent:retry-progress:test -->"
	result := services.FormatRetryProgressMessage(50, 50, "Error", marker)
	require.Contains(t, result, "**Progress**: [██████████] 100%")
}

func TestFormatRetryProgressMessage_TruncatesLongError(t *testing.T) {
	marker := "<!-- agent:retry-progress:test -->"
	longError := strings.Repeat("a", 150)
	result := services.FormatRetryProgressMessage(1, 50, longError, marker)

	// Should contain truncated error (100 chars + "...")
	require.Contains(t, result, strings.Repeat("a", 100)+"...")
}

func TestFindCommentWithMarker_Found(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	marker := "<!-- agent:retry-progress:test -->"
	mock.Seed(100, marker+"\nExisting comment")

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	commentID, found, err := svc.FindCommentWithMarker(ctx, "o", "r", 100, marker)

	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, commentID)
	require.Equal(t, int64(100), *commentID)
}

func TestFindCommentWithMarker_NotFound(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	commentID, found, err := svc.FindCommentWithMarker(ctx, "o", "r", 100, "<!-- agent:retry-progress:not-found -->")

	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, commentID)
}

func TestNotifyRetryProgress_NewPostsToIssueAndPR(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyRetryProgress(ctx, "org", "repo", 10, 20, 5, 50, "Test error", "delivery-1")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 2)

	seenIssue := false
	seenPR := false
	for _, p := range posts {
		if p.Number == 10 {
			seenIssue = true
			require.Contains(t, p.Body, "<!-- agent:retry-progress:delivery-1 -->")
			require.Contains(t, p.Body, "🔄 Retry Progress")
			require.Contains(t, p.Body, "**Attempt**: 5/50")
			require.Contains(t, p.Body, "Test error")
		}
		if p.Number == 20 {
			seenPR = true
			require.Contains(t, p.Body, "<!-- agent:retry-progress:delivery-1 -->")
		}
	}
	require.True(t, seenIssue)
	require.True(t, seenPR)
}

func TestNotifyRetryProgress_UpdatesExistingComment(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	idem := "test-key"
	marker := "<!-- agent:retry-progress:" + idem + " -->"
	mock.Seed(100, marker+"\nOld comment")

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyRetryProgress(ctx, "o", "r", 100, 0, 3, 50, "New error", idem)
	require.NoError(t, err)

	// Should have one update (PATCH) call
	updates := mock.Updates()
	require.Len(t, updates, 1)
	require.Equal(t, int64(100), updates[0].CommentID)
	require.Contains(t, updates[0].Body, "<!-- agent:retry-progress:"+idem+" -->")
	require.Contains(t, updates[0].Body, "**Attempt**: 3/50")
	require.Contains(t, updates[0].Body, "New error")
}

func TestNotifyRetryProgress_InputValidation(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()

	// Negative retryCount
	err := svc.NotifyRetryProgress(ctx, "o", "r", 10, 20, -1, 50, "error", "key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "retryCount must be >= 0")

	// Zero maxRetries
	err = svc.NotifyRetryProgress(ctx, "o", "r", 10, 20, 5, 0, "error", "key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "maxRetries must be > 0")

	// Empty idempotencyKey
	err = svc.NotifyRetryProgress(ctx, "o", "r", 10, 20, 5, 50, "error", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "idempotencyKey must not be empty")
}

func TestNotifyRetryProgress_IssueOnly(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyRetryProgress(ctx, "org", "repo", 10, 0, 2, 50, "Error", "key1")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 1)
	require.Equal(t, 10, posts[0].Number)
}

func TestNotifyRetryProgress_PROnly(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()
	err := svc.NotifyRetryProgress(ctx, "org", "repo", 0, 20, 2, 50, "Error", "key2")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 1)
	require.Equal(t, 20, posts[0].Number)
}

// -----------------------------------------------------------------------------
// Dependency Violation Notification Tests (US5 T129)
// -----------------------------------------------------------------------------

func TestFormatDependencyViolationBody_SingleIssue(t *testing.T) {
	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 123, State: "open"},
	}
	marker := "<!-- agent:dependency-violation:key1 -->"
	result := services.FormatDependencyViolationBody(blocked, marker)

	require.Contains(t, result, marker)
	require.Contains(t, result, "❌ Dependency violation")
	require.Contains(t, result, "owner/repo#123 [open]")
	require.Contains(t, result, "Close all blocking issues to proceed.")
}

func TestFormatDependencyViolationBody_MultipleIssues(t *testing.T) {
	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 1, State: "open"},
		{Repo: "owner/repo", Number: 2, State: "open"},
		{Repo: "owner/repo", Number: 3, State: "open"},
	}
	marker := "<!-- agent:dependency-violation:key2 -->"
	result := services.FormatDependencyViolationBody(blocked, marker)

	require.Contains(t, result, "owner/repo#1 [open]")
	require.Contains(t, result, "owner/repo#2 [open]")
	require.Contains(t, result, "owner/repo#3 [open]")
	require.NotContains(t, result, "... and")
}

func TestFormatDependencyViolationBody_MoreThanMaxShows(t *testing.T) {
	blocked := make([]models.Issue, 15)
	for i := 0; i < 15; i++ {
		blocked[i] = models.Issue{Repo: "owner/repo", Number: i + 1, State: "open"}
	}
	marker := "<!-- agent:dependency-violation:key3 -->"
	result := services.FormatDependencyViolationBody(blocked, marker)

	// Should show first 10
	require.Contains(t, result, "owner/repo#1 [open]")
	require.Contains(t, result, "owner/repo#10 [open]")
	// Should not show 11th
	require.NotContains(t, result, "owner/repo#11 [open]")
	// Should show summary
	require.Contains(t, result, "... and 5 more")
}

func TestNotifyDependencyViolation_NewPostsToIssueAndPR(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 100, State: "open"},
		{Repo: "owner/repo", Number: 101, State: "open"},
	}

	ctx := context.Background()
	err := svc.NotifyDependencyViolation(ctx, "owner", "repo", 10, 20, blocked, "delivery-1")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 2)

	seenIssue := false
	seenPR := false
	for _, p := range posts {
		if p.Number == 10 {
			seenIssue = true
			require.Contains(t, p.Body, "<!-- agent:dependency-violation:delivery-1 -->")
			require.Contains(t, p.Body, "❌ Dependency violation")
			require.Contains(t, p.Body, "owner/repo#100 [open]")
			require.Contains(t, p.Body, "owner/repo#101 [open]")
		}
		if p.Number == 20 {
			seenPR = true
			require.Contains(t, p.Body, "<!-- agent:dependency-violation:delivery-1 -->")
		}
	}
	require.True(t, seenIssue)
	require.True(t, seenPR)
}

func TestNotifyDependencyViolation_UpdatesExistingComment(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	idem := "test-key"
	marker := "<!-- agent:dependency-violation:" + idem + " -->"
	mock.Seed(100, marker+"\nOld comment")

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 200, State: "open"},
	}

	ctx := context.Background()
	err := svc.NotifyDependencyViolation(ctx, "o", "r", 100, 0, blocked, idem)
	require.NoError(t, err)

	// Should have one update (PATCH) call
	updates := mock.Updates()
	require.Len(t, updates, 1)
	require.Equal(t, int64(100), updates[0].CommentID)
	require.Contains(t, updates[0].Body, marker)
	require.Contains(t, updates[0].Body, "owner/repo#200 [open]")
}

func TestNotifyDependencyViolation_InputValidation(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	ctx := context.Background()

	// Empty blocked list
	err := svc.NotifyDependencyViolation(ctx, "o", "r", 10, 20, []models.Issue{}, "key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked issues list must not be empty")

	// Empty idempotencyKey
	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 1, State: "open"},
	}
	err = svc.NotifyDependencyViolation(ctx, "o", "r", 10, 20, blocked, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "idempotencyKey must not be empty")
}

func TestNotifyDependencyViolation_IssueOnly(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 1, State: "open"},
	}

	ctx := context.Background()
	err := svc.NotifyDependencyViolation(ctx, "org", "repo", 10, 0, blocked, "key1")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 1)
	require.Equal(t, 10, posts[0].Number)
}

func TestNotifyDependencyViolation_PROnly(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, config.NewNopLogger())

	blocked := []models.Issue{
		{Repo: "owner/repo", Number: 1, State: "open"},
	}

	ctx := context.Background()
	err := svc.NotifyDependencyViolation(ctx, "org", "repo", 0, 20, blocked, "key2")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 1)
	require.Equal(t, 20, posts[0].Number)
}
