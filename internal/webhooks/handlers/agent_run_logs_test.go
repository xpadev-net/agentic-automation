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
	"unicode/utf8"

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

	// Only the newly stored seq is published on resend — subscribers must
	// never see the skipped duplicate (seq=1 "dup") again.
	deadline := time.After(500 * time.Millisecond)
	var got []string
	for {
		select {
		case ev := <-sub:
			got = append(got, ev.Line)
			if ev.Line == "third" {
				deadline = time.After(50 * time.Millisecond)
			}
		case <-deadline:
			for _, l := range got {
				if l == "dup" {
					t.Fatalf("duplicate seq republished: %v", got)
				}
			}
			return
		}
	}
}

func TestIngestLogsTruncatesOnRuneBoundary(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	// 3000 x 3-byte runes = 9000 bytes: the 8192-byte cut lands mid-rune.
	w := postLogs(t, r, "/api/agent-runs/"+itoa(run.ID)+"/logs",
		entries(strings.Repeat("あ", 3000)))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	repo := repositories.NewAgentRunLogRepository(db)
	logs, _ := repo.GetAfterSeq(run.ID, 0, 10)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	l := logs[0].Line
	if !utf8.ValidString(l) {
		t.Fatal("truncated line is invalid UTF-8")
	}
	if !strings.HasSuffix(l, "…[truncated]") {
		t.Fatalf("truncation marker missing: %q", l[len(l)-30:])
	}
}

func TestIngestLogsRedactsSecrets(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	secret := "ghp_" + strings.Repeat("a", 36)
	w := postLogs(t, r, "/api/agent-runs/"+itoa(run.ID)+"/logs",
		entries("token: "+secret))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	repo := repositories.NewAgentRunLogRepository(db)
	logs, _ := repo.GetAfterSeq(run.ID, 0, 10)
	if len(logs) != 1 || strings.Contains(logs[0].Line, secret) {
		t.Fatalf("secret persisted: %v", logs)
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

// A PEM block spanning two POST requests must stay masked: batch 2 carries
// only body lines (a short final line the base64 pattern misses) plus END,
// and the stored lines from batch 1 must reconstruct the masking state.
func TestIngestLogsMasksPEMAcrossBatches(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "normal line"},
			{"seq": 2, "line": "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----"},
			{"seq": 3, "line": "MIIEpAIBAAKCAQEA7shortbody"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch1: expected 200, got %d", w.Code)
	}
	w = postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 4, "line": "abc123shorttail"},
			{"seq": 5, "line": "-----END RSA PRIVATE KEY-----"},
			{"seq": 6, "line": "after the key"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch2: expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 6 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	want := []string{
		"normal line",
		"[REDACTED PRIVATE KEY BEGIN]",
		"[REDACTED]",
		"[REDACTED]",
		"[REDACTED PRIVATE KEY END]",
		"after the key",
	}
	for i, l := range logs {
		if l.Line != want[i] {
			t.Fatalf("seq %d: want %q got %q", i+1, want[i], l.Line)
		}
	}
}

// A shipper-masked block continues across POST boundaries: the next batch
// carries "[REDACTED]" body lines and the "[REDACTED PRIVATE KEY END]"
// sentinel rather than raw markers. The handler must recognize the
// sentinel to close masking, or every later normal line is hidden.
func TestIngestLogsRecognizesShipperSentinels(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	// Batch 1: raw BEGIN leaks in unmasked — server masks it.
	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----"},
			{"seq": 2, "line": "MIIEpAIBAAKCAQEA7body"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch1: expected 200, got %d", w.Code)
	}
	// Batch 2: shipper-style sentinels closing the block, then normal logs.
	w = postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 3, "line": "[REDACTED]"},
			{"seq": 4, "line": "[REDACTED PRIVATE KEY END]"},
			{"seq": 5, "line": "normal after pem"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch2: expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 5 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	want := []string{
		"[REDACTED PRIVATE KEY BEGIN]",
		"[REDACTED]",
		"[REDACTED]",
		"[REDACTED PRIVATE KEY END]",
		"normal after pem",
	}
	for i, l := range logs {
		if l.Line != want[i] {
			t.Fatalf("seq %d: want %q got %q", i+1, want[i], l.Line)
		}
	}
}

// A resent batch can overlap stored seqs: {dup seq 1, new seq 4} after
// stored {1, BEGIN@2, body@3} must still mask seq 4 — seeding state only
// at the batch's min seq misses the BEGIN sitting between the two.
func TestIngestLogsPEMStateOverlappingBatch(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "normal line"},
			{"seq": 2, "line": "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----"},
			{"seq": 3, "line": "MIIEpAIBAAKCAQEA7body"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch1: expected 200, got %d", w.Code)
	}
	w = postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "normal line"}, // resend duplicate
			{"seq": 4, "line": "AbCdEfShortKeyBody"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch2: expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 4 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	want := []string{
		"normal line",
		"[REDACTED PRIVATE KEY BEGIN]",
		"[REDACTED]",
		"[REDACTED]",
	}
	for i, l := range logs {
		if l.Line != want[i] {
			t.Fatalf("seq %d: want %q got %q", i+1, want[i], l.Line)
		}
	}
}

// Retry attempts occupy disjoint 1e9-seq ranges; an unterminated BEGIN
// stored by attempt 0 must not leak masking into attempt 1's lines.
func TestIngestLogsPEMStateScopedToAttempt(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----"},
			{"seq": 2, "line": "MIIEpAIBAAKCAQEA7body"}, // pod dies here: no END
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch1: expected 200, got %d", w.Code)
	}
	const attempt1 = int64(1_000_000_000)
	w = postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": attempt1 + 1, "line": "attempt 1 first line"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch2: expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, attempt1, 10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	if logs[0].Line != "attempt 1 first line" {
		t.Fatalf("attempt 1 line must not be masked, got %q", logs[0].Line)
	}
}

// A re-posted seq carrying an END sentinel where the stored row was a PEM
// body line must not flip masking state: storage ignores the duplicate,
// so a later fresh entry still inside the block stays masked.
func TestIngestLogsDuplicateEntryCannotAlterPEMState(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----"},
			{"seq": 2, "line": "MIIEpAIBAAKCAQEA7body"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch1: expected 200, got %d", w.Code)
	}
	// Duplicate seq 2 now claims to be the END sentinel; seq 3 is a fresh
	// key fragment that must remain masked because the block never closed.
	w = postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 2, "line": "[REDACTED PRIVATE KEY END]"},
			{"seq": 3, "line": "shortkeyfragment"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch2: expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 3 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	if logs[2].Seq != 3 || logs[2].Line != "[REDACTED]" {
		t.Fatalf("fresh entry inside unclosed PEM must stay masked, got seq=%d line=%q",
			logs[2].Seq, logs[2].Line)
	}
}

// A `-----END CERTIFICATE-----` line interleaved inside an open
// private-key block must not close masking — remaining body stays
// redacted and is never the stored end sentinel.
func TestIngestLogsCertificateEndDoesNotClosePEM(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----"},
			{"seq": 2, "line": "-----END CERTIFICATE-----"},
			{"seq": 3, "line": "shortkeyfrag"},
			{"seq": 4, "line": "-----END RSA PRIVATE KEY-----"},
			{"seq": 5, "line": "done"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 5 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	want := []string{
		"[REDACTED PRIVATE KEY BEGIN]",
		"[REDACTED]",
		"[REDACTED]",
		"[REDACTED PRIVATE KEY END]",
		"done",
	}
	for i, l := range logs {
		if l.Line != want[i] {
			t.Fatalf("seq %d: want %q got %q", i+1, want[i], l.Line)
		}
	}
}

// A stored line ending at a bare credential key primes pending state for
// the next batch: its first line is the value and must be masked.
func TestIngestLogsPendingCredentialAcrossBatches(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "CURSOR_API_KEY"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch1: expected 200, got %d", w.Code)
	}
	w = postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 2, "line": "xyz987uvw654"},
			{"seq": 3, "line": "after"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch2: expected 200, got %d", w.Code)
	}

	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 3 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	if logs[1].Line != "***" {
		t.Fatalf("pending value not masked: %q", logs[1].Line)
	}
	if logs[2].Line != "after" {
		t.Fatalf("state leaked past one line: %q", logs[2].Line)
	}
}

// A gap with NO stored predecessor and no in-memory continuation means
// the real masking state is unknowable (earlier batch not arrived /
// dropped) — fail closed: mask the line and propagate "pending" so the
// next line is masked as a possible continuation value.
func TestUnknownStateGapMasksConservatively(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	// seq2 is still in flight when seq3..4 land first.
	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 3, "line": "PASSWORD="},
			{"seq": 4, "line": "secret-value"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("first batch: %d", w.Code)
	}
	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	if logs[0].Line != "***" || logs[1].Line != "***" {
		t.Fatalf("gap entries must fail closed: %q / %q", logs[0].Line, logs[1].Line)
	}

	// The missing seqs arrive: seq1 plain + seq2 a bare credential key.
	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "running"},
			{"seq": 2, "line": "GITHUB_TOKEN"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("late batch: %d", w.Code)
	}
	var l2 models.AgentRunLog
	if err := db.Where("agent_run_id = ? AND seq = 2", run.ID).First(&l2).Error; err != nil {
		t.Fatal(err)
	}
	if l2.Line != "GITHUB_TOKEN" || l2.RedactState != "p" {
		t.Fatalf("seq2: got %q state %q", l2.Line, l2.RedactState)
	}
}

// In-batch gap with a stored but NON-adjacent predecessor still fails
// closed — the missing seqs may carry masking state.
func TestGapWithNonAdjacentStoredPredMasks(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{{"seq": 1, "line": "ok"}},
	}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}
	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{{"seq": 3, "line": "hello"}},
	}); w.Code != http.StatusOK {
		t.Fatalf("gap batch: %d", w.Code)
	}
	var l3 models.AgentRunLog
	if err := db.Where("agent_run_id = ? AND seq = 3", run.ID).First(&l3).Error; err != nil {
		t.Fatal(err)
	}
	if l3.Line != "***" {
		t.Fatalf("non-adjacent stored pred should mask: %q", l3.Line)
	}
}

// An in-batch gap is itself unknown state (the missing seq may have
// opened a credential/PEM block) — the entry is masked conservatively.
func TestInBatchGapMasksAsUnknown(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	// seq1 bare key, seq2 dropped, seq3 is the value line — same batch:
	// the pending state from seq1 must still mask seq3.
	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 1, "line": "GITHUB_TOKEN"},
			{"seq": 3, "line": "leaked-value"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("batch: %d", w.Code)
	}
	var l3 models.AgentRunLog
	if err := db.Where("agent_run_id = ? AND seq = 3", run.ID).First(&l3).Error; err != nil {
		t.Fatal(err)
	}
	if l3.Line != "***" {
		t.Fatalf("in-memory pending lost across gap: %q", l3.Line)
	}
}

// Once the state is unknown it stays sticky for the rest of the attempt:
// later benign-looking lines are masked too, because the missing seq may
// have opened a quoted value or PEM block that never visibly closes.
func TestUnknownStateIsStickyWithinAttempt(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	// seq1 missing; seqs 2-4 land as one batch.
	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 2, "line": "beta"},
			{"seq": 3, "line": "gamma"},
			{"seq": 4, "line": `delta"`},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("batch: %d", w.Code)
	}
	repo := repositories.NewAgentRunLogRepository(db)
	logs, err := repo.GetAfterSeq(run.ID, 0, 10)
	if err != nil || len(logs) != 3 {
		t.Fatalf("get logs: %v len=%d", err, len(logs))
	}
	for i, l := range logs {
		if l.Line != "***" {
			t.Fatalf("seq %d not masked: %q", i+2, l.Line)
		}
		if !strings.HasPrefix(l.RedactState, "u") {
			t.Fatalf("seq %d state not unknown: %q", i+2, l.RedactState)
		}
	}

	// A subsequent batch resuming from a "u" row keeps masking.
	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{{"seq": 5, "line": "epsilon"}},
	}); w.Code != http.StatusOK {
		t.Fatalf("followup: %d", w.Code)
	}
	var l5 models.AgentRunLog
	if err := db.Where("agent_run_id = ? AND seq = 5", run.ID).First(&l5).Error; err != nil {
		t.Fatal(err)
	}
	if l5.Line != "***" {
		t.Fatalf("resume from u-row should mask: %q", l5.Line)
	}
}

// A new attempt resets the unknown marker — the 1e9 stride boundary
// starts clean again.
func TestUnknownStateResetsAtAttemptBoundary(t *testing.T) {
	db := setupLogTestDB(t)
	config.SetDBForTesting(db)
	r := setupLogTestRouter(t)

	run := &models.AgentRun{IssueID: 1, State: "started"}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	path := "/api/agent-runs/" + itoa(run.ID) + "/logs"

	if w := postLogs(t, r, path, map[string]any{
		"entries": []map[string]any{
			{"seq": 3, "line": "unknown-state line"},
			{"seq": 1_000_000_001, "line": "new attempt first line"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("batch: %d", w.Code)
	}
	var l models.AgentRunLog
	if err := db.Where("agent_run_id = ? AND seq = ?", run.ID, int64(1_000_000_001)).First(&l).Error; err != nil {
		t.Fatal(err)
	}
	if l.Line != "new attempt first line" || l.RedactState != "" {
		t.Fatalf("attempt 1 must start clean: %q state %q", l.Line, l.RedactState)
	}
}
