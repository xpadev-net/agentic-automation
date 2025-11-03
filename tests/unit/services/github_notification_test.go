package services_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"agentic-automation/internal/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockIssueCommentsAPI creates a test server that mocks GitHub issue comments endpoints
// It supports:
// - GET /repos/:owner/:repo/issues/:number/comments
// - POST /repos/:owner/:repo/issues/:number/comments
func mockIssueCommentsAPI(t *testing.T, existingByNumber map[int][]string, createdByNumber map[int][]string) *httptest.Server {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expected paths: /repos/:owner/:repo/issues/:number/comments
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 6 || parts[0] != "repos" || parts[3] != "issues" || parts[5] != "comments" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// owner := parts[1]
		// repo := parts[2]
		numStr := parts[4]
		n, err := strconv.Atoi(numStr)
		require.NoError(t, err)

		switch r.Method {
		case http.MethodGet:
			// Return existing comments
			bodies := existingByNumber[n]
			type comment struct {
				Body string `json:"body"`
			}
			resp := make([]comment, 0, len(bodies))
			for _, b := range bodies {
				resp = append(resp, comment{Body: b})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case http.MethodPost:
			// Capture created comment
			var payload struct {
				Body string `json:"body"`
			}
			dec := json.NewDecoder(r.Body)
			_ = dec.Decode(&payload)
			createdByNumber[n] = append(createdByNumber[n], payload.Body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "body": payload.Body})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	return httptest.NewServer(handler)
}

func TestNotifyPRCreated_PostsToIssueAndPR(t *testing.T) {
	existing := map[int][]string{}
	created := map[int][]string{}
	srv := mockIssueCommentsAPI(t, existing, created)
	t.Cleanup(srv.Close)

	ghClient := buildTestGitHubClient(t, srv.URL)
	svc := services.NewGitHubNotificationService(ghClient, zap.NewNop())

	err := svc.NotifyPRCreated(
		t.Context(),
		"o", "r",
		10, // issue number
		42, // pr number
		"https://github.com/o/r/pull/42",
		"feature/x",
		"abc123def456",
		"idem-123",
	)
	require.NoError(t, err)

	// One comment on issue and one on PR
	assert.Len(t, created[10], 1)
	assert.Len(t, created[42], 1)

	// Body contains marker and formatted message with short SHA
	for _, body := range []string{created[10][0], created[42][0]} {
		assert.Contains(t, body, "<!-- agent:pr-created:idem-123 -->")
		assert.Contains(t, body, "PR created: #42 (https://github.com/o/r/pull/42) branch=feature/x sha=abc1234")
	}
}

func TestNotifyPRCreated_SkipsWhenMarkerExists(t *testing.T) {
	// Existing marker in both threads should skip posting
	marker := "<!-- agent:pr-created:idem-999 -->"
	existing := map[int][]string{
		10: {marker + "\nold"},
		42: {marker + "\nold"},
	}
	created := map[int][]string{}
	srv := mockIssueCommentsAPI(t, existing, created)
	t.Cleanup(srv.Close)

	ghClient := buildTestGitHubClient(t, srv.URL)
	svc := services.NewGitHubNotificationService(ghClient, zap.NewNop())

	err := svc.NotifyPRCreated(
		t.Context(),
		"o", "r",
		10,
		42,
		"https://github.com/o/r/pull/42",
		"feature/x",
		"abc123def456",
		"idem-999",
	)
	require.NoError(t, err)

	// No new comments should be created
	assert.Len(t, created[10], 0)
	assert.Len(t, created[42], 0)
}

func TestNotifyPRCreated_RetryOnTransientFailure(t *testing.T) {
	// Arrange a server that fails the first POST to issue 10 with 500, then succeeds
	var issuePostCount int
	existing := map[int][]string{}
	created := map[int][]string{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 6 || parts[0] != "repos" || parts[3] != "issues" || parts[5] != "comments" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n, _ := strconv.Atoi(parts[4])
		switch r.Method {
		case http.MethodGet:
			type comment struct {
				Body string `json:"body"`
			}
			_ = json.NewEncoder(w).Encode([]comment{})
		case http.MethodPost:
			if n == 10 {
				issuePostCount++
				if issuePostCount == 1 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
			var payload struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			created[n] = append(created[n], payload.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "body": payload.Body})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ghClient := buildTestGitHubClient(t, srv.URL)
	svc := services.NewGitHubNotificationService(ghClient, zap.NewNop())

	err := svc.NotifyPRCreated(
		t.Context(),
		"o", "r",
		10,
		42,
		"https://github.com/o/r/pull/42",
		"feature/x",
		"abc123def456",
		"idem-retry",
	)
	require.NoError(t, err)

	// Should have eventually created on both threads
	require.Len(t, created[10], 1)
	require.Len(t, created[42], 1)

	// Verify that it retried at least once on issue 10
	require.GreaterOrEqual(t, issuePostCount, 2)
}
