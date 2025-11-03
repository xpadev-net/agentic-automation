package mocks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

// PermissionResponse represents a simplified GitHub API response for permission level.
type PermissionResponse struct {
	Permission string `json:"permission"`
}

// NewGitHubPermissionTestServer creates an httptest server that mimics the GitHub
// GetPermissionLevel endpoint used by go-github:
// GET /repos/{owner}/{repo}/collaborators/{username}/permission
//
// routes is a map keyed by "owner/repo/username" → (status, permission).
// If key is not found, it returns 404.
func NewGitHubPermissionTestServer(routes map[string]struct {
	StatusCode int
	Permission string
}) *httptest.Server {
	handler := http.NewServeMux()

	handler.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		// Expected path: /repos/{owner}/{repo}/collaborators/{username}/permission
		// Split and extract segments
		// ["repos", owner, repo, "collaborators", username, "permission"]
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 6 || parts[0] != "repos" || parts[3] != "collaborators" || parts[5] != "permission" {
			http.NotFound(w, r)
			return
		}

		owner := parts[1]
		repo := parts[2]
		username := parts[4]
		key := owner + "/" + repo + "/" + username

		if route, ok := routes[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(route.StatusCode)
			if route.StatusCode == http.StatusOK {
				_ = json.NewEncoder(w).Encode(PermissionResponse{Permission: route.Permission})
			} else {
				// Minimal error body
				_ = json.NewEncoder(w).Encode(map[string]string{"message": http.StatusText(route.StatusCode)})
			}
			return
		}

		// Default: not found
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	})

	return httptest.NewServer(handler)
}
