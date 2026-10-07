package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// permCacheTTL is how long a positive or negative permission result is reused.
const permCacheTTL = 5 * time.Minute

type permCacheEntry struct {
	allowed bool
	expires time.Time
}

// permChecker answers "may this GitHub user see repo contents?" using the
// user's own OAuth token. Results are cached briefly to avoid hammering the
// GitHub API on every poll.
type permChecker struct {
	apiBase string
	client  *http.Client

	mu    sync.Mutex
	cache map[string]permCacheEntry
}

func newPermChecker(apiBase string) *permChecker {
	return &permChecker{
		apiBase: strings.TrimRight(apiBase, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
		cache:   make(map[string]permCacheEntry),
	}
}

// Allowed reports whether login may view repo ("owner/name") at the given
// minimum level ("read" = repo visible, "write" = push permission).
func (p *permChecker) Allowed(ctx context.Context, token, login, repo, minPerm string) bool {
	if token == "" || repo == "" {
		return false
	}
	key := login + "|" + repo + "|" + minPerm
	if v, ok := p.get(key); ok {
		return v
	}
	var ok bool
	if minPerm == "write" {
		ok = p.hasWritePermission(ctx, token, login, repo)
	} else {
		ok = p.canSeeRepo(ctx, token, repo)
	}
	p.put(key, ok)
	return ok
}

func (p *permChecker) get(key string) (bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[key]
	if !ok || time.Now().After(e.expires) {
		return false, false
	}
	return e.allowed, true
}

func (p *permChecker) put(key string, allowed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.cache) > 4096 {
		p.cache = make(map[string]permCacheEntry)
	}
	p.cache[key] = permCacheEntry{allowed: allowed, expires: time.Now().Add(permCacheTTL)}
}

func (p *permChecker) getJSON(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// canSeeRepo returns true when GET /repos/{repo} succeeds with the user token
// (works for public repos and private repos the user/app can see).
func (p *permChecker) canSeeRepo(ctx context.Context, token, repo string) bool {
	status, err := p.getJSON(ctx, token, fmt.Sprintf("/repos/%s", repo), nil)
	return err == nil && status == http.StatusOK
}

type collaboratorPermission struct {
	Permission string `json:"permission"`
}

// hasWritePermission returns true when the user's collaborator permission on
// the repo is write/maintain/admin.
func (p *permChecker) hasWritePermission(ctx context.Context, token, login, repo string) bool {
	var out collaboratorPermission
	status, err := p.getJSON(ctx, token,
		fmt.Sprintf("/repos/%s/collaborators/%s/permission", repo, login), &out)
	if err != nil || status != http.StatusOK {
		return false
	}
	switch strings.ToLower(out.Permission) {
	case "admin", "maintain", "write":
		return true
	default:
		return false
	}
}
