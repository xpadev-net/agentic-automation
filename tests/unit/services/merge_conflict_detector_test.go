package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	libclients "agentic-automation/internal/clients"
	"agentic-automation/internal/config"
	libservices "agentic-automation/internal/services"

	"github.com/google/go-github/v76/github"
)

// helper to create a GitHub client pointing to a test server
func newGitHubClientForServer(t *testing.T, srv *httptest.Server) *github.Client {
	t.Helper()
	baseURL, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("failed to parse server url: %v", err)
	}
	cli := github.NewClient(srv.Client())
	cli.BaseURL = baseURL
	return cli
}

// stub handler for GET /repos/{owner}/{repo}/pulls/{number}
func prHandler(t *testing.T, responses []map[string]any) http.HandlerFunc {
	t.Helper()
	idx := 0
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// naive path check
		// expected: /repos/owner/repo/pulls/123
		if r.URL.Path == "" || idx >= len(responses) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := responses[idx]
		idx++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestDetect_NoConflict_Clean(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": true, "mergeable_state": "clean"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusNoConflict {
		t.Fatalf("expected no_conflict, got %s", status)
	}
}

func TestDetect_HasConflict_Dirty(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": true, "mergeable_state": "dirty"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/2", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusHasConflict {
		t.Fatalf("expected has_conflict, got %s", status)
	}
}

func TestDetect_HasConflict_MergeableFalse(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": false, "mergeable_state": "dirty"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/3", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusHasConflict {
		t.Fatalf("expected has_conflict, got %s", status)
	}
}

func TestDetect_Unknown_WhenMergeableNilAfterRetries(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": nil},
		{"mergeable": nil},
		{"mergeable": nil},
		{"mergeable": nil}, // initial + 3 retries = 4 fetches
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/4", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	// tighten context timeout to avoid long waits in CI
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	status, err := detector.Detect(ctx, "o", "r", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusUnknown {
		t.Fatalf("expected unknown, got %s", status)
	}
}

func TestDetect_NoConflict_StateBlocked(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": true, "mergeable_state": "blocked"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/5", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusNoConflict {
		t.Fatalf("expected no_conflict, got %s", status)
	}
}

func TestDetect_NoConflict_WhenMergeableFalse_Blocked(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": false, "mergeable_state": "blocked"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/6", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusNoConflict {
		t.Fatalf("expected no_conflict, got %s", status)
	}
}

func TestDetect_NoConflict_WhenMergeableFalse_Behind(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": false, "mergeable_state": "behind"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/7", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusNoConflict {
		t.Fatalf("expected no_conflict, got %s", status)
	}
}

func TestDetect_NoConflict_WhenMergeableFalse_Draft(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": false, "mergeable_state": "draft"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/8", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusNoConflict {
		t.Fatalf("expected no_conflict, got %s", status)
	}
}

func TestDetect_Unknown_WhenMergeableFalse_StateNil(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": false},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/9", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusUnknown {
		t.Fatalf("expected unknown, got %s", status)
	}
}

func TestDetect_Unknown_WhenMergeableFalse_StateUnknown(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": false, "mergeable_state": "unknown"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/10", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusUnknown {
		t.Fatalf("expected unknown, got %s", status)
	}
}

func TestDetect_Unknown_WhenMergeableTrue_StateUnknown(t *testing.T) {
	responses := []map[string]any{
		{"mergeable": true, "mergeable_state": "unknown"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/11", prHandler(t, responses))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newGitHubClientForServer(t, srv)
	wrapped := libclients.NewFromGitHub(ghc, config.NewNopLogger())
	detector := libservices.NewMergeConflictDetector(wrapped, config.NewNopLogger())

	ctx := context.Background()
	status, err := detector.Detect(ctx, "o", "r", 11)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != libservices.MergeConflictStatusUnknown {
		t.Fatalf("expected unknown, got %s", status)
	}
}
