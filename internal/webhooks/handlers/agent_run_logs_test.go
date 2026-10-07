package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/loghub"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupLogTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT,
			issue_id INTEGER,
			pr_id INTEGER,
			state TEXT DEFAULT 'queued',
			agent_type TEXT DEFAULT 'claude-code',
			execution_mode TEXT DEFAULT 'normal',
			job_name TEXT,
			plan_content TEXT,
			review_feedback_id INTEGER,
			plan_agent_run_id INTEGER,
			input TEXT,
			output TEXT,
			retry_count INTEGER DEFAULT 0,
			error_message TEXT,
			commit_sha TEXT,
			s3_session_key TEXT,
			session_saved_at DATETIME,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME,
			updated_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create agent_runs: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentRunLog{}); err != nil {
		t.Fatalf("create agent_run_logs: %v", err)
	}
	return db
}

func setupLogTestRouter(t *testing.T) *gin.Engine {
	t.Setenv("OPERATOR_API_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/agent-runs/:id/logs", HandleAgentRunLogs)
	return r
}

func postLogs(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func entries(lines ...string) map[string]any {
	es := make([]map[string]any, 0, len(lines))
	for i, l := range lines {
		es = append(es, map[string]any{"seq": i + 1, "line": l})
	}
	return map[string]any{"entries": es}
}

func TestIngestLogsValidations(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	if w := postLogs(t, r, "/api/agent-runs/abc/logs", entries("x")); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: expected 400, got %d", w.Code)
	}
	if w := postLogs(t, r, "/api/agent-runs/1/logs", map[string]any{"entries": []any{}}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty entries: expected 400, got %d", w.Code)
	}
	if w := postLogs(t, r, "/api/agent-runs/99/logs", entries("x")); w.Code != http.StatusNotFound {
		t.Fatalf("missing run: expected 404, got %d", w.Code)
	}
	// Per-entry seq validation must apply to each element (dive), not the slice.
	zeroSeq := map[string]any{"entries": []map[string]any{{"seq": 0, "line": "bad"}}}
	if w := postLogs(t, r, "/api/agent-runs/1/logs", zeroSeq); w.Code != http.StatusBadRequest {
		t.Fatalf("seq=0 entry: expected 400, got %d", w.Code)
	}
}

func TestIngestLogsStoresAndPublishes(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}

	sub, cancel := loghub.Subscribe(run.ID)
	defer cancel()

	now := time.Now()
	w := postLogs(t, r, "/api/agent-runs/"+itoa(run.ID)+"/logs", map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "ts": now.Format(time.RFC3339Nano), "line": "first"},
			{"seq": 2, "line": "second"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp["stored"] != float64(2) {
		t.Fatalf("expected stored=2, got %v", resp["stored"])
	}

	// Entries persisted in order.
	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	if logs[0].Line != "first" || logs[1].Line != "second" {
		t.Fatalf("unexpected lines: %+v", logs)
	}
	if logs[0].TS == nil || logs[1].TS != nil {
		t.Fatalf("ts handling wrong: %+v / %+v", logs[0].TS, logs[1].TS)
	}

	// Live event published.
	select {
	case ev := <-sub:
		if ev.Seq != 1 || ev.Line != "first" {
			t.Fatalf("bad event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no published event received")
	}

	// Resending the same seqs is idempotent (OnConflict DoNothing).
	w = postLogs(t, r, "/api/agent-runs/"+itoa(run.ID)+"/logs", map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "dup"},
			{"seq": 3, "line": "third"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("resend: expected 200, got %d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["stored"] != float64(1) {
		t.Fatalf("expected stored=1 on resend, got %v", resp["stored"])
	}
	logs, _ = repo.GetAfterSeq(run.ID, 0, 10)
	if len(logs) != 3 || logs[0].Line != "first" {
		t.Fatalf("dedup broke data: %+v", logs)
	}
}

func TestIngestLogsTruncatesLongLines(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	w := postLogs(t, r, "/api/agent-runs/"+itoa(run.ID)+"/logs",
		entries(strings.Repeat("x", 20000)))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	repo := repositories.NewAgentRunLogRepository(db)
	logs, _ := repo.GetAfterSeq(run.ID, 0, 10)
	if len(logs) != 1 || len(logs[0].Line) > 8300 {
		t.Fatalf("line not truncated: len=%d", len(logs[0].Line))
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
