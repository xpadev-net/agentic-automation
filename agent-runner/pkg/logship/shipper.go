// Package logship tees the runner's stderr stream to the Operator's log
// ingestion endpoint so the WebUI can show live logs. It is best-effort:
// shipping failures never fail the agent run; dropped lines appear as seq
// gaps for the viewer.
package logship

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agent-runner/pkg/redact"
)

const (
	// channelBuffer bounds queued lines awaiting shipment.
	channelBuffer = 4096
	// batchMax is the max lines per POST; server accepts up to 500.
	batchMax = 200
	// flushInterval is the max wait before a partial batch is sent.
	flushInterval = 2 * time.Second
	// httpTimeout bounds each ingestion request.
	httpTimeout = 10 * time.Second
	// sendAttempts bounds retries for a single batch before dropping it.
	sendAttempts = 3
	// drainBudget bounds how long Close() spends flushing queued lines; past
	// this the remaining queue is dropped so shutdown cannot hang.
	drainBudget = 30 * time.Second
	// truncMark marks lines cut for exceeding the per-line limit.
	truncMark = "…[truncated]"
	// maxShipLineBytes leaves room for the truncation marker so shipped
	// lines stay under the server's 8192-byte per-line limit.
	maxShipLineBytes = 8192 - len(truncMark)
	// maxRetryAfterSecs bounds a Retry-After hint from the server.
	maxRetryAfterSecs = 30
	// seqAttemptStride partitions the sequence space per retry attempt so a
	// retried Job (same AgentRun, bumped RETRY_COUNT) does not collide with
	// earlier persisted seq values.
	seqAttemptStride = int64(1_000_000_000)
)

type logEntry struct {
	Seq  int64     `json:"seq"`
	TS   time.Time `json:"ts"`
	Line string    `json:"line"`
}

type logsRequest struct {
	Entries []logEntry `json:"entries"`
}

// Shipper captures os.Stderr through a pipe, mirrors every line to the real
// stderr, and ships lines to the Operator in batches.
type Shipper struct {
	endpoint string
	token    string
	http     *http.Client

	orig  *os.File // real stderr
	pipeR *os.File
	pipeW *os.File

	lines chan logEntry
	done  chan struct{}

	wg      sync.WaitGroup
	seq     int64
	dropped int64

	closeOnce sync.Once
}

// Attach replaces os.Stderr with a pipe and starts the shipping goroutines.
// retryCount partitions the seq space per attempt (attempt n starts at
// n*1e9+1) so retried Jobs do not collide with logs persisted by earlier
// attempts for the same AgentRun. It returns nil (shipping disabled) when
// configuration is incomplete, so callers can unconditionally defer Close().
func Attach(apiURL, apiToken string, agentRunID, retryCount int) (*Shipper, error) {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" || apiToken == "" || agentRunID <= 0 {
		return nil, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	if retryCount < 0 {
		retryCount = 0
	}
	s := &Shipper{
		endpoint: fmt.Sprintf("%s/api/agent-runs/%d/logs", apiURL, agentRunID),
		token:    apiToken,
		http:     &http.Client{Timeout: httpTimeout},
		orig:     os.Stderr,
		pipeR:    r,
		pipeW:    w,
		lines:    make(chan logEntry, channelBuffer),
		done:     make(chan struct{}),
		seq:      int64(retryCount) * seqAttemptStride,
	}
	os.Stderr = w
	s.wg.Add(2)
	go s.readLoop()
	go s.sendLoop()
	return s, nil
}

// Close restores stderr, drains the pipe, flushes the pending batch, and
// waits for the goroutines to finish. Safe to call on a nil Shipper.
func (s *Shipper) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.done)
		os.Stderr = s.orig
		_ = s.pipeW.Close()
		s.wg.Wait()
		_ = s.pipeR.Close()
		if s.dropped > 0 {
			fmt.Fprintf(s.orig, "[logship] %d log lines were dropped due to backpressure/failures\n", s.dropped)
		}
	})
}

// readLoop drains the pipe: tee to the real stderr and enqueue for shipping.
// bufio.Reader is used instead of Scanner so arbitrarily long lines never
// terminate the loop — an oversized line is mirrored raw but shipped
// truncated to the server's per-line limit.
func (s *Shipper) readLoop() {
	defer s.wg.Done()
	reader := bufio.NewReaderSize(s.pipeR, 64*1024)
	for {
		chunk, err := reader.ReadString('\n')
		if chunk != "" {
			line := strings.TrimSuffix(chunk, "\n")
			s.seq++
			fmt.Fprintln(s.orig, line)
			// Redact credentials before queueing for shipment: agent stderr
			// can echo secrets from the runner env and these lines are
			// persisted + streamed to the WebUI.
			line = truncateShipLine(redact.String(line))
			select {
			case s.lines <- logEntry{Seq: s.seq, TS: time.Now().UTC(), Line: line}:
			default:
				s.dropped++
			}
		}
		if err != nil {
			break
		}
	}
	close(s.lines)
}

// truncateShipLine cuts a line at maxShipLineBytes on a rune boundary and
// appends the truncation marker. Keeps every POST under the server's
// per-line limit so a single huge line cannot get the whole batch a 400.
func truncateShipLine(line string) string {
	if len(line) <= maxShipLineBytes {
		return line
	}
	cut := maxShipLineBytes
	for cut > 0 && !utf8.ValidString(line[:cut]) {
		cut--
	}
	return line[:cut] + truncMark
}

// sendLoop batches queued lines and POSTs them to the ingestion endpoint.
func (s *Shipper) sendLoop() {
	defer s.wg.Done()
	batch := make([]logEntry, 0, batchMax)
	timer := time.NewTimer(flushInterval)
	if !timer.Stop() {
		<-timer.C
	}
	timerActive := false
	defer timer.Stop()

	stopTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerActive = false
	}
	flush := func() {
		if len(batch) > 0 {
			s.send(batch)
			batch = batch[:0]
		}
		stopTimer()
	}

	for {
		select {
		case <-s.done:
			// Drain remaining queued lines, but bound the total time so a
			// stalled network cannot hang runner shutdown.
			deadline := time.Now().Add(drainBudget)
			for e := range s.lines {
				if time.Now().After(deadline) {
					s.dropped++
					continue
				}
				batch = append(batch, e)
				if len(batch) >= batchMax {
					flush()
				}
			}
			if time.Now().Before(deadline) {
				flush()
			} else {
				s.dropped += int64(len(batch))
			}
			return
		case e, ok := <-s.lines:
			if !ok {
				flush()
				return
			}
			// The flush interval counts from the first line of a batch, not
			// the latest — a steady trickle must not postpone the flush.
			if !timerActive {
				timer.Reset(flushInterval)
				timerActive = true
			}
			batch = append(batch, e)
			if len(batch) >= batchMax {
				flush()
			}
		case <-timer.C:
			timerActive = false
			flush()
		}
	}
}

// send POSTs one batch with small retry; failures only drop the batch.
func (s *Shipper) send(batch []logEntry) {
	body, err := json.Marshal(logsRequest{Entries: batch})
	if err != nil {
		s.dropped += int64(len(batch))
		return
	}
	var lastErr error
	for attempt := 1; attempt <= sendAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(body))
		if err != nil {
			break
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.http.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}
		code := resp.StatusCode
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()
		if code >= 200 && code < 300 {
			return
		}
		lastErr = fmt.Errorf("log ingestion status %d", code)
		// 4xx other than 429 means the request is invalid; retrying won't
		// help. 429 (and 5xx) are transient — honor Retry-After when sane.
		if code >= 400 && code < 500 && code != http.StatusTooManyRequests {
			break
		}
		delay := time.Duration(attempt) * 500 * time.Millisecond
		if code == http.StatusTooManyRequests {
			if secs, e := strconv.Atoi(retryAfter); e == nil && secs > 0 {
				if secs > maxRetryAfterSecs {
					secs = maxRetryAfterSecs
				}
				delay = time.Duration(secs) * time.Second
			}
		}
		time.Sleep(delay)
	}
	fmt.Fprintf(s.orig, "[logship] dropped %d log lines: %v\n", len(batch), lastErr)
	s.dropped += int64(len(batch))
}
