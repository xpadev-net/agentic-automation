package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.UISession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func testLogger() *config.AppLogger {
	return config.GetLogger()
}

func newTestConfig(oauthURL, apiURL string) *Config {
	if oauthURL == "" {
		oauthURL = "https://github.com"
	}
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	return &Config{
		ClientID:     "cid",
		ClientSecret: "csecret",
		PublicURL:    "http://localhost:3000",
		SessionTTL:   time.Hour,
		OAuthBaseURL: oauthURL,
		APIBaseURL:   apiURL,
	}
}

func newTestRouter(t *testing.T, cfg *Config, db *gorm.DB) (*gin.Engine, *Handler) {
	gin.SetMode(gin.TestMode)
	h, _ := NewHandler(cfg, db, testLogger())
	r := gin.New()
	h.RegisterRoutes(r)
	return r, h
}

func doRequest(r *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func cookieValue(w *httptest.ResponseRecorder, name string) string {
	for _, ck := range w.Result().Cookies() {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func testConfig() *Config { return newTestConfig("", "") }

func TestCryptoRoundTripAndFailures(t *testing.T) {
	key := testConfig().EncryptionKey()
	enc, err := encryptToken(key, "ghu_secret-token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(enc, "ghu_secret-token") {
		t.Fatalf("ciphertext leaks plaintext")
	}
	dec, err := decryptToken(key, enc)
	if err != nil || dec != "ghu_secret-token" {
		t.Fatalf("round trip mismatch: %q (%v)", dec, err)
	}
	if _, err := decryptToken([32]byte{1, 2, 3}, enc); err == nil {
		t.Fatalf("expected decrypt with wrong key to fail")
	}
	if _, err := decryptToken(key, "not-base64!!"); err == nil {
		t.Fatalf("expected invalid base64 to fail")
	}
}

func TestConfigBasics(t *testing.T) {
	cfg := newTestConfig("", "")
	if !cfg.Enabled() {
		t.Fatalf("expected enabled with all fields set")
	}
	if cfg.SecureCookies() {
		t.Fatalf("http public url must not set Secure")
	}
	cfg.PublicURL = "https://ops.example.com"
	if !cfg.SecureCookies() {
		t.Fatalf("https public url must set Secure")
	}
	cfg.ClientID = ""
	if cfg.Enabled() {
		t.Fatalf("expected disabled without client id")
	}
}

func TestLoginRedirectsToGitHubWithState(t *testing.T) {
	db := setupTestDB(t)
	r, _ := newTestRouter(t, testConfig(), db)

	w := doRequest(r, http.MethodGet, "/auth/github/login", nil)
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://github.com/login/oauth/authorize?") {
		t.Fatalf("unexpected redirect: %s", loc)
	}
	if !strings.Contains(loc, "client_id=cid") ||
		!strings.Contains(loc, "redirect_uri=http%3A%2F%2Flocalhost%3A3000%2Fauth%2Fgithub%2Fcallback") {
		t.Fatalf("missing params in redirect: %s", loc)
	}
	if cookieValue(w, oauthStateCookieName) == "" {
		t.Fatalf("state cookie not set")
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	db := setupTestDB(t)
	r, _ := newTestRouter(t, testConfig(), db)

	w := doRequest(r, http.MethodGet, "/auth/github/callback?state=x&code=y", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// startFakeGitHub stands up mock OAuth-token and API servers.
func startFakeGitHub(t *testing.T, wantCode, wantSecret, login string) (oauthURL, apiURL string) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if q.Get("code") != wantCode || q.Get("client_secret") != wantSecret {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "ghu_test-token",
			"token_type":   "bearer",
		})
	}))
	t.Cleanup(oauth.Close)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer ghu_test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
	}))
	t.Cleanup(api.Close)
	return oauth.URL, api.URL
}

func TestCallbackCreatesSessionAndMeWorks(t *testing.T) {
	db := setupTestDB(t)
	oauthURL, apiURL := startFakeGitHub(t, "the-code", "csecret", "octocat")
	cfg := newTestConfig(oauthURL, apiURL)
	r, _ := newTestRouter(t, cfg, db)

	// Start login to mint a state cookie.
	wLogin := doRequest(r, http.MethodGet, "/auth/github/login", nil)
	state := cookieValue(wLogin, oauthStateCookieName)
	if state == "" {
		t.Fatalf("no state cookie")
	}

	// Complete callback with matching state.
	wCb := doRequest(r, http.MethodGet,
		"/auth/github/callback?state="+state+"&code=the-code",
		map[string]string{"Cookie": oauthStateCookieName + "=" + state})
	if wCb.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", wCb.Code, wCb.Body.String())
	}
	if loc := wCb.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %s", loc)
	}
	sessionID := cookieValue(wCb, sessionCookieName)
	if len(sessionID) != 64 {
		t.Fatalf("bad session id %q", sessionID)
	}
	if sessionID == sessionKey(sessionID) {
		t.Fatalf("cookie should hold the raw token, not its digest")
	}

	// Session row exists keyed by the hashed cookie value, with encrypted token.
	repo := repositories.NewUISessionRepository(db)
	sess, err := repo.GetByID(sessionKey(sessionID))
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if sess.GitHubLogin != "octocat" {
		t.Fatalf("wrong login %q", sess.GitHubLogin)
	}
	if strings.Contains(sess.AccessToken, "ghu_test-token") {
		t.Fatalf("token stored in plaintext")
	}
	tok, err := decryptToken(cfg.EncryptionKey(), sess.AccessToken)
	if err != nil || tok != "ghu_test-token" {
		t.Fatalf("token decrypt failed: %v", err)
	}

	// /api/ui/me works with the session cookie.
	wMe := doRequest(r, http.MethodGet, "/api/ui/me",
		map[string]string{"Cookie": sessionCookieName + "=" + sessionID})
	if wMe.Code != http.StatusOK {
		t.Fatalf("me failed: %d", wMe.Code)
	}
	var me map[string]string
	if err := json.Unmarshal(wMe.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me["login"] != "octocat" {
		t.Fatalf("me login mismatch: %v", me)
	}
}

func TestMeRequiresSession(t *testing.T) {
	db := setupTestDB(t)
	r, _ := newTestRouter(t, testConfig(), db)

	w := doRequest(r, http.MethodGet, "/api/ui/me", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	w = doRequest(r, http.MethodGet, "/api/ui/me",
		map[string]string{"Cookie": sessionCookieName + "=bogus"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bogus session, got %d", w.Code)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()
	r, _ := newTestRouter(t, cfg, db)

	repo := repositories.NewUISessionRepository(db)
	if err := repo.Create(&models.UISession{
		ID:          sessionKey("deadbeef"),
		GitHubLogin: "ghost",
		AccessToken: "x",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := doRequest(r, http.MethodGet, "/api/ui/me",
		map[string]string{"Cookie": sessionCookieName + "=deadbeef"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired session, got %d", w.Code)
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	db := setupTestDB(t)
	r, _ := newTestRouter(t, testConfig(), db)

	repo := repositories.NewUISessionRepository(db)
	if err := repo.Create(&models.UISession{
		ID:          sessionKey("s1"),
		GitHubLogin: "u",
		AccessToken: "x",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := doRequest(r, http.MethodPost, "/auth/logout",
		map[string]string{"Cookie": sessionCookieName + "=s1"})
	if w.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", w.Code)
	}
	if _, err := repo.GetByID(sessionKey("s1")); err == nil {
		t.Fatalf("session not deleted")
	}
}
