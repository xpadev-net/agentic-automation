package mocks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// IssueComment is a minimal shape compatible with go-github IssueComment JSON.
type IssueComment struct {
	Body *string `json:"body"`
}

// ErrorMode controls simulated error behaviors per endpoint.
type ErrorMode struct {
	// When true, GET /issues/{number}/comments responds with 500.
	List500 bool
	// When true, POST /issues/{number}/comments responds with 500.
	Post500 bool
	// When true, simulate network drop by hijacking connection (here: 500 + close).
	DropConnection bool
	// Optional: per-number failure injection for granular control
	List500For map[int]bool
	Post500For map[int]bool
}

// GitHubIssueCommentsServer mocks GitHub Issues comments endpoints used by go-github.
// It stores comments per issue/PR number (same issues API is used for PR discussions).
type GitHubIssueCommentsServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	comments map[int][]IssueComment
	postLog  []struct {
		Number int
		Body   string
	}
	errorMode ErrorMode
}

// NewGitHubIssueCommentsServer creates a new mock server instance.
func NewGitHubIssueCommentsServer() *GitHubIssueCommentsServer {
	s := &GitHubIssueCommentsServer{
		comments: make(map[int][]IssueComment),
		postLog: make([]struct {
			Number int
			Body   string
		}, 0),
		errorMode: ErrorMode{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/", s.handleRepos)
	s.srv = httptest.NewServer(mux)
	return s
}

// Close shuts down the underlying server.
func (s *GitHubIssueCommentsServer) Close() { s.srv.Close() }

// URL returns the base URL of this mock server.
func (s *GitHubIssueCommentsServer) URL() string { return s.srv.URL }

// Reset clears stored comments and logs.
func (s *GitHubIssueCommentsServer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments = make(map[int][]IssueComment)
	s.postLog = s.postLog[:0]
	s.errorMode = ErrorMode{}
}

// Seed adds an initial comment body to given issue/PR number.
func (s *GitHubIssueCommentsServer) Seed(number int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments[number] = append(s.comments[number], IssueComment{Body: strPtr(body)})
}

// SetErrorMode configures error behaviors.
func (s *GitHubIssueCommentsServer) SetErrorMode(mode ErrorMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errorMode = mode
}

// Posts returns a snapshot of POST operations recorded.
func (s *GitHubIssueCommentsServer) Posts() []struct {
	Number int
	Body   string
} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]struct {
		Number int
		Body   string
	}, len(s.postLog))
	copy(out, s.postLog)
	return out
}

func (s *GitHubIssueCommentsServer) handleRepos(w http.ResponseWriter, r *http.Request) {
	// Expected paths:
	// GET  /repos/{owner}/{repo}/issues/{number}/comments
	// POST /repos/{owner}/{repo}/issues/{number}/comments
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 6 || parts[0] != "repos" || parts[3] != "issues" || parts[5] != "comments" {
		http.NotFound(w, r)
		return
	}

	// number at index 4
	number, err := strconv.Atoi(parts[4])
	if err != nil {
		http.NotFound(w, r)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		if s.errorMode.DropConnection || s.errorMode.List500 || s.errorMode.List500For[number] {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		list := s.comments[number]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	case http.MethodPost:
		if s.errorMode.DropConnection || s.errorMode.Post500 || s.errorMode.Post500For[number] {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		var in IssueComment
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		body := ""
		if in.Body != nil {
			body = *in.Body
		}
		s.comments[number] = append(s.comments[number], IssueComment{Body: &body})
		s.postLog = append(s.postLog, struct {
			Number int
			Body   string
		}{Number: number, Body: body})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(IssueComment{Body: &body})
	default:
		http.NotFound(w, r)
	}
}

func strPtr(s string) *string { return &s }
