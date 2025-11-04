package githubutil

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/golang-jwt/jwt/v4"
	gh "github.com/google/go-github/v57/github"
)

// GetGitHubToken returns a short-lived Installation Token for the GitHub App
// installed on the specified repository. The token can be used with Git over HTTPS.
//
// Required environment variables:
// - GITHUB_APP_ID: numeric App ID
// - GITHUB_PRIVATE_KEY: PEM contents of the app private key (with newlines)
func GetGitHubToken(ctx context.Context, owner, repo string) (string, error) {
	appIDStr := strings.TrimSpace(os.Getenv("GITHUB_APP_ID"))
	privateKey := os.Getenv("GITHUB_PRIVATE_KEY")

	if appIDStr == "" || privateKey == "" {
		return "", errors.New("missing GitHub App credentials in env: GITHUB_APP_ID or GITHUB_PRIVATE_KEY")
	}

	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return "", errors.New("invalid GITHUB_APP_ID: must be an integer")
	}

	if owner == "" {
		owner = strings.TrimSpace(os.Getenv("REPO_OWNER"))
	}
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("REPO_NAME"))
	}
	if owner == "" || repo == "" {
		return "", errors.New("repository owner/name is required")
	}

	rsaKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKey))
	if err != nil {
		return "", err
	}

	appsTr := ghinstallation.NewAppsTransportFromPrivateKey(http.DefaultTransport, appID, rsaKey)

	httpClient := &http.Client{Transport: appsTr}
	ghClient := gh.NewClient(httpClient)

	inst, _, err := ghClient.Apps.FindRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return "", err
	}

	instTr := ghinstallation.NewFromAppsTransport(appsTr, inst.GetID())
	token, err := instTr.Token(ctx)
	if err != nil {
		return "", err
	}

	return token, nil
}
