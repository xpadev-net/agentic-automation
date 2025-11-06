package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	libclients "agentic-automation/internal/clients"
	libservices "agentic-automation/internal/services"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// installationResp is the response for installation endpoint
type installationResp struct {
	ID int64 `json:"id"`
}

// tokenResp is the response for token endpoint
type tokenResp struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// newGitHubAppClientForServer creates a GitHubClient for testing that points to the test server
func newGitHubAppClientForServer(t *testing.T, srv *httptest.Server) *libclients.GitHubClient {
	t.Helper()

	t.Setenv("GITHUB_APP_TEST_MODE", "1")
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIB...test...\n-----END PRIVATE KEY-----\n")

	appClient, err := libclients.NewGitHubAppClient(zap.NewNop())
	require.NoError(t, err)

	// Inject test server URL and HTTP client
	u, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	appClient.SetTestServer(u, srv.Client())

	return appClient
}

// installationHandler handles GET /repos/{owner}/{repo}/installation
func installationHandler(t *testing.T, installationID int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(installationResp{ID: installationID})
	}
}

// tokenHandler handles POST /app/installations/{id}/access_tokens
func tokenHandler(t *testing.T, token string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResp{
			Token:     token,
			ExpiresAt: time.Now().Add(55 * time.Minute),
		})
	}
}

// mergeHandler handles PUT /repos/{owner}/{repo}/pulls/{number}/merge
func mergeHandler(t *testing.T, statusCode int, responseBody map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if responseBody != nil {
			_ = json.NewEncoder(w).Encode(responseBody)
		}
	}
}

// isMergedHandler handles GET /repos/{owner}/{repo}/pulls/{number}/merge
func isMergedHandler(t *testing.T, isMerged bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if isMerged {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// prHandlerSingle handles GET /repos/{owner}/{repo}/pulls/{number} (single response)
func prHandlerSingle(t *testing.T, prData map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prData)
	}
}

// deleteRefHandler handles DELETE /repos/{owner}/{repo}/git/refs/heads/{branch}
func deleteRefHandler(t *testing.T, shouldSucceed bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if shouldSucceed {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Internal Server Error"})
		}
	}
}

// ============================================================================
// Input Validation Tests
// ============================================================================

func TestAttemptAutoMerge_InvalidInput_EmptyOwner(t *testing.T) {
	service := libservices.NewAutoMergeService(nil, zap.NewNop())
	result, err := service.AttemptAutoMerge(context.Background(), "", "repo", 1)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid input")
}

func TestAttemptAutoMerge_InvalidInput_EmptyRepo(t *testing.T) {
	service := libservices.NewAutoMergeService(nil, zap.NewNop())
	result, err := service.AttemptAutoMerge(context.Background(), "owner", "", 1)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid input")
}

func TestAttemptAutoMerge_InvalidInput_ZeroPRNumber(t *testing.T) {
	service := libservices.NewAutoMergeService(nil, zap.NewNop())
	result, err := service.AttemptAutoMerge(context.Background(), "owner", "repo", 0)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid input")
}

func TestAttemptAutoMerge_InvalidInput_NegativePRNumber(t *testing.T) {
	service := libservices.NewAutoMergeService(nil, zap.NewNop())
	result, err := service.AttemptAutoMerge(context.Background(), "owner", "repo", -1)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid input")
}

// ============================================================================
// ClassifyMergeError Tests
// ============================================================================

func TestClassifyMergeError_Nil(t *testing.T) {
	result := libservices.ClassifyMergeError(nil)
	assert.Equal(t, "", result)
}

func TestClassifyMergeError_409(t *testing.T) {
	err := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 409,
		},
		Message: "Conflict",
	}
	result := libservices.ClassifyMergeError(err)
	assert.Equal(t, "merge_conflict_or_not_mergeable", result)
}

func TestClassifyMergeError_422(t *testing.T) {
	err := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 422,
		},
		Message: "Unprocessable",
	}
	result := libservices.ClassifyMergeError(err)
	assert.Equal(t, "merge_rejected_by_protection_or_reviews", result)
}

func TestClassifyMergeError_429(t *testing.T) {
	err := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 429,
		},
		Message: "Rate limited",
	}
	result := libservices.ClassifyMergeError(err)
	assert.Equal(t, "rate_limited", result)
}

func TestClassifyMergeError_500(t *testing.T) {
	err := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 500,
		},
		Message: "Internal Server Error",
	}
	result := libservices.ClassifyMergeError(err)
	assert.Equal(t, "github_server_error", result)
}

func TestClassifyMergeError_503(t *testing.T) {
	err := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 503,
		},
		Message: "Service Unavailable",
	}
	result := libservices.ClassifyMergeError(err)
	assert.Equal(t, "github_server_error", result)
}

func TestClassifyMergeError_Other(t *testing.T) {
	err := &github.ErrorResponse{
		Response: &http.Response{
			StatusCode: 400,
		},
		Message: "Bad Request",
	}
	result := libservices.ClassifyMergeError(err)
	// github.ErrorResponse.Error() returns "400 Bad Request []" format
	assert.Contains(t, result, "Bad Request")
}

func TestClassifyMergeError_NonGitHubError(t *testing.T) {
	err := errors.New("some other error")
	result := libservices.ClassifyMergeError(err)
	assert.Equal(t, "some other error", result)
}

// ============================================================================
// Merge Success Tests
// ============================================================================

func TestAttemptAutoMerge_Success(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", mergeHandler(t, http.StatusOK, map[string]any{
		"merged": true,
		"sha":    "abc123def456",
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "feature-branch",
			"repo": map[string]any{
				"full_name": owner + "/" + repo,
				"owner": map[string]any{
					"login": owner,
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/feature-branch", deleteRefHandler(t, true))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.Equal(t, "abc123def456", result.MergeSHA)
	assert.Equal(t, "merged", result.Message)
}

// ============================================================================
// Merge Error Tests
// ============================================================================

func TestAttemptAutoMerge_Conflict409(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusConflict, map[string]any{
				"message": "Conflict",
			})(w, r)
		} else if r.Method == http.MethodGet {
			isMergedHandler(t, false)(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Merged)
	assert.Equal(t, "merge_conflict_or_not_mergeable", result.ErrorType)
	assert.Equal(t, "merge_failed", result.Message)
}

func TestAttemptAutoMerge_Unprocessable422(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusUnprocessableEntity, map[string]any{
				"message": "Unprocessable",
			})(w, r)
		} else if r.Method == http.MethodGet {
			isMergedHandler(t, false)(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Merged)
	assert.Equal(t, "merge_rejected_by_protection_or_reviews", result.ErrorType)
	assert.Equal(t, "merge_failed", result.Message)
}

func TestAttemptAutoMerge_RateLimited429(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusTooManyRequests, map[string]any{
				"message": "Rate limited",
			})(w, r)
		} else if r.Method == http.MethodGet {
			isMergedHandler(t, false)(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Merged)
	assert.Equal(t, "rate_limited", result.ErrorType)
	assert.Equal(t, "merge_failed", result.Message)
}

func TestAttemptAutoMerge_ServerError500(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusInternalServerError, map[string]any{
				"message": "Internal Server Error",
			})(w, r)
		} else if r.Method == http.MethodGet {
			isMergedHandler(t, false)(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Merged)
	assert.Equal(t, "github_server_error", result.ErrorType)
	assert.Equal(t, "merge_failed", result.Message)
}

func TestAttemptAutoMerge_OtherError(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusBadRequest, map[string]any{
				"message": "Bad Request",
			})(w, r)
		} else if r.Method == http.MethodGet {
			isMergedHandler(t, false)(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Merged)
	// Other errors return the error message as-is
	assert.Contains(t, result.ErrorMessage, "Bad Request")
}

// ============================================================================
// Already Merged Tests
// ============================================================================

func TestAttemptAutoMerge_AlreadyMerged(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			// Merge API returns error
			mergeHandler(t, http.StatusInternalServerError, map[string]any{
				"message": "Internal Server Error",
			})(w, r)
		} else if r.Method == http.MethodGet {
			// But IsMerged returns true
			isMergedHandler(t, true)(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "feature-branch",
			"repo": map[string]any{
				"full_name": owner + "/" + repo,
				"owner": map[string]any{
					"login": owner,
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/feature-branch", deleteRefHandler(t, true))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.Equal(t, "merged", result.Message)
}

// ============================================================================
// Branch Deletion Tests
// ============================================================================

func TestAttemptAutoMerge_BranchDeleted_Success(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"
	branchDeleted := false

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusOK, map[string]any{
				"merged": true,
				"sha":    "abc123",
			})(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "feature-branch",
			"repo": map[string]any{
				"full_name": owner + "/" + repo,
				"owner": map[string]any{
					"login": owner,
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/feature-branch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			branchDeleted = true
			deleteRefHandler(t, true)(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.True(t, branchDeleted, "branch should be deleted")
}

func TestAttemptAutoMerge_BranchDeleted_SkipFork(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"
	branchDeleted := false

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusOK, map[string]any{
				"merged": true,
				"sha":    "abc123",
			})(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "feature-branch",
			"repo": map[string]any{
				"full_name": "fork-owner/" + repo,
				"owner": map[string]any{
					"login": "fork-owner",
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/feature-branch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			branchDeleted = true
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.False(t, branchDeleted, "fork branch should not be deleted")
}

func TestAttemptAutoMerge_BranchDeleted_SkipMain(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"
	branchDeleted := false

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusOK, map[string]any{
				"merged": true,
				"sha":    "abc123",
			})(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "main",
			"repo": map[string]any{
				"full_name": owner + "/" + repo,
				"owner": map[string]any{
					"login": owner,
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/main", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			branchDeleted = true
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.False(t, branchDeleted, "main branch should not be deleted")
}

func TestAttemptAutoMerge_BranchDeleted_SkipMaster(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"
	branchDeleted := false

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusOK, map[string]any{
				"merged": true,
				"sha":    "abc123",
			})(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "master",
			"repo": map[string]any{
				"full_name": owner + "/" + repo,
				"owner": map[string]any{
					"login": owner,
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/master", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			branchDeleted = true
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.False(t, branchDeleted, "master branch should not be deleted")
}

func TestAttemptAutoMerge_BranchDeleted_GetPRFailed(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"
	branchDeleted := false

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusOK, map[string]any{
				"merged": true,
				"sha":    "abc123",
			})(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			branchDeleted = true
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Merged)
	assert.False(t, branchDeleted, "branch should not be deleted when PR fetch fails")
}

func TestAttemptAutoMerge_BranchDeleted_DeleteFailed(t *testing.T) {
	owner := "owner"
	repo := "repo"
	prNumber := 1
	installationID := int64(789)
	testToken := "test-token-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/installation", installationHandler(t, installationID))
	mux.HandleFunc("/app/installations/789/access_tokens", tokenHandler(t, testToken))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mergeHandler(t, http.StatusOK, map[string]any{
				"merged": true,
				"sha":    "abc123",
			})(w, r)
		}
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/pulls/1", prHandlerSingle(t, map[string]any{
		"head": map[string]any{
			"ref": "feature-branch",
			"repo": map[string]any{
				"full_name": owner + "/" + repo,
				"owner": map[string]any{
					"login": owner,
				},
				"name": repo,
			},
		},
	}))
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/git/refs/heads/feature-branch", deleteRefHandler(t, false))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghClient := newGitHubAppClientForServer(t, srv)
	service := libservices.NewAutoMergeService(ghClient, zap.NewNop())

	result, err := service.AttemptAutoMerge(context.Background(), owner, repo, prNumber)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Branch deletion failure should not affect merge result
	assert.True(t, result.Merged)
	assert.Equal(t, "merged", result.Message)
}
