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
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Agent Runs") {
		t.Fatalf("index: %d %q", w.Code, body[:80])
	}
	// The built index.html references hashed assets under /assets/; those
	// must be served directly.
	for _, m := range assetPathPattern.FindAllString(body, -1) {
		w = doRequest(r, http.MethodGet, m, nil)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("asset %s: %d", m, w.Code)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Fatalf("asset %s: expected immutable cache, got %q", m, cc)
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
