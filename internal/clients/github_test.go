package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
)

// minimal response structs
type installationResp struct {
	ID int64 `json:"id"`
}

type tokenResp struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func newTestLogger(t *testing.T) *zap.Logger {
	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}
	return logger
}

func TestInstallationTokenCache_ReusesValidToken(t *testing.T) {
	var accessTokenCalls int32
	owner := "acme"
	repo := "app"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+owner+"/"+repo+"/installation":
			_ = json.NewEncoder(w).Encode(installationResp{ID: 123})
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/123/access_tokens":
			atomic.AddInt32(&accessTokenCalls, 1)
			_ = json.NewEncoder(w).Encode(tokenResp{Token: "tkn1", ExpiresAt: time.Now().Add(50 * time.Minute)})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	logger := newTestLogger(t)
	// inject fake envs
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := NewGitHubAppClient(logger)
	if err != nil {
		t.Fatalf("NewGitHubAppClient error: %v", err)
	}

	// override base URL by calling resolve and token creation against our server
	// We leverage internal helpers by temporarily swapping to server BaseURL via local client
	origResolve := appClient.resolveInstallationID
	appClient.resolveInstallationID = func(ctx context.Context, o, r string) (int64, error) {
		client := github.NewClient(server.Client())
		client.BaseURL = mustParseBaseURL(server.URL)
		inst, _, err := client.Apps.FindRepositoryInstallation(ctx, o, r)
		if err != nil || inst == nil || inst.ID == nil {
			return 0, err
		}
		return *inst.ID, nil
	}
	origGet := appClient.getInstallationToken
	appClient.getInstallationToken = func(ctx context.Context, installationID int64) (string, error) {
		client := github.NewClient(server.Client())
		client.BaseURL = mustParseBaseURL(server.URL)
		tok, _, err := client.Apps.CreateInstallationToken(ctx, installationID, &github.InstallationTokenOptions{})
		if err != nil {
			return "", err
		}
		appClient.tokenCache.mutex.Lock()
		appClient.tokenCache.tokens[installationID] = &TokenEntry{Token: tok.GetToken(), ExpiresAt: tok.ExpiresAt.Time}
		appClient.tokenCache.mutex.Unlock()
		return tok.GetToken(), nil
	}
	defer func() {
		appClient.resolveInstallationID = origResolve
		appClient.getInstallationToken = origGet
	}()

	ctx := context.Background()
	_, err = appClient.ForRepo(ctx, owner, repo)
	if err != nil {
		t.Fatalf("ForRepo 1 error: %v", err)
	}
	_, err = appClient.ForRepo(ctx, owner, repo)
	if err != nil {
		t.Fatalf("ForRepo 2 error: %v", err)
	}
	if atomic.LoadInt32(&accessTokenCalls) != 1 {
		t.Fatalf("expected 1 token call, got %d", accessTokenCalls)
	}
}

func TestInstallationTokenCache_RefreshNearExpiry(t *testing.T) {
	var accessTokenCalls int32
	owner := "acme"
	repo := "app"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+owner+"/"+repo+"/installation":
			_ = json.NewEncoder(w).Encode(installationResp{ID: 456})
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/456/access_tokens":
			atomic.AddInt32(&accessTokenCalls, 1)
			// short expiry to force refresh (our cache requires >2m remaining)
			_ = json.NewEncoder(w).Encode(tokenResp{Token: "tknX", ExpiresAt: time.Now().Add(30 * time.Second)})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	logger := newTestLogger(t)
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := NewGitHubAppClient(logger)
	if err != nil {
		t.Fatalf("NewGitHubAppClient error: %v", err)
	}

	origResolve := appClient.resolveInstallationID
	appClient.resolveInstallationID = func(ctx context.Context, o, r string) (int64, error) {
		client := github.NewClient(server.Client())
		client.BaseURL = mustParseBaseURL(server.URL)
		inst, _, err := client.Apps.FindRepositoryInstallation(ctx, o, r)
		if err != nil || inst == nil || inst.ID == nil {
			return 0, err
		}
		return *inst.ID, nil
	}
	origGet := appClient.getInstallationToken
	appClient.getInstallationToken = func(ctx context.Context, installationID int64) (string, error) {
		client := github.NewClient(server.Client())
		client.BaseURL = mustParseBaseURL(server.URL)
		tok, _, err := client.Apps.CreateInstallationToken(ctx, installationID, &github.InstallationTokenOptions{})
		if err != nil {
			return "", err
		}
		appClient.tokenCache.mutex.Lock()
		appClient.tokenCache.tokens[installationID] = &TokenEntry{Token: tok.GetToken(), ExpiresAt: tok.ExpiresAt.Time}
		appClient.tokenCache.mutex.Unlock()
		return tok.GetToken(), nil
	}
	defer func() {
		appClient.resolveInstallationID = origResolve
		appClient.getInstallationToken = origGet
	}()

	ctx := context.Background()
	_, err = appClient.ForRepo(ctx, owner, repo)
	if err != nil {
		t.Fatalf("ForRepo 1 error: %v", err)
	}
	_, err = appClient.ForRepo(ctx, owner, repo)
	if err != nil {
		t.Fatalf("ForRepo 2 error: %v", err)
	}
	if atomic.LoadInt32(&accessTokenCalls) != 2 {
		t.Fatalf("expected 2 token calls due to near expiry, got %d", accessTokenCalls)
	}
}

// mustParseBaseURL converts the server URL to a form acceptable by go-github BaseURL
func mustParseBaseURL(u string) *github.URL {
	parsed, err := github.ParseURL(u + "/")
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestForRepo_MinimalIntegrationWithMockServer(t *testing.T) {
	owner := "acme"
	repo := "app"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+owner+"/"+repo+"/installation":
			_ = json.NewEncoder(w).Encode(installationResp{ID: 789})
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/789/access_tokens":
			_ = json.NewEncoder(w).Encode(tokenResp{Token: "itkn", ExpiresAt: time.Now().Add(55 * time.Minute)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+owner+"/"+repo:
			if r.Header.Get("Authorization") == "token itkn" {
				_ = json.NewEncoder(w).Encode(github.Repository{Name: github.String(repo)})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	logger := newTestLogger(t)
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := NewGitHubAppClient(logger)
	if err != nil {
		t.Fatalf("NewGitHubAppClient error: %v", err)
	}

	// override internal API to hit mock server
	origResolve := appClient.resolveInstallationID
	appClient.resolveInstallationID = func(ctx context.Context, o, r string) (int64, error) {
		client := github.NewClient(server.Client())
		client.BaseURL = mustParseBaseURL(server.URL)
		inst, _, err := client.Apps.FindRepositoryInstallation(ctx, o, r)
		if err != nil || inst == nil || inst.ID == nil {
			return 0, err
		}
		return *inst.ID, nil
	}
	origGet := appClient.getInstallationToken
	appClient.getInstallationToken = func(ctx context.Context, installationID int64) (string, error) {
		client := github.NewClient(server.Client())
		client.BaseURL = mustParseBaseURL(server.URL)
		tok, _, err := client.Apps.CreateInstallationToken(ctx, installationID, &github.InstallationTokenOptions{})
		if err != nil {
			return "", err
		}
		appClient.tokenCache.mutex.Lock()
		appClient.tokenCache.tokens[installationID] = &TokenEntry{Token: tok.GetToken(), ExpiresAt: tok.ExpiresAt.Time}
		appClient.tokenCache.mutex.Unlock()
		return tok.GetToken(), nil
	}
	defer func() {
		appClient.resolveInstallationID = origResolve
		appClient.getInstallationToken = origGet
	}()

	ctx := context.Background()
	ghc, err := appClient.ForRepo(ctx, owner, repo)
	if err != nil {
		t.Fatalf("ForRepo error: %v", err)
	}
	ghc.BaseURL = mustParseBaseURL(server.URL)
	repository, _, err := ghc.Repositories.Get(ctx, owner, repo)
	if err != nil {
		t.Fatalf("Repositories.Get error: %v", err)
	}
	if repository == nil || repository.GetName() != repo {
		t.Fatalf("unexpected repository response")
	}
}
