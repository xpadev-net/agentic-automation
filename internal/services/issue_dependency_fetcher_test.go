package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentic-automation/internal/clients"
	"github.com/google/go-github/v76/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// --- Helpers ---

type recordedRequest struct {
	method  string
	path    string
	query   url.Values
	accept  string
	page    string
	perPage string
}

func issuesJSON(owner, repo string, list []struct {
	Number int
	Title  string
	State  string
	URL    string
}) string {
	type issueJSON struct {
		Number        int    `json:"number"`
		Title         string `json:"title"`
		State         string `json:"state"`
		HTMLURL       string `json:"html_url"`
		RepositoryURL string `json:"repository_url"`
	}
	out := make([]issueJSON, 0, len(list))
	for _, it := range list {
		out = append(out, issueJSON{
			Number:        it.Number,
			Title:         it.Title,
			State:         it.State,
			HTMLURL:       it.URL,
			RepositoryURL: fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo),
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func newWrappedGitHubClient(t *testing.T, base string) *clients.Client {
	t.Helper()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	gh := github.NewClient(httpClient)
	// go-github expects BaseURL to end with '/'
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	u, err := url.Parse(base)
	require.NoError(t, err)
	gh.BaseURL = u
	return clients.NewFromGitHub(gh, zaptest.NewLogger(t))
}

// --- Constructor tests ---

func TestNewIssueDependencyFetcher_Success(t *testing.T) {
	logger := zaptest.NewLogger(t)
	githubClient := &clients.Client{}
	svc := NewIssueDependencyFetcher(githubClient, logger)
	require.NotNil(t, svc)
}

func TestNewIssueDependencyFetcher_NilLogger(t *testing.T) {
	githubClient := &clients.Client{}
	svc := NewIssueDependencyFetcher(githubClient, nil)
	require.NotNil(t, svc)
}

func TestNewIssueDependencyFetcher_PanicOnNilClient(t *testing.T) {
	logger := zaptest.NewLogger(t)
	assert.Panics(t, func() { NewIssueDependencyFetcher(nil, logger) })
}

// --- Mapping tests ---

func TestParseOwnerRepoFromAPIURL(t *testing.T) {
	owner, repo := parseOwnerRepoFromAPIURL("https://api.github.com/repos/foo/bar")
	assert.Equal(t, "foo", owner)
	assert.Equal(t, "bar", repo)

	owner, repo = parseOwnerRepoFromAPIURL("https://api.github.com/repos/foo/bar/")
	assert.Equal(t, "foo", owner)
	assert.Equal(t, "bar", repo)

	owner, repo = parseOwnerRepoFromAPIURL("")
	assert.Equal(t, "", owner)
	assert.Equal(t, "", repo)

	owner, repo = parseOwnerRepoFromAPIURL("://bad-url::")
	assert.Equal(t, "", owner)
	assert.Equal(t, "", repo)
}

// --- API success tests ---

func TestListBlockedBy_SinglePage_Success(t *testing.T) {
	var lastReq atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/1/dependencies/blocked_by", func(w http.ResponseWriter, r *http.Request) {
		rr := recordedRequest{
			method:  r.Method,
			path:    r.URL.Path,
			query:   r.URL.Query(),
			accept:  r.Header.Get("Accept"),
			page:    r.URL.Query().Get("page"),
			perPage: r.URL.Query().Get("per_page"),
		}
		lastReq.Store(rr)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, issuesJSON("o", "r", []struct {
			Number            int
			Title, State, URL string
		}{
			{Number: 10, Title: "A", State: "open", URL: "https://github.com/o/r/issues/10"},
		}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newWrappedGitHubClient(t, srv.URL)
	logger := zaptest.NewLogger(t)
	svc := NewIssueDependencyFetcher(ghc, logger)

	ctx := context.Background()
	res, err := svc.ListBlockedBy(ctx, "o", "r", 1)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, DependencyIssue{Owner: "o", Repo: "r", Number: 10, Title: "A", State: "open", HTMLURL: "https://github.com/o/r/issues/10"}, res[0])

	// Header & query verification
	v := lastReq.Load().(recordedRequest)
	assert.Equal(t, "GET", v.method)
	assert.Equal(t, "/repos/o/r/issues/1/dependencies/blocked_by", v.path)
	assert.Equal(t, "application/vnd.github+json", v.accept)
	// Default page is 1 when not specified by go-github; our client sets query explicitly
	assert.Equal(t, "1", v.page)
	assert.Equal(t, "100", v.perPage)
}

func TestListBlocking_Pagination_Success(t *testing.T) {
	var page1Hit, page2Hit int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/1/dependencies/blocking", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page := q.Get("page")
		per := q.Get("per_page")
		if page == "" {
			page = "1"
		}
		if per == "" {
			per = "30"
		}
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			atomic.AddInt32(&page1Hit, 1)
			// Link to next page
			next := fmt.Sprintf("<%s/repos/o/r/issues/1/dependencies/blocking?page=2&per_page=%s>; rel=\"next\"", r.HostlessURL(), per)
			// r.HostlessURL is not real; construct with scheme+host
			// Build absolute URL
			base := r.URL
			baseCopy := *base
			baseCopy.Scheme = "http"
			if r.TLS != nil {
				baseCopy.Scheme = "https"
			}
			baseCopy.Host = r.Host
			baseCopy.RawQuery = "page=2&per_page=" + per
			next = fmt.Sprintf("<%s://%s%s?%s>; rel=\"next\"", baseCopy.Scheme, baseCopy.Host, baseCopy.Path, baseCopy.RawQuery)
			w.Header().Set("Link", next)
			fmt.Fprint(w, issuesJSON("o", "r", []struct {
				Number            int
				Title, State, URL string
			}{
				{Number: 11, Title: "P1", State: "open", URL: "https://github.com/o/r/issues/11"},
			}))
		case "2":
			atomic.AddInt32(&page2Hit, 1)
			fmt.Fprint(w, issuesJSON("o", "r", []struct {
				Number            int
				Title, State, URL string
			}{
				{Number: 12, Title: "P2", State: "closed", URL: "https://github.com/o/r/issues/12"},
				{Number: 13, Title: "P3", State: "open", URL: "https://github.com/o/r/issues/13"},
			}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newWrappedGitHubClient(t, srv.URL)
	svc := NewIssueDependencyFetcher(ghc, zaptest.NewLogger(t))

	ctx := context.Background()
	res, err := svc.ListBlocking(ctx, "o", "r", 1)
	require.NoError(t, err)
	require.Len(t, res, 3)
	assert.Equal(t, 1, int(page1Hit))
	assert.Equal(t, 1, int(page2Hit))

	// spot check mapped values
	assert.Equal(t, 11, res[0].Number)
	assert.Equal(t, "o", res[0].Owner)
	assert.Equal(t, "r", res[0].Repo)
}

// --- Error tests ---

func TestListBlocking_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/1/dependencies/blocking", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newWrappedGitHubClient(t, srv.URL)
	svc := NewIssueDependencyFetcher(ghc, zaptest.NewLogger(t))

	ctx := context.Background()
	_, err := svc.ListBlocking(ctx, "o", "r", 1)
	require.Error(t, err)
}

func TestListBlocking_InvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/1/dependencies/blocking", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[{invalid json]")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newWrappedGitHubClient(t, srv.URL)
	svc := NewIssueDependencyFetcher(ghc, zaptest.NewLogger(t))

	ctx := context.Background()
	_, err := svc.ListBlocking(ctx, "o", "r", 1)
	require.Error(t, err)
}

func TestListBlocking_ContextTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/1/dependencies/blocking", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, issuesJSON("o", "r", nil))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ghc := newWrappedGitHubClient(t, srv.URL)
	svc := NewIssueDependencyFetcher(ghc, zaptest.NewLogger(t))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := svc.ListBlocking(ctx, "o", "r", 1)
	require.Error(t, err)
}
