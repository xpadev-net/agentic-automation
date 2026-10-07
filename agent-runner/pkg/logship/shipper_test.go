package logship

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
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
		var req logsRequest
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
	s, err := Attach("", "tok", 1)
	if err != nil || s != nil {
		t.Fatalf("expected disabled shipper, got %v %v", s, err)
	}
	s.Close() // nil-safe
}

func TestShipperForwardsStderrLines(t *testing.T) {
	c, srv := newCaptureServer(t)

	s, err := Attach(srv.URL, "tok", 7)
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

func TestShipperDropsOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	s, err := Attach(srv.URL, "tok", 7)
	if err != nil || s == nil {
		t.Fatalf("attach failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "will be dropped\n")
	// Give the send loop a moment to attempt and fail.
	time.Sleep(300 * time.Millisecond)
	s.Close()
	if s.dropped == 0 {
		t.Fatalf("expected dropped count > 0")
	}
}
