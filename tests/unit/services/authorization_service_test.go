package services_test

import (
	"agentic-automation/internal/clients"
	"agentic-automation/internal/services"
	testmocks "agentic-automation/tests/mocks"
	"context"
	"net/http"
	neturl "net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// buildTestGitHubClient constructs a clients.Client whose BaseURL points to the
// provided test server. It reuses oauth2-backed http.Client created by NewClient.
func buildTestGitHubClient(t *testing.T, baseURL string) *clients.Client {
	t.Helper()
	cli, err := clients.NewClient("test-token", zap.NewNop())
	require.NoError(t, err)

	// Point go-github client to the test server
	// Ensure trailing slash as required by go-github
	if baseURL[len(baseURL)-1] != '/' {
		baseURL = baseURL + "/"
	}
	cli.Client.BaseURL = mustParseURL(t, baseURL)
	// UploadURL is not used here, but set for completeness
	cli.Client.UploadURL = mustParseURL(t, baseURL)
	return cli
}

func mustParseURL(t *testing.T, raw string) *neturl.URL {
	t.Helper()
	u, err := neturl.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestAuthorizationService_CheckPermission_TableDriven(t *testing.T) {
	// Prepare routes for permission levels
	routes := map[string]struct {
		StatusCode int
		Permission string
	}{
		"org/repo/admin":      {StatusCode: http.StatusOK, Permission: "admin"},
		"org/repo/maintainer": {StatusCode: http.StatusOK, Permission: "maintain"},
		"org/repo/writer":     {StatusCode: http.StatusOK, Permission: "write"},
		"org/repo/reader":     {StatusCode: http.StatusOK, Permission: "read"},
		// Not present → 404
	}

	srv := testmocks.NewGitHubPermissionTestServer(routes)
	defer srv.Close()

	ghClient := buildTestGitHubClient(t, srv.URL)
	svc := services.NewAuthorizationService(ghClient, zap.NewNop())

	type testCase struct {
		name      string
		username  string
		wantAllow bool
		wantErr   bool
	}
	cases := []testCase{
		{name: "admin -> allow", username: "admin", wantAllow: true},
		{name: "maintain -> allow", username: "maintainer", wantAllow: true},
		{name: "write -> allow", username: "writer", wantAllow: true},
		{name: "read -> deny", username: "reader", wantAllow: false},
		{name: "404 -> deny", username: "unknown", wantAllow: false},
	}

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowed, err := svc.CheckPermission(ctx, "org", "repo", tc.username)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantAllow, allowed)
		})
	}
}

func TestAuthorizationService_CheckPermission_ServerError(t *testing.T) {
	// 500 error case to ensure error propagates
	routes := map[string]struct {
		StatusCode int
		Permission string
	}{
		"org/repo/fail": {StatusCode: http.StatusInternalServerError, Permission: ""},
	}
	srv := testmocks.NewGitHubPermissionTestServer(routes)
	defer srv.Close()

	ghClient := buildTestGitHubClient(t, srv.URL)
	svc := services.NewAuthorizationService(ghClient, zap.NewNop())

	ctx := context.Background()
	allowed, err := svc.CheckPermission(ctx, "org", "repo", "fail")
	require.Error(t, err)
	require.False(t, allowed)
}
