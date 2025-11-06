package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	githubutil "agent-runner/pkg/github"
)

// EnsureCommitIdentity ensures git user.name and user.email are set locally in workDir.
// If missing, it tries to fetch the latest /run-agent commenter and use their profile.
// Falls back to GitHub Actions bot identity if GitHub API lookup is unavailable.
func EnsureCommitIdentity(workDir, repo string, issueNumber int) error {
	// If both are configured, nothing to do
	name, _ := getGitConfig(workDir, "user.name")
	email, _ := getGitConfig(workDir, "user.email")
	if strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" {
		return nil
	}

	actorName := ""
	actorEmail := ""

	// Try to resolve from GitHub if possible
	owner, repoName, err := splitRepo(repo)
	if err == nil && issueNumber > 0 {
		// Acquire token via GitHub App
		token, tokErr := githubutil.GetGitHubToken(context.Background(), owner, repoName)
		if tokErr == nil && token != "" {
			// Find latest /run-agent actor
			login, findErr := githubutil.FindRunAgentActor(context.Background(), token, owner, repoName, issueNumber)
			if findErr == nil && login != "" {
				if user, getErr := githubutil.GetUserProfile(context.Background(), token, login); getErr == nil && user != nil {
					if user.Name != nil && *user.Name != "" {
						actorName = *user.Name
					} else {
						actorName = login
					}
					if user.Email != nil && *user.Email != "" {
						actorEmail = *user.Email
					} else {
						actorEmail = fmt.Sprintf("%s@users.noreply.github.com", login)
					}
				} else {
					actorName = login
					actorEmail = fmt.Sprintf("%s@users.noreply.github.com", login)
				}
			}
		}
	}

	// Fallback if unresolved
	if actorName == "" || actorEmail == "" {
		actorName = "GitHub Actions Bot"
		actorEmail = "actions@users.noreply.github.com"
	}

	if strings.TrimSpace(name) == "" {
		if err := setGitConfig(workDir, "user.name", actorName); err != nil {
			return err
		}
	}
	if strings.TrimSpace(email) == "" {
		if err := setGitConfig(workDir, "user.email", actorEmail); err != nil {
			return err
		}
	}
	return nil
}

func getGitConfig(dir, key string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func setGitConfig(dir, key, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("git config value must not be empty")
	}
	cmd := exec.Command("git", "config", key, value)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set %s: %v - %s", key, err, stderr.String())
	}
	return nil
}

func splitRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("repository must be in format owner/repo, got: %q", repo)
	}
	return parts[0], parts[1], nil
}
