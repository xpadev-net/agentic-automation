// Package logship tees the runner's stderr stream to the Operator's log
// ingestion endpoint so the WebUI can show live logs. It is best-effort:
// shipping failures never fail the agent run; dropped lines appear as seq
// gaps for the viewer.
package logship

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// maxRequestBodyJSON bounds each encoded POST body. JSON escaping can
	// expand lines several-fold (`<` → `<`), so sizing by raw line
	// bytes or line count alone could exceed the server's 4 MiB limit.
	maxRequestBodyJSON = 3 << 20
)

type logEntry struct {
	Seq  int64     `json:"seq"`
	TS   time.Time `json:"ts"`
	Line string    `json:"line"`
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

	wg  sync.WaitGroup
	seq int64
	// dropped is touched by both readLoop (queue overflow) and sendLoop
	// (send failures): it must be atomic or concurrent increments race.
	dropped atomic.Int64

	closeOnce sync.Once

	// shutdownCtx cancels any send still in flight when Close begins, so an
	// active flush cannot delay shutdown past its own request timeout.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
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
	s.shutdownCtx, s.shutdownCancel = context.WithCancel(context.Background())
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
		s.shutdownCancel() // abort any in-flight normal-path send
		close(s.done)
		os.Stderr = s.orig
		_ = s.pipeW.Close()
		s.wg.Wait()
		_ = s.pipeR.Close()
		if n := s.dropped.Load(); n > 0 {
			fmt.Fprintf(s.orig, "[logship] %d log lines were dropped due to backpressure/failures\n", n)
		}
	})
}

// maxLineBufBytes caps how much of a single newline-free stderr record is
// accumulated for shipping. The pipe keeps being drained and mirrored raw
// beyond the cap, so a writer dumping a huge record neither blocks nor can
// OOM the runner even though only the shippable prefix is retained.
const maxLineBufBytes = 64 * 1024

// readLoop drains the pipe: tee to the real stderr and enqueue for shipping.
// It reads bounded fragments and splits lines itself so a record without a
// newline never accumulates unbounded in memory — only the first
// maxLineBufBytes are kept and the line ships truncated.
func (s *Shipper) readLoop() {
	defer s.wg.Done()
	reader := bufio.NewReaderSize(s.pipeR, 64*1024)
	frag := make([]byte, 32*1024)
	var lineBuf []byte
	overflow := false

	// append keeps at most maxLineBufBytes of the current line.
	appendSeg := func(seg []byte) {
		rem := maxLineBufBytes - len(lineBuf)
		if rem <= 0 {
			overflow = true
			return
		}
		if len(seg) > rem {
			seg = seg[:rem]
			overflow = true
		}
		lineBuf = append(lineBuf, seg...)
	}
	// A multiline PEM value (e.g. GITHUB_PRIVATE_KEY) spills base64 body
	// lines that no per-line pattern can identify reliably, so mask
	// everything between the BEGIN/END markers.
	inPEM := false
	emit := func() {
		s.seq++
		raw := string(lineBuf)
		// Redact credentials before queueing for shipment: agent stderr
		// can echo secrets from the runner env and these lines are
		// persisted + streamed to the WebUI.
		line := truncateShipLine(redact.String(raw))
		if strings.HasPrefix(raw, "-----BEGIN ") && strings.Contains(raw, "PRIVATE KEY") {
			inPEM = true
		}
		if inPEM {
			line = "[REDACTED]"
		}
		if strings.HasPrefix(raw, "-----END ") {
			inPEM = false
		}
		select {
		case s.lines <- logEntry{Seq: s.seq, TS: time.Now().UTC(), Line: line}:
		default:
			s.dropped.Add(1)
		}
		lineBuf = lineBuf[:0]
		overflow = false
	}

	for {
		n, err := reader.Read(frag)
		if n > 0 {
			_, _ = s.orig.Write(frag[:n])
			start := 0
			for {
				idx := bytes.IndexByte(frag[start:n], '\n')
				if idx < 0 {
					break
				}
				end := start + idx
				appendSeg(frag[start:end])
				emit() // one entry per newline, including empty lines
				start = end + 1
			}
			appendSeg(frag[start:n])
		}
		if err != nil {
			if len(lineBuf) > 0 || overflow {
				emit()
			}
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
	flush := func(ctx context.Context) {
		if len(batch) > 0 {
			// send returns any remainder it could not deliver because ctx
			// was cancelled (Close() aborting an in-flight send): keep it
			// so the drain path below retries it under a fresh budgeted
			// context instead of losing up to batchMax lines at shutdown.
			batch = s.send(batch, ctx)
		}
		stopTimer()
	}

	for {
		select {
		case <-s.done:
			// Drain remaining queued lines, but bound the total time so a
			// stalled network cannot hang runner shutdown.
			deadline := time.Now().Add(drainBudget)
			drainCtx, drainCancel := context.WithDeadline(context.Background(), deadline)
			defer drainCancel()
			for e := range s.lines {
				if time.Now().After(deadline) {
					s.dropped.Add(1)
					continue
				}
				batch = append(batch, e)
				if len(batch) >= batchMax {
					batch = s.send(batch, drainCtx)
				}
			}
			if time.Now().Before(deadline) && len(batch) > 0 {
				batch = s.send(batch, drainCtx)
			}
			// Whatever remains undelivered here (real failure or an
			// exhausted drain budget) was never counted by send().
			s.dropped.Add(int64(len(batch)))
			return
		case e, ok := <-s.lines:
			if !ok {
				// readLoop closed the channel. If Close already cancelled
				// shutdownCtx, flush the final batch under a fresh
				// budgeted context instead of the dead one.
				ctx := s.shutdownCtx
				if ctx.Err() != nil {
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(context.Background(),
						time.Now().Add(drainBudget))
					defer cancel()
				}
				flush(ctx)
				// Remainder kept by a cancelled drain send is truly dropped.
				s.dropped.Add(int64(len(batch)))
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
				flush(s.shutdownCtx)
			}
		case <-timer.C:
			timerActive = false
			flush(s.shutdownCtx)
		}
	}
}

// encodeBatch marshals a prefix of entries into one request body, stopping
// before the encoded JSON would exceed maxRequestBodyJSON. It returns the
// body and how many entries were consumed.
func encodeBatch(entries []logEntry) ([]byte, int) {
	buf := bytes.NewBuffer(make([]byte, 0, 4096))
	buf.WriteString(`{"entries":[`)
	n := 0
	for _, e := range entries {
		eb, err := json.Marshal(e)
		if err != nil {
			break
		}
		need := len(eb)
		if n > 0 {
			need++ // ','
		}
		if buf.Len()+need+2 > maxRequestBodyJSON {
			break
		}
		if n > 0 {
			buf.WriteByte(',')
		}
		buf.Write(eb)
		n++
	}
	buf.WriteString("]}")
	return buf.Bytes(), n
}

// send POSTs a batch, splitting it into encoded-size-bounded chunks. ctx
// bounds the whole call: attempts and sleeps past it are skipped and an
// in-flight request aborts, so Close() cannot block on a stalled endpoint.
// On ctx cancellation it returns the undelivered remainder for the caller
// to retry under a drain context; on a real send failure it counts the
// undelivered entries as dropped and returns nil.
func (s *Shipper) send(batch []logEntry, ctx context.Context) []logEntry {
	for len(batch) > 0 {
		body, n := encodeBatch(batch)
		if n == 0 {
			// A single 8 KiB entry can never reach the cap; treat as corrupt.
			s.dropped.Add(int64(len(batch)))
			return nil
		}
		if !s.postChunk(body, n, ctx) {
			if ctx.Err() != nil {
				return batch
			}
			s.dropped.Add(int64(len(batch)))
			return nil
		}
		batch = batch[n:]
	}
	return nil
}

// postChunk sends one encoded request body with small retry. Failures drop
// the chunk (and, per send(), the rest of the batch).
func (s *Shipper) postChunk(body []byte, n int, ctx context.Context) bool {
	var lastErr error
	for attempt := 1; attempt <= sendAttempts; attempt++ {
		if ctx.Err() != nil {
			break
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
		if err != nil {
			break
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.http.Do(req)
		if err != nil {
			lastErr = err
		} else {
			code := resp.StatusCode
			retryAfter := resp.Header.Get("Retry-After")
			// Drain before close so the keep-alive connection is reusable.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if code >= 200 && code < 300 {
				return true
			}
			lastErr = fmt.Errorf("log ingestion status %d", code)
			// 4xx other than 429 means the request is invalid; retrying
			// won't help. 429 (and 5xx) are transient — honor Retry-After.
			if code >= 400 && code < 500 && code != http.StatusTooManyRequests {
				break
			}
			// Sleep only when another attempt follows — idling after the
			// final failure just stalls the single send goroutine while
			// the queue keeps filling.
			if attempt < sendAttempts {
				delay := time.Duration(attempt) * 500 * time.Millisecond
				if code == http.StatusTooManyRequests {
					if secs, e := strconv.Atoi(retryAfter); e == nil && secs > 0 {
						if secs > maxRetryAfterSecs {
							secs = maxRetryAfterSecs
						}
						delay = time.Duration(secs) * time.Second
					}
				}
				select {
				case <-time.After(delay):
				case <-ctx.Done():
				}
			}
			continue
		}
		if attempt < sendAttempts {
			select {
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			case <-ctx.Done():
			}
		}
	}
	fmt.Fprintf(s.orig, "[logship] dropped %d log lines: %v\n", n, lastErr)
	return false
}
