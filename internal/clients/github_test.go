package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	t.Setenv("GITHUB_APP_TEST_MODE", "1")
	// inject fake envs
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := NewGitHubAppClient(logger)
	if err != nil {
		t.Fatalf("NewGitHubAppClient error: %v", err)
	}

	// テスト用のBaseURLとHTTPクライアント（Transport）を注入
	u, _ := url.Parse(server.URL + "/")
	appClient.baseURL = u
	appClient.httpClient = server.Client()

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
	t.Setenv("GITHUB_APP_TEST_MODE", "1")
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := NewGitHubAppClient(logger)
	if err != nil {
		t.Fatalf("NewGitHubAppClient error: %v", err)
	}

	// テスト用のBaseURLとHTTPクライアント（Transport）を注入
	u2, _ := url.Parse(server.URL + "/")
	appClient.baseURL = u2
	appClient.httpClient = server.Client()

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

// mustParseBaseURL: 互換維持のためのダミー（使用箇所削除済）
func mustParseBaseURL(_ string) *url.URL { return nil }

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
			_ = json.NewEncoder(w).Encode(github.Repository{Name: github.String(repo)})
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	logger := newTestLogger(t)
	t.Setenv("GITHUB_APP_TEST_MODE", "1")
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := NewGitHubAppClient(logger)
	if err != nil {
		t.Fatalf("NewGitHubAppClient error: %v", err)
	}

	// テスト用のBaseURLとHTTPクライアント（Transport）を注入
	u, _ := url.Parse(server.URL + "/")
	appClient.baseURL = u
	appClient.httpClient = server.Client()

	ctx := context.Background()
	ghc, err := appClient.ForRepo(ctx, owner, repo)
	if err != nil {
		t.Fatalf("ForRepo error: %v", err)
	}
	ghc.BaseURL = u
	repository, _, err := ghc.Repositories.Get(ctx, owner, repo)
	if err != nil {
		t.Fatalf("Repositories.Get error: %v", err)
	}
	if repository == nil || repository.GetName() != repo {
		t.Fatalf("unexpected repository response")
	}
}
