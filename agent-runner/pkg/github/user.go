package githubutil

import (
	"context"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

// FindRunAgentActor returns the login of the latest commenter who triggered /run-agent on the issue.
// If no such comment is found, returns empty string.
func FindRunAgentActor(ctx context.Context, token, owner, repo string, issueNumber int) (string, error) {
	if owner == "" || repo == "" || issueNumber <= 0 {
		return "", fmt.Errorf("invalid parameters")
	}

	// Build GitHub client
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := gh.NewClient(tc)

	// List all comments on the issue (paginate up to a reasonable cap)
	opts := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	var latestLogin string
	var latestCreatedAt int64
	for {
		comments, resp, err := client.Issues.ListComments(ctx, owner, repo, issueNumber, opts)
		if err != nil {
			return "", err
		}
		for _, c := range comments {
			if c == nil || c.Body == nil || c.User == nil || c.User.Login == nil {
				continue
			}
			body := strings.ToLower(*c.Body)
			if !strings.Contains(body, "/run-agent") {
				continue
			}
			ts := int64(0)
			if c.CreatedAt != nil {
				ts = c.CreatedAt.Time.Unix()
			}
			if ts >= latestCreatedAt {
				latestCreatedAt = ts
				latestLogin = *c.User.Login
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return latestLogin, nil
}

// GetUserProfile fetches a user's public profile by login.
func GetUserProfile(ctx context.Context, token, login string) (*gh.User, error) {
	if login == "" {
		return nil, fmt.Errorf("login is required")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := gh.NewClient(tc)
	user, _, err := client.Users.Get(ctx, login)
	if err != nil {
		return nil, err
	}
	return user, nil
}
