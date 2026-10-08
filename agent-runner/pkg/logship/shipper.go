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
	"regexp"
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
	// A Secret-sourced token may carry a trailing newline; net/http would
	// reject the Authorization header and drop every batch.
	apiToken = strings.TrimSpace(apiToken)
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

// pemBoundaryRE matches an actual private-key marker; markers inside a
// line apply in textual order so the LAST one sets block state
// (`END ... BEGIN` leaves the block open). `-----END CERTIFICATE-----`
// does not match, so an interleaved certificate end can't leak the body.
var pemBoundaryRE = regexp.MustCompile(`-----(BEGIN|END) [A-Z0-9 ]*PRIVATE KEY-----`)

// pemOrderedState applies every PEM marker in s, in order, to inPEM.
func pemOrderedState(s string, inPEM bool) bool {
	for _, m := range pemBoundaryRE.FindAllStringSubmatch(s, -1) {
		inPEM = m[1] == "BEGIN"
	}
	return inPEM
}

// pemMarkerCount counts private-key markers in s.
func pemMarkerCount(s string) int {
	return len(pemBoundaryRE.FindAllStringIndex(s, -1))
}

// Stored sentinel values for a masked private-key block. The ingestion
// handler uses the same values so it can reconstruct masking state from
// previously stored lines (a PEM block may span POST requests).
const (
	pemBeginSentinel = "[REDACTED PRIVATE KEY BEGIN]"
	pemEndSentinel   = "[REDACTED PRIVATE KEY END]"
	redactedLine     = "[REDACTED]"
)

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

	// A multiline PEM value (e.g. GITHUB_PRIVATE_KEY) spills base64 body
	// lines that no per-line pattern can identify reliably, so mask
	// everything between the BEGIN/END markers.
	inPEM := false
	// Cross-line credential state: `KEY\nvalue` and `KEY="...` fragments
	// span line boundaries, so masking is a Stream, not a per-line call.
	cred := redact.Stream{}
	// The discarded tail of an over-cap line still drives redaction
	// state — a `KEY` or open quote anywhere in it must mask the NEXT
	// lines — so EVERY discarded fragment feeds the credential stream
	// (a bare `PASSWORD="` in the middle must not go unnoticed), plus a
	// marker carry to catch PEM markers straddling fragment boundaries.
	// tailBoundary is the LAST PEM marker seen in the discarded tail
	// ('B' = BEGIN, 'E' = END, 0 = none) — markers are recorded in
	// textual order so `END ... BEGIN` in a tail leaves the block open.
	var tailBoundary byte
	markerCarry := ""
	discard := func(seg []byte) {
		check := markerCarry + string(seg)
		for _, m := range pemBoundaryRE.FindAllStringSubmatch(check, -1) {
			tailBoundary = m[1][0]
		}
		if len(check) > 64 {
			markerCarry = check[len(check)-64:]
		} else {
			markerCarry = check
		}
		_ = cred.Line(strings.ToValidUTF8(string(seg), "\uFFFD"))
	}
	appendSeg := func(seg []byte) {
		rem := maxLineBufBytes - len(lineBuf)
		if rem <= 0 {
			overflow = true
			discard(seg)
			return
		}
		if len(seg) > rem {
			overflow = true
			discarded := seg[rem:]
			lineBuf = append(lineBuf, seg[:rem]...)
			discard(discarded)
			return
		}
		lineBuf = append(lineBuf, seg...)
	}
	emit := func() {
		s.seq++
		raw := string(lineBuf)
		// Redact credentials before queueing for shipment: agent stderr
		// can echo secrets from the runner env and these lines are
		// persisted + streamed to the WebUI.
		// The BEGIN marker may sit behind an assignment prefix
		// (GITHUB_PRIVATE_KEY=-----BEGIN RSA...), so match Contains, not
		// just a line prefix. BEGIN/END emit distinct sentinel lines so the
		// ingestion side can tell where a masked block starts and ends.
		// PEM-body lines bypass the credential stream — they are already
		// fully masked and must not poison its cross-line state.
		// Advance credential tracking on EVERY line — a PEM boundary
		// line can also open a quoted credential
		// (`-----END RSA PRIVATE KEY----- PASSWORD="x`), so even masked
		// PEM lines feed the stream (the result is used outside PEM
		// regions only). Invalid UTF-8 is normalized BEFORE the byte
		// limit — json.Marshal would otherwise expand it to U+FFFD on
		// the way out.
		masked := cred.Line(strings.ToValidUTF8(raw, "\uFFFD"))
		var line string
		// PEM markers apply in textual order — a single line can carry
		// `END ... -----BEGIN ...` and the block stays open at the last
		// marker. Only a PRIVATE KEY marker counts; an interleaved
		// `-----END CERTIFICATE-----` must not close the block.
		switch n := pemMarkerCount(raw); {
		case n > 1:
			inPEM = pemOrderedState(raw, inPEM)
			line = "***"
		case n == 1:
			inPEM = pemOrderedState(raw, inPEM)
			if inPEM {
				line = pemBeginSentinel
			} else {
				line = pemEndSentinel
			}
		case inPEM:
			line = redactedLine
		default:
			line = truncateShipLine(masked)
		}
		// An overflowed line's discarded tail still drives redaction
		// state: the last marker seen there reopens/closes the block, and
		// credential state was already advanced per discarded fragment —
		// a `PASSWORD="` past the cap masks the value on the next lines.
		if overflow {
			switch tailBoundary {
			case 'B':
				inPEM = true
				line = pemBeginSentinel
			case 'E':
				if inPEM {
					line = pemEndSentinel
				}
				inPEM = false
			}
		}
		select {
		case s.lines <- logEntry{Seq: s.seq, TS: time.Now().UTC(), Line: line}:
		default:
			s.dropped.Add(1)
		}
		lineBuf = lineBuf[:0]
		overflow = false
		tailBoundary = 0
		markerCarry = ""
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
