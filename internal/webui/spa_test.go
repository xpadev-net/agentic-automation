package webui

import (
	"net/http"
	"strings"
	"testing"
)

func TestSPAServesIndexAndAssets(t *testing.T) {
	db := setupTestDB(t)
	r, _ := newTestRouter(t, testConfig(), db)

	w := doRequest(r, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Agent Runs") {
		t.Fatalf("index: %d %q", w.Code, w.Body.String()[:80])
	}
	for _, p := range []string{"/app.js", "/style.css"} {
		w = doRequest(r, http.MethodGet, p, nil)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("asset %s: %d", p, w.Code)
		}
	}
}

func TestSPAFallbackForDeepLinks(t *testing.T) {
	db := setupTestDB(t)
	r, _ := newTestRouter(t, testConfig(), db)

	w := doRequest(r, http.MethodGet, "/runs/123", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Agent Runs") {
		t.Fatalf("deep link should serve SPA: %d", w.Code)
	}
	// Unknown API paths stay JSON 404s, not the SPA.
	w = doRequest(r, http.MethodGet, "/api/ui/unknown", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("api 404 expected, got %d", w.Code)
	}
}
