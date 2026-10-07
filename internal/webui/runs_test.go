package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const runsDDL = `
CREATE TABLE IF NOT EXISTS issues (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	repo TEXT,
	number INTEGER,
	title TEXT
);
CREATE TABLE IF NOT EXISTS agent_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	idempotency_key TEXT,
	issue_id INTEGER,
	pr_id INTEGER,
	state TEXT DEFAULT 'queued',
	agent_type TEXT DEFAULT 'claude-code',
	execution_mode TEXT DEFAULT 'normal',
	job_name TEXT,
	retry_count INTEGER DEFAULT 0,
	error_message TEXT,
	started_at DATETIME,
	completed_at DATETIME,
	created_at DATETIME,
	updated_at DATETIME
);`

func setupRunsTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	for _, stmt := range strings.Split(runsDDL, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	if err := db.AutoMigrate(&models.UISession{}, &models.AgentRunLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// fakePermAPI serves /repos/* and /repos/*/collaborators/*/permission.
func fakePermAPI(t *testing.T, visible map[string]bool, perms map[string]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/collaborators/") {
			parts := strings.Split(r.URL.Path, "/")
			repo := parts[2] + "/" + parts[3]
			if p, ok := perms[repo]; ok {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"permission":%q}`, p)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			repo := strings.TrimPrefix(r.URL.Path, "/repos/")
			if visible[repo] {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// seedSession inserts a session row and returns the Cookie header value.
func seedSession(t *testing.T, db *gorm.DB, cfg *Config, login string) string {
	enc, err := encryptToken(cfg.EncryptionKey(), "ghu_test")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	sess := &models.UISession{
		ID: sessionKey("sess-" + login), GitHubLogin: login, AccessToken: enc,
		ExpiresAt: time.Now().Add(time.Hour), LastSeenAt: time.Now(),
	}
	if err := repositories.NewUISessionRepository(db).Create(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sessionCookieName + "=" + "sess-" + login
}

func seedRun(t *testing.T, db *gorm.DB, repo, state string) int {
	res := db.Exec(`INSERT INTO issues (repo, number, title) VALUES (?, 1, ?)`, repo, "issue in "+repo)
	if res.Error != nil {
		t.Fatalf("seed issue: %v", res.Error)
	}
	var issueID int64
	if err := db.Raw("SELECT id FROM issues WHERE repo = ?", repo).Scan(&issueID).Error; err != nil {
		t.Fatalf("issue id: %v", err)
	}
	if err := db.Exec(`INSERT INTO agent_runs (issue_id, state, agent_type, execution_mode, retry_count, created_at, updated_at)
		VALUES (?, ?, 'claude-code', 'normal', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, issueID, state).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	var runID int64
	if err := db.Raw("SELECT MAX(id) FROM agent_runs").Scan(&runID).Error; err != nil {
		t.Fatalf("run id: %v", err)
	}
	return int(runID)
}

func authed(r *gin.Engine, path, cookie string) *httptest.ResponseRecorder {
	return doRequest(r, http.MethodGet, path, map[string]string{"Cookie": cookie})
}

func TestListRunsFiltersByRepoPermission(t *testing.T) {
	db := setupRunsTestDB(t)
	api := fakePermAPI(t, map[string]bool{"o/visible": true}, nil)
	cfg := newTestConfig("", api.URL)
	r, _ := newTestRouter(t, cfg, db)
	cookie := seedSession(t, db, cfg, "me")

	seedRun(t, db, "o/visible", "succeeded")
	seedRun(t, db, "o/hidden", "succeeded")

	w := authed(r, "/api/ui/runs", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Runs []runSummary `json:"runs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Runs) != 1 || resp.Runs[0].Repo != "o/visible" {
		t.Fatalf("permission filter failed: %+v", resp.Runs)
	}
}

func TestGetRunAndLogs(t *testing.T) {
	db := setupRunsTestDB(t)
	api := fakePermAPI(t, map[string]bool{"o/r": true}, nil)
	cfg := newTestConfig("", api.URL)
	r, _ := newTestRouter(t, cfg, db)
	cookie := seedSession(t, db, cfg, "me")

	runID := seedRun(t, db, "o/r", "succeeded")
	logRepo := repositories.NewAgentRunLogRepository(db)
	now := time.Now()
	if _, err := logRepo.CreateBatch([]*models.AgentRunLog{
		{AgentRunID: runID, Seq: 1, TS: &now, Line: "l1"},
		{AgentRunID: runID, Seq: 2, Line: "l2"},
		{AgentRunID: runID, Seq: 3, Line: "l3"},
	}); err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	w := authed(r, fmt.Sprintf("/api/ui/runs/%d", runID), cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("get run: %d", w.Code)
	}
	var detail map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail["logs_count"] != float64(3) {
		t.Fatalf("logs_count wrong: %v", detail)
	}

	w = authed(r, fmt.Sprintf("/api/ui/runs/%d/logs?after_seq=1&limit=1", runID), cookie)
	var logs struct {
		Entries []struct {
			Seq  int64  `json:"seq"`
			Line string `json:"line"`
		} `json:"entries"`
		HasMore bool `json:"has_more"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &logs)
	if len(logs.Entries) != 1 || logs.Entries[0].Seq != 2 || !logs.HasMore {
		t.Fatalf("logs paging wrong: %+v", logs)
	}
}

func TestGetRunLogsForbidden(t *testing.T) {
	db := setupRunsTestDB(t)
	api := fakePermAPI(t, map[string]bool{}, nil)
	cfg := newTestConfig("", api.URL)
	r, _ := newTestRouter(t, cfg, db)
	cookie := seedSession(t, db, cfg, "me")

	runID := seedRun(t, db, "o/hidden", "succeeded")
	w := authed(r, fmt.Sprintf("/api/ui/runs/%d/logs", runID), cookie)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	w = authed(r, "/api/ui/runs/9999", cookie)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestStreamRunLogsReplaysAndEnds(t *testing.T) {
	db := setupRunsTestDB(t)
	api := fakePermAPI(t, map[string]bool{"o/r": true}, nil)
	cfg := newTestConfig("", api.URL)
	r, _ := newTestRouter(t, cfg, db)
	cookie := seedSession(t, db, cfg, "me")

	runID := seedRun(t, db, "o/r", "succeeded")
	logRepo := repositories.NewAgentRunLogRepository(db)
	if _, err := logRepo.CreateBatch([]*models.AgentRunLog{
		{AgentRunID: runID, Seq: 1, Line: "alpha"},
		{AgentRunID: runID, Seq: 2, Line: "beta"},
	}); err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/ui/runs/%d/logs/stream", runID), nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"line":"alpha"`) || !strings.Contains(body, `"line":"beta"`) {
		t.Fatalf("replay missing: %s", body)
	}
	if !strings.Contains(body, "event: end") || !strings.Contains(body, `"state":"succeeded"`) {
		t.Fatalf("end event missing: %s", body)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("not sse content type: %s", w.Header().Get("Content-Type"))
	}
}

func TestPermCheckerWriteMode(t *testing.T) {
	api := fakePermAPI(t, nil, map[string]string{
		"o/admin":  "admin",
		"o/reader": "read",
	})
	p := newPermChecker(api.URL)
	if !p.Allowed(t.Context(), "tok", "me", "o/admin", "write") {
		t.Fatalf("admin should be allowed in write mode")
	}
	if p.Allowed(t.Context(), "tok", "me", "o/reader", "write") {
		t.Fatalf("read collaborator must not pass write mode")
	}
	// Cached result reused (server would 500 on unexpected call if re-queried).
	if !p.Allowed(t.Context(), "tok", "me", "o/admin", "write") {
		t.Fatalf("cached result lost")
	}
}
