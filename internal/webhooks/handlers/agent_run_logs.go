package handlers

import (
	"agentic-automation/internal/config"
	"agentic-automation/internal/loghub"
	"agentic-automation/internal/logredact"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
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
// sqlite path used by tests. Entries are reference-counted so the map does
// not grow with every historical run id.
var (
	ingestLocksMu sync.Mutex
	ingestLocks   = map[int]*ingestLock{}
)

type ingestLock struct {
	mu   sync.Mutex
	refs int
}

// acquireIngestLock takes the per-run mutex and returns the release
// function; the map entry is dropped once the last holder releases.
func acquireIngestLock(runID int) func() {
	ingestLocksMu.Lock()
	l := ingestLocks[runID]
	if l == nil {
		l = &ingestLock{}
		ingestLocks[runID] = l
	}
	l.refs++
	ingestLocksMu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		ingestLocksMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(ingestLocks, runID)
		}
		ingestLocksMu.Unlock()
	}
}

func ingestLockName(runID int) string {
	return fmt.Sprintf("agentic:log-ingest:%d", runID)
}

// Stored sentinel lines marking the edges of a masked private-key block,
// distinct from the body mask. Masking state survives across ingestion
// batches because these lines are persisted; the shipper emits the same
// sentinels for blocks it masked itself.
const (
	pemBeginSentinel = "[REDACTED PRIVATE KEY BEGIN]"
	pemEndSentinel   = "[REDACTED PRIVATE KEY END]"
	redactedLine     = "[REDACTED]"
)

// seqAttemptStride mirrors the shipper's per-attempt seq partitioning
// (attempt n emits seqs starting at n*stride+1), so stored boundaries
// from an earlier attempt must not seed this attempt's masking state.
const seqAttemptStride = int64(1_000_000_000)

// encodeLogState packs the PEM flag and the credential Stream state into
// the redact_state column; decodeLogState reverses it. Rows without
// state (pre-column or third-party inserts) decode to idle.
func encodeLogState(inPEM bool, st string) string {
	if inPEM {
		if st == "" {
			return "pem"
		}
		return "pem;" + st
	}
	return st
}

func decodeLogState(enc string) (bool, string) {
	if enc == "pem" {
		return true, ""
	}
	if strings.HasPrefix(enc, "pem;") {
		return true, enc[len("pem;"):]
	}
	return false, enc
}

// lockAndTxDB acquires the MySQL named lock on a dedicated connection and
// returns a *gorm.DB bound to that same physical connection, so callers
// can run a gorm Transaction while the lock is held — and released only
// after commit. release() must be called once the transaction returns; it
// runs on context.Background() because the request context may be done.
func lockAndTxDB(ctx context.Context, db *gorm.DB, lockName string) (*gorm.DB, func(), error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	lockConn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	var got sql.NullInt64
	if err := lockConn.QueryRowContext(ctx,
		"SELECT GET_LOCK(?, ?)", lockName, 10).Scan(&got); err != nil || !got.Valid || got.Int64 != 1 {
		_ = lockConn.Close()
		if err == nil {
			err = fmt.Errorf("not granted")
		}
		return nil, nil, err
	}
	release := func() {
		_, _ = lockConn.ExecContext(context.Background(),
			"SELECT RELEASE_LOCK(?)", lockName)
		_ = lockConn.Close()
	}
	txDB, err := gorm.Open(mysql.New(mysql.Config{Conn: lockConn}), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		release()
		return nil, nil, err
	}
	return txDB, release, nil
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

	// Serialize check + insert + publish-decision per run so publication is
	// atomic with the insert: a concurrent same-seq request waits, then sees
	// the rows the first request stored. On MySQL a named lock serializes
	// across operator replicas; it is taken on a dedicated connection held
	// until AFTER the transaction commits (releasing inside the callback
	// would reopen the cross-replica race just before rows become visible).
	// The process-local mutex covers the sqlite path used by tests.
	unlock := acquireIngestLock(run.ID)
	defer unlock()

	lockName := ingestLockName(run.ID)
	// txDB carries the transaction below. On MySQL the named lock must be
	// held through commit, so the lock and the transaction run on ONE
	// dedicated connection (wrapped as a gorm.DB bound to that conn).
	// Holding one pooled conn while Transaction() waits for a second would
	// deadlock once every pool slot is taken by a lock-holder.
	txDB := db
	if db.Dialector.Name() == "mysql" {
		var lerr error
		var release func()
		txDB, release, lerr = lockAndTxDB(c.Request.Context(), db, lockName)
		if lerr != nil {
			logger.Error("Failed to acquire ingest lock",
				config.Int("agent_run_id", runID), config.Error(lerr))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "failed to store logs",
			})
			return
		}
		// release() frees the lock AFTER txDB.Transaction has committed
		// (defer order) and on Background: the request context may be done.
		defer release()
	}

	var stored int
	var toPublish []loghub.Event
	err = txDB.Transaction(func(tx *gorm.DB) error {
		logRepo := repositories.NewAgentRunLogRepository(tx)
		// A PEM value split across entries (GITHUB_PRIVATE_KEY=-----BEGIN...
		// on the first line, base64 body after) defeats per-line patterns,
		// so mask every line between BEGIN and END like the shipper does.
		// Masking state must survive the request — a block spanning two
		// POSTs would otherwise resume unmasked — so it replays the stored
		// BEGIN/END sentinels preceding each entry. Replaying per entry
		// (not once at min(seq)) handles overlapping batches: a duplicate
		// low seq followed by new higher seqs would otherwise miss a
		// stored BEGIN between them. Doing it inside the lock/transaction
		// keeps a racing earlier batch's BEGIN visible here.
		entries := make([]LogEntryRequest, len(req.Entries))
		copy(entries, req.Entries)
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Seq < entries[j].Seq })
		// Deduplicate BEFORE the PEM state replay: a re-posted seq carrying
		// different text (e.g. an END sentinel where the stored row was a
		// body line) must not flip masking state for later entries —
		// storage ignores it, so masking must too. First occurrence wins,
		// matching the row OnConflict keeps.
		reqSeqs := make([]int64, len(entries))
		for i, e := range entries {
			reqSeqs[i] = e.Seq
		}
		existing, err := logRepo.ExistingSeqs(run.ID, reqSeqs)
		if err != nil {
			return err
		}
		seenReq := make(map[int64]bool, len(entries))
		fresh := entries[:0]
		for _, e := range entries {
			if existing[e.Seq] || seenReq[e.Seq] {
				continue
			}
			seenReq[e.Seq] = true
			fresh = append(fresh, e)
		}
		if len(fresh) == 0 {
			// Whole batch already stored — idempotent no-op.
			return nil
		}
		// Masking state is persisted per row: every stored line carries
		// the redaction state it left behind (inPEM + credential Stream),
		// so each fresh entry seeds from the latest stored row of ITS
		// attempt — surviving batch boundaries, unclosed quoted values,
		// and overlapping resends alike (a stored bare `GITHUB_TOKEN`
		// between fresh entries masks the value after it).
		inPEM := false
		stream := logredact.Stream{}
		seed := func(seq int64) error {
			base := (seq / seqAttemptStride) * seqAttemptStride
			var prev models.AgentRunLog
			err := tx.Select("redact_state").
				Where("agent_run_id = ? AND seq >= ? AND seq < ?", run.ID, base, seq).
				Order("seq DESC").Limit(1).Take(&prev).Error
			if err == nil {
				var st string
				inPEM, st = decodeLogState(prev.RedactState)
				stream = logredact.Stream{}
				stream.SetState(st)
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// No stored predecessor in this attempt — idle state (this
			// also resets state across an attempt boundary).
			inPEM = false
			stream = logredact.Stream{}
			return nil
		}
		logs := make([]*models.AgentRunLog, 0, len(fresh))
		prevSeq := int64(0)
		for _, entry := range fresh {
			if entry.Seq != prevSeq+1 || prevSeq == 0 {
				// First entry or a gap (a stored row or dropped seq sits
				// between): resume the state the stored predecessor left.
				if err := seed(entry.Seq); err != nil {
					return err
				}
			}
			// Redact credentials before anything is persisted or streamed:
			// agent stderr can leak tokens injected into the runner env.
			// PEM-body and sentinel lines bypass the credential stream —
			// they are already fully masked and must not poison its state.
			var line string
			switch {
			case entry.Line == pemBeginSentinel:
				// Shipper already masked this block — just track state.
				inPEM = true
				line = entry.Line
			case entry.Line == pemEndSentinel:
				inPEM = false
				line = entry.Line
			default:
				if strings.Contains(entry.Line, "-----BEGIN ") && strings.Contains(entry.Line, "PRIVATE KEY") {
					inPEM = true
					line = pemBeginSentinel
				} else if inPEM {
					line = redactedLine
				} else {
					line = stream.Line(entry.Line)
				}
				// Only a PRIVATE KEY end marker closes the block — an
				// interleaved `-----END CERTIFICATE-----` must not leak
				// the remaining key body (same fix as the shipper).
				if strings.Contains(entry.Line, "-----END ") && strings.Contains(entry.Line, "PRIVATE KEY") {
					if inPEM {
						line = pemEndSentinel
					}
					inPEM = false
				}
			}
			if len(line) > maxLogLineBytes {
				line = truncateLineUTF8(line, maxLogLineBytes)
			}
			logs = append(logs, &models.AgentRunLog{
				AgentRunID:  run.ID,
				Seq:         entry.Seq,
				TS:          entry.TS,
				Line:        line,
				RedactState: encodeLogState(inPEM, stream.State()),
			})
			prevSeq = entry.Seq
		}
		// OnConflict DoNothing makes re-posted batches idempotent for
		// storage, but live publication must be limited to rows actually
		// inserted or a retried batch would fan the same lines out to SSE
		// subscribers twice. `existing` was computed before dedup above and
		// still guards the race window with a concurrent batch.
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
