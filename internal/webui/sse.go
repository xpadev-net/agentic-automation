package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/loghub"

	"github.com/gin-gonic/gin"
)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func runIsTerminal(state string) bool {
	return state == "succeeded" || state == "failed"
}

type sseLogPayload struct {
	Seq  int64      `json:"seq"`
	TS   *time.Time `json:"ts,omitempty"`
	Line string     `json:"line"`
}

func sseWriteLog(c *gin.Context, p sseLogPayload) {
	b, _ := json.Marshal(p)
	fmt.Fprintf(c.Writer, "id: %d\ndata: %s\n\n", p.Seq, b)
}

// HandleStreamRunLogs streams run logs as Server-Sent Events: it first
// replays persisted entries (or the pod-log snapshot when none exist), then
// follows the live hub feed until the run is terminal and quiet.
func (h *Handler) HandleStreamRunLogs(c *gin.Context) {
	run := h.requireRunAccess(c)
	if run == nil {
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	// Resume point: Last-Event-ID header or after_seq query param.
	afterSeq := int64(atoi(c.GetHeader("Last-Event-ID")))
	if afterSeq == 0 {
		afterSeq = int64(atoi(c.Query("after_seq")))
	}

	// Replay persisted entries in pages.
	latestSeq := afterSeq
	total := 0
	for {
		entries, err := h.logs.GetAfterSeq(run.ID, latestSeq, logsMaxLimit)
		if err != nil {
			h.logger.Error("SSE replay failed", config.Int("run_id", run.ID), config.Error(err))
			return
		}
		if len(entries) == 0 {
			break
		}
		for _, e := range entries {
			sseWriteLog(c, sseLogPayload{Seq: e.Seq, TS: e.TS, Line: e.Line})
			latestSeq = e.Seq
			total++
		}
		if len(entries) < logsMaxLimit {
			break
		}
	}
	// Pod-log fallback when nothing was ever pushed.
	if total == 0 && afterSeq == 0 {
		for _, e := range h.podLogSnapshot(c.Request.Context(), run) {
			seq, _ := e["seq"].(int)
			line, _ := e["line"].(string)
			sseWriteLog(c, sseLogPayload{Seq: int64(seq), Line: line})
			latestSeq = int64(seq)
			total++
		}
	}
	flusher.Flush()

	finish := func() {
		b, _ := json.Marshal(gin.H{"state": run.State})
		fmt.Fprintf(c.Writer, "event: end\ndata: %s\n\n", b)
		flusher.Flush()
	}

	// Already finished and fully replayed: close immediately.
	if runIsTerminal(run.State) {
		finish()
		return
	}

	events, cancel := loghub.Subscribe(run.ID)
	defer cancel()

	lastEvent := time.Now()
	keepalive := time.NewTicker(sseKeepalive)
	defer keepalive.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				finish()
				return
			}
			if ev.Seq <= latestSeq {
				continue
			}
			sseWriteLog(c, sseLogPayload{Seq: ev.Seq, TS: ev.TS, Line: ev.Line})
			latestSeq = ev.Seq
			lastEvent = time.Now()
			flusher.Flush()
		case <-keepalive.C:
			// Refresh terminal state; end stream once quiet for a while.
			if fresh, err := h.loadRun(run.ID); err == nil && fresh != nil {
				run.State = fresh.State
			}
			if runIsTerminal(run.State) && time.Since(lastEvent) > sseEndQuiet {
				finish()
				return
			}
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}
