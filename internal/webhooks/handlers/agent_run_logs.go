package handlers

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/loghub"
	"agentic-automation/internal/logredact"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Limits for the log ingestion endpoint.
const (
	maxLogEntriesPerRequest = 500
	maxLogLineBytes         = 8192
	maxLogRequestBodyBytes  = 4 << 20 // 4 MiB
)

// truncateLineUTF8 cuts a line at maxBytes on a rune boundary and marks it
// truncated. A raw byte slice could split a multibyte rune and produce
// invalid UTF-8 that utf8mb4 columns then reject.
func truncateLineUTF8(line string, maxBytes int) string {
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(line[:cut]) {
		cut--
	}
	return line[:cut] + "…[truncated]"
}

// ingestLocks serializes the check-existing/insert/publish sequence per
// AgentRun within this process. Without it two overlapping requests
// carrying the same seqs (e.g. a resend racing the original) could both
// pass ExistingSeqs before either inserts, and both would publish —
// OnConflict only dedups storage. Cross-replica serialization uses a MySQL
// named lock inside the transaction below; this mutex still covers the
// sqlite path used by tests.
var ingestLocks sync.Map // map[int]*sync.Mutex

func lockForRun(runID int) *sync.Mutex {
	v, _ := ingestLocks.LoadOrStore(runID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func ingestLockName(runID int) string {
	return fmt.Sprintf("agentic:log-ingest:%d", runID)
}

func seqsOf(logs []*models.AgentRunLog) []int64 {
	seqs := make([]int64, 0, len(logs))
	for _, l := range logs {
		seqs = append(seqs, l.Seq)
	}
	return seqs
}

// LogEntryRequest is one log line in the ingestion batch.
type LogEntryRequest struct {
	Seq  int64      `json:"seq" binding:"required,min=1"`
	TS   *time.Time `json:"ts,omitempty"`
	Line string     `json:"line"`
}

// AgentRunLogsRequest is the request body of POST /api/agent-runs/:id/logs.
type AgentRunLogsRequest struct {
	Entries []LogEntryRequest `json:"entries" binding:"required,min=1,dive"`
}

// HandleAgentRunLogs ingests batched log lines from an agent-runner pod.
// Bearer-authenticated like the report endpoint; entries are stored
// idempotently on (agent_run_id, seq) and published to the live log hub.
func HandleAgentRunLogs(c *gin.Context) {
	logger := config.GetLogger()

	runID, err := strconv.Atoi(c.Param("id"))
	if err != nil || runID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_AGENT_RUN_ID",
			"message": "agent run id must be a positive integer",
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLogRequestBodyBytes)
	var req AgentRunLogsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "request body must be JSON with a non-empty entries array",
		})
		return
	}
	if len(req.Entries) > maxLogEntriesPerRequest {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "TOO_MANY_ENTRIES",
			"message": "entries exceeds the per-request maximum",
		})
		return
	}

	db := config.GetDB()
	runRepo := repositories.NewAgentRunRepository(db)
	run, err := runRepo.GetByID(runID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "AGENT_RUN_NOT_FOUND",
				"message": "agent run not found",
			})
			return
		}
		logger.Error("Failed to load agent run for log ingestion",
			config.Int("agent_run_id", runID), config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "failed to load agent run",
		})
		return
	}

	logs := make([]*models.AgentRunLog, 0, len(req.Entries))
	for _, entry := range req.Entries {
		// Redact credentials before anything is persisted or streamed:
		// agent stderr can leak tokens injected into the runner env.
		line := logredact.Line(entry.Line)
		if len(line) > maxLogLineBytes {
			line = truncateLineUTF8(line, maxLogLineBytes)
		}
		logs = append(logs, &models.AgentRunLog{
			AgentRunID: run.ID,
			Seq:        entry.Seq,
			TS:         entry.TS,
			Line:       line,
		})
	}

	// Serialize check + insert + publish-decision per run so publication is
	// atomic with the insert: a concurrent same-seq request waits, then sees
	// the rows the first request stored. On MySQL a named lock inside the
	// transaction serializes across operator replicas (GET_LOCK is
	// session-scoped, so it must run on the transaction's pinned
	// connection); the process-local mutex covers sqlite/test paths.
	mu := lockForRun(run.ID)
	mu.Lock()
	defer mu.Unlock()

	var stored int
	var toPublish []loghub.Event
	lockName := ingestLockName(run.ID)
	err = db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "mysql" {
			var got sql.NullInt64
			if err := tx.Raw("SELECT GET_LOCK(?, ?)", lockName, 10).Scan(&got).Error; err != nil {
				return fmt.Errorf("acquire ingest lock: %w", err)
			} else if !got.Valid || got.Int64 != 1 {
				return fmt.Errorf("acquire ingest lock: not granted")
			}
			defer tx.Exec("SELECT RELEASE_LOCK(?)", lockName)
		}

		logRepo := repositories.NewAgentRunLogRepository(tx)
		// OnConflict DoNothing makes re-posted batches idempotent for
		// storage, but live publication must be limited to rows actually
		// inserted or a retried batch would fan the same lines out to SSE
		// subscribers twice.
		existing, err := logRepo.ExistingSeqs(run.ID, seqsOf(logs))
		if err != nil {
			return err
		}
		stored, err = logRepo.CreateBatch(logs)
		if err != nil {
			return err
		}
		seen := make(map[int64]bool, len(logs))
		for _, l := range logs {
			if existing[l.Seq] || seen[l.Seq] {
				continue
			}
			seen[l.Seq] = true
			toPublish = append(toPublish, loghub.Event{
				AgentRunID: l.AgentRunID,
				Seq:        l.Seq,
				TS:         l.TS,
				Line:       l.Line,
			})
		}
		return nil
	})
	if err != nil {
		logger.Error("Failed to store agent run logs",
			config.Int("agent_run_id", runID), config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "failed to store logs",
		})
		return
	}

	// Publish while still holding the per-run mutex so events reach
	// subscribers in request order; the decision itself was made atomically
	// under the DB lock.
	for _, ev := range toPublish {
		loghub.Publish(ev)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "logs ingested",
		"agent_run_id": runID,
		"received":     len(req.Entries),
		"stored":       stored,
	})
}
