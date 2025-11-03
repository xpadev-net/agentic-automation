package webhooks_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/webhooks/handlers"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	// Pre-create minimal issues table to satisfy GORM's relation discovery on AgentRun without MySQL enums
	if err := db.Exec("CREATE TABLE IF NOT EXISTS issues (id integer PRIMARY KEY AUTOINCREMENT)").Error; err != nil {
		t.Fatalf("failed to create stub issues table: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentRun{}, &models.AuditLog{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	config.SetDBForTesting(db)
	return db
}

func TestHandleAgentReport_Failed_StoresSummaryAndAudit(t *testing.T) {
	db := setupTestDB(t)
	// seed agent run (failed path does not require Issue row)
	run := &models.AgentRun{
		IdempotencyKey: "idem-1",
		IssueID:        0,
		State:          "started",
		AgentType:      "claude-code",
		StartedAt:      ptrTime(time.Now()),
	}
	if err := db.Create(run).Error; err != nil {
		t.Fatalf("seed agent run: %v", err)
	}

	// build request
	body := map[string]any{
		"status":        "failed",
		"agent_type":    "claude-code",
		"error_message": "primary error",
		"logs":          "INFO start\nERROR something bad\nstack trace...\n",
	}
	b, _ := json.Marshal(body)

	// gin test context
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+strconv.Itoa(run.ID)+"/report", strings.NewReader(string(b)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: strconv.Itoa(run.ID)}}

	// invoke handler
	handlers.HandleAgentReport(c)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", w.Code, w.Body.String())
	}

	// verify AgentRun updated
	var updated models.AgentRun
	if err := db.First(&updated, run.ID).Error; err != nil {
		t.Fatalf("load updated run: %v", err)
	}
	if updated.State != "failed" {
		t.Fatalf("expected state failed, got %s", updated.State)
	}
	if updated.ErrorMessage == nil || !strings.Contains(*updated.ErrorMessage, "primary error") {
		t.Fatalf("error message not set or missing primary: %v", updated.ErrorMessage)
	}

	// verify audit log
	var audits []models.AuditLog
	if err := db.Find(&audits).Error; err != nil {
		t.Fatalf("query audits: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(audits))
	}
	if audits[0].EventType != "agent-run.failed" {
		t.Fatalf("unexpected event type: %s", audits[0].EventType)
	}
}

func ptrTime(ti time.Time) *time.Time { return &ti }
