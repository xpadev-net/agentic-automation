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
	"strings"
	"sync"
	"time"
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
// It returns nil (shipping disabled) when configuration is incomplete, so
// callers can unconditionally defer Close().
func Attach(apiURL, apiToken string, agentRunID int) (*Shipper, error) {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" || apiToken == "" || agentRunID <= 0 {
		return nil, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
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
func (s *Shipper) readLoop() {
	defer s.wg.Done()
	scanner := bufio.NewScanner(s.pipeR)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		s.seq++
		fmt.Fprintln(s.orig, line)
		select {
		case s.lines <- logEntry{Seq: s.seq, TS: time.Now().UTC(), Line: line}:
		default:
			s.dropped++
		}
	}
	close(s.lines)
}

// sendLoop batches queued lines and POSTs them to the ingestion endpoint.
func (s *Shipper) sendLoop() {
	defer s.wg.Done()
	batch := make([]logEntry, 0, batchMax)
	timer := time.NewTimer(flushInterval)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.send(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-s.done:
			// Drain remaining queued lines quickly, then flush once.
			for e := range s.lines {
				batch = append(batch, e)
				if len(batch) >= batchMax {
					flush()
				}
			}
			flush()
			return
		case e, ok := <-s.lines:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= batchMax {
				flush()
			}
		case <-timer.C:
			flush()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(flushInterval)
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
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			resp.Body.Close()
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("log ingestion status %d", resp.StatusCode)
			resp.Body.Close()
			// 4xx means the request is invalid; retrying won't help.
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				break
			}
		}
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	fmt.Fprintf(s.orig, "[logship] dropped %d log lines: %v\n", len(batch), lastErr)
	s.dropped += int64(len(batch))
}
