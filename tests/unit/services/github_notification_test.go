package services_test

import (
	"agentic-automation/internal/services"
	testmocks "agentic-automation/tests/mocks"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNotifyPRCreated_NewPostsToIssueAndPR(t *testing.T) {
	mock := testmocks.NewGitHubIssueCommentsServer()
	t.Cleanup(mock.Close)
	mock.Reset()

	gh := buildTestGitHubClient(t, mock.URL())
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())
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
	svc := services.NewGitHubNotificationService(gh, zap.NewNop())

	ctx := context.Background()
	err := svc.NotifyPRCreated(ctx, "o", "r", 0, 20, "https://github.com/o/r/pull/20", "b", "abcdef0", "idX")
	require.NoError(t, err)

	posts := mock.Posts()
	require.Len(t, posts, 1)
	require.Equal(t, 20, posts[0].Number)
}
