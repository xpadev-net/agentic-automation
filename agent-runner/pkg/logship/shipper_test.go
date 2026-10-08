package logship

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type captured struct {
	batches [][]logEntry
}

func newCaptureServer(t *testing.T) (*captured, *httptest.Server) {
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Entries []logEntry `json:"entries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.batches = append(c.batches, req.Entries)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return c, srv
}

func TestAttachDisabledWithoutConfig(t *testing.T) {
	s, err := Attach("", "tok", 1, 0)
	if err != nil || s != nil {
		t.Fatalf("expected disabled shipper, got %v %v", s, err)
	}
	s.Close() // nil-safe
}

func TestShipperForwardsStderrLines(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}

	fmt.Fprintf(os.Stderr, "hello line one\n")
	fmt.Fprintf(os.Stderr, "second line\n")
	s.Close()

	if len(c.batches) == 0 {
		t.Fatalf("no batches received")
	}
	var lines []logEntry
	for _, b := range c.batches {
		lines = append(lines, b...)
	}
	if len(lines) != 2 || lines[0].Line != "hello line one" || lines[1].Line != "second line" {
		t.Fatalf("unexpected entries: %+v", lines)
	}
	if lines[0].Seq != 1 || lines[1].Seq != 2 {
		t.Fatalf("seq not monotonic: %+v", lines)
	}
	if lines[0].TS.IsZero() {
		t.Fatalf("ts not set")
	}
	// Stderr restored after Close.
	if os.Stderr != s.orig {
		t.Fatalf("os.Stderr not restored")
	}
}

// A retry (RetryCount>0) must start its sequence in a disjoint range so it
// cannot collide with entries persisted by the earlier attempt.
func TestShipperSeqPartitionedByRetryCount(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 2)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "retry line\n")
	s.Close()

	var seqs []int64
	for _, b := range c.batches {
		for _, e := range b {
			seqs = append(seqs, e.Seq)
		}
	}
	if len(seqs) != 1 || seqs[0] != 2*seqAttemptStride+1 {
		t.Fatalf("expected seq 2*stride+1, got %v", seqs)
	}
}

// Lines larger than the scanner-era 1MiB cap must not kill readLoop: the
// line is still mirrored raw to stderr, shipped truncated to the server's
// per-line limit, and later lines keep flowing.
func TestShipperOversizedLineStillFlows(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	// Benign giant content — spaces keep it below the base64-run
	// threshold so it exercises truncation rather than redaction.
	big := strings.Repeat("x ", 1024*1024)
	fmt.Fprintf(os.Stderr, "%s\n", big)
	fmt.Fprintf(os.Stderr, "after big\n")
	s.Close()

	var lines []string
	for _, b := range c.batches {
		for _, e := range b {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("unexpected lines (count=%d)", len(lines))
	}
	if !strings.HasSuffix(lines[0], truncMark) || len(lines[0]) > 8192 {
		t.Fatalf("oversized line not truncated: len=%d", len(lines[0]))
	}
	if lines[1] != "after big" {
		t.Fatalf("line after oversized lost: %q", lines[1])
	}
}

func TestShipperRedactsSecrets(t *testing.T) {
	c, srv := newCaptureServer(t)
	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "token: ghp_%s\n", strings.Repeat("a", 36))
	fmt.Fprintf(os.Stderr, "OPENAI_API_KEY=sk-test-value-12345\n")
	s.Close()

	var lines []string
	for _, b := range c.batches {
		for _, e := range b {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("unexpected lines (count=%d)", len(lines))
	}
	if strings.Contains(lines[0], "ghp_") || strings.Contains(lines[1], "sk-test-value") {
		t.Fatalf("secrets not redacted: %v", lines)
	}
}

func TestShipperRetriesOn429(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var req struct {
			Entries []logEntry `json:"entries"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "retry me\n")
	s.Close()
	if attempts < 2 {
		t.Fatalf("expected retry after 429, attempts=%d", attempts)
	}
}

func TestShipperDropsOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "will be dropped\n")
	// Give the send loop a moment to attempt and fail.
	time.Sleep(300 * time.Millisecond)
	s.Close()
	if s.dropped.Load() == 0 {
		t.Fatalf("expected dropped count > 0")
	}
}

// A stderr record without a newline must be drained and shipped bounded:
// readLoop only accumulates maxLineBufBytes instead of the whole record.
func TestShipperNewlineFreeRecordBounded(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	// ~5 MiB with no trailing newline; spaces keep it below the
	// base64-run threshold so it exercises truncation, not redaction.
	fmt.Fprintf(os.Stderr, "%s", strings.Repeat("y ", 5*1024*1024/2))
	s.Close()

	var lines []string
	for _, b := range c.batches {
		for _, e := range b {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 shipped line, got %d", len(lines))
	}
	if !strings.HasSuffix(lines[0], truncMark) || len(lines[0]) > 8192 {
		t.Fatalf("line not truncated: len=%d", len(lines[0]))
	}
}

// The final flush on Close must use a live context: Close cancels
// shutdownCtx before the send loop exits, and a flush that reused it would
// silently drop the last lines.
func TestShipperCloseStillFlushesQueuedLines(t *testing.T) {
	for i := 0; i < 30; i++ {
		c, srv := newCaptureServer(t)
		s, err := Attach(srv.URL, "tok", 7, 0)
		if err != nil || s == nil {
			t.Fatalf("attach failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "tail line\n")
		s.Close()
		if len(c.batches) == 0 {
			t.Fatalf("iteration %d: final batch dropped", i)
		}
	}
}

// The retry delay must not run after the last attempt — with Retry-After
// the send goroutine would idle one extra period while the queue fills.
func TestShipperSkipsDelayAfterFinalAttempt(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "throttled\n")
	start := time.Now()
	s.Close()
	elapsed := time.Since(start)

	// 3 attempts => 2 inter-attempt sleeps of ~3s each; a trailing sleep
	// after the last attempt would push this past ~9s.
	if elapsed > 8*time.Second {
		t.Fatalf("close took %v — retry delay ran after the final attempt", elapsed)
	}
	if attempts != sendAttempts {
		t.Fatalf("expected %d attempts, got %d", sendAttempts, attempts)
	}
}

func TestEncodeBatchSplitsByEncodedSize(t *testing.T) {
	// '<' marshals to < — six bytes per character — so raw-size
	// budgeting would overshoot the encoded body cap.
	line := strings.Repeat("<", maxShipLineBytes)
	entries := make([]logEntry, 200)
	for i := range entries {
		entries[i] = logEntry{Seq: int64(i + 1), Line: line}
	}
	var consumed, bodies int
	for consumed < len(entries) {
		body, n := encodeBatch(entries[consumed:])
		if n == 0 {
			t.Fatal("encodeBatch made no progress")
		}
		if len(body) > maxRequestBodyJSON {
			t.Fatalf("body of %d bytes exceeds cap %d", len(body), maxRequestBodyJSON)
		}
		var req struct {
			Entries []logEntry `json:"entries"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Entries) != n {
			t.Fatalf("invalid encoded body: n=%d err=%v", n, err)
		}
		consumed += n
		bodies++
	}
	if bodies < 2 {
		t.Fatalf("expected the batch to split, got %d body", bodies)
	}
}

// A batch aborted mid-send by Close()'s shutdownCtx cancellation must be
// retried under the drain budget, not silently dropped: the first request
// blocks server-side, Close cancels it client-side, and the drain-path
// retry lands as a fresh request.
func TestShipperCloseRetriesInFlightBatch(t *testing.T) {
	const n = 200
	var mu sync.Mutex
	var got []string
	var firstRequest int32
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&firstRequest, 1) == 1 {
			close(started)
			<-release // in-flight until the test finishes
			return
		}
		var req struct {
			Entries []logEntry `json:"entries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		for _, e := range req.Entries {
			got = append(got, e.Line)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(os.Stderr, "inflight-%d\n", i)
	}
	<-started // wait for the first (blocking) batch to go in-flight
	s.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("expected %d lines delivered via drain retry, got %d", n, len(got))
	}
}

// A credential name on one line and its value on the next must mask the
// value line — splitting before redaction would ship it raw.
func TestShipperMasksCredentialValueOnNextLine(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "CURSOR_API_KEY\n")
	fmt.Fprintf(os.Stderr, "xyz987uvw654\n")
	fmt.Fprintf(os.Stderr, "after\n")
	s.Close()

	var lines []logEntry
	for _, b := range c.batches {
		lines = append(lines, b...)
	}
	if len(lines) != 3 {
		t.Fatalf("unexpected entries: %+v", lines)
	}
	if lines[0].Line != "CURSOR_API_KEY" || lines[1].Line != "***" || lines[2].Line != "after" {
		t.Fatalf("cross-line credential not masked: %+v", lines)
	}
}

// A `-----END CERTIFICATE-----` line inside an open private-key block must
// not close masking — the remaining key body stays redacted.
func TestShipperCertificateEndDoesNotClosePEM(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----\n")
	fmt.Fprintf(os.Stderr, "-----END CERTIFICATE-----\n")
	fmt.Fprintf(os.Stderr, "MIIEshortkeybody\n")
	fmt.Fprintf(os.Stderr, "-----END RSA PRIVATE KEY-----\n")
	fmt.Fprintf(os.Stderr, "done\n")
	s.Close()

	var lines []logEntry
	for _, b := range c.batches {
		lines = append(lines, b...)
	}
	want := []string{
		"[REDACTED PRIVATE KEY BEGIN]",
		"[REDACTED]",
		"[REDACTED]",
		"[REDACTED PRIVATE KEY END]",
		"done",
	}
	if len(lines) != len(want) {
		t.Fatalf("unexpected entries: %+v", lines)
	}
	for i, w := range want {
		if lines[i].Line != w {
			t.Fatalf("line %d: want %q got %q (all: %+v)", i, w, lines[i].Line, lines)
		}
	}
}

// A credential key buried past the 64 KiB accumulation cap must still
// prime redaction state: the next line is the value and ships masked.
func TestShipperOverflowTailStillMasksNextLine(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	big := strings.Repeat("x ", 40*1024) // >64 KiB so the key lands in the discarded tail
	fmt.Fprintf(os.Stderr, "%sOPERATOR_API_TOKEN\n", big)
	fmt.Fprintf(os.Stderr, "xyz987uvw654\n")
	s.Close()

	var lines []string
	for _, b := range c.batches {
		for _, e := range b {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("unexpected lines (count=%d)", len(lines))
	}
	if lines[1] != "***" {
		t.Fatalf("credential value after overflowed KEY leaked: %q", lines[1])
	}
}

// A PEM BEGIN marker in the discarded tail must still open masking for
// following lines until its END marker.
func TestShipperOverflowTailPEMMarkerStillMasks(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	big := strings.Repeat("x ", 40*1024)
	fmt.Fprintf(os.Stderr, "%s-----BEGIN RSA PRIVATE KEY-----\n", big)
	fmt.Fprintf(os.Stderr, "shortkeybody\n")
	fmt.Fprintf(os.Stderr, "-----END RSA PRIVATE KEY-----\n")
	fmt.Fprintf(os.Stderr, "after\n")
	s.Close()

	var lines []string
	for _, b := range c.batches {
		for _, e := range b {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 4 {
		t.Fatalf("unexpected lines (count=%d): %v", len(lines), lines)
	}
	if lines[1] != redactedLine || lines[2] != pemEndSentinel || lines[3] != "after" {
		t.Fatalf("tail PEM marker did not mask body: %v", lines)
	}
}

// Invalid UTF-8 must be normalized before the byte limit — otherwise a
// 5000-byte 0xff line JSON-decodes to 15000 bytes and the batch 400s.
func TestShipperInvalidUTF8StaysUnderLimit(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	os.Stderr.Write(bytes.Repeat([]byte{0xff}, 5000))
	os.Stderr.Write([]byte("\n"))
	s.Close()

	var lines []string
	for _, b := range c.batches {
		for _, e := range b {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("unexpected lines (count=%d)", len(lines))
	}
	// ToValidUTF8 collapses the invalid run to one U+FFFD — far under the
	// limit — and the decoded JSON line is valid UTF-8 either way.
	if !utf8.ValidString(lines[0]) || len(lines[0]) > 8192 {
		t.Fatalf("invalid UTF-8 line escaped the byte limit: len=%d", len(lines[0]))
	}
}

// A PEM closing line that also opens a quoted credential must keep the
// credential tracker alive — the continuation lines still mask.
func TestShipperPEMLineAdvancesCredentialState(t *testing.T) {
	c, srv := newCaptureServer(t)
	s, err := Attach(srv.URL, "tok", 8, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintln(os.Stderr, "-----BEGIN RSA PRIVATE KEY-----")
	fmt.Fprintln(os.Stderr, `-----END RSA PRIVATE KEY----- PASSWORD="correct`)
	fmt.Fprintln(os.Stderr, "horse battery")
	fmt.Fprintln(os.Stderr, `staple"`)
	s.Close()

	var lines []logEntry
	for _, b := range c.batches {
		lines = append(lines, b...)
	}
	if len(lines) != 4 {
		t.Fatalf("entries: %+v", lines)
	}
	if lines[0].Line != pemBeginSentinel {
		t.Fatalf("begin: %q", lines[0].Line)
	}
	if lines[1].Line != pemEndSentinel {
		t.Fatalf("end+cred: %q", lines[1].Line)
	}
	for i := 2; i < 4; i++ {
		if lines[i].Line != "***" {
			t.Fatalf("continuation %d: %q", i, lines[i].Line)
		}
	}
}

// `END ... BEGIN` on one line leaves the block open at its last marker —
// a following body fragment still ships masked.
func TestShipperPEMMarkersAppliedInOrder(t *testing.T) {
	c, srv := newCaptureServer(t)
	s, err := Attach(srv.URL, "tok", 8, 0)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintln(os.Stderr, "-----END RSA PRIVATE KEY----- -----BEGIN RSA PRIVATE KEY-----")
	fmt.Fprintln(os.Stderr, "shortkeyfragment")
	s.Close()

	var lines []logEntry
	for _, b := range c.batches {
		lines = append(lines, b...)
	}
	if len(lines) != 2 {
		t.Fatalf("entries: %+v", lines)
	}
	if lines[0].Line != "***" {
		t.Fatalf("mixed-marker line: %q", lines[0].Line)
	}
	if lines[1].Line != redactedLine {
		t.Fatalf("body after END...BEGIN must stay masked: %q", lines[1].Line)
	}
}
