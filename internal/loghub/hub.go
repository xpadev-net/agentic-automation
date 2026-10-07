// Package loghub fans freshly-ingested agent log lines out to live SSE
// subscribers of the WebUI. It is best-effort: slow subscribers drop events
// and clients detect gaps via seq numbers and resync through the REST API.
package loghub

import (
	"sync"
	"time"
)

// Event is a single log line notification for one agent run.
type Event struct {
	AgentRunID int
	Seq        int64
	TS         *time.Time
	Line       string
}

// subscriberBuffer bounds queued events per subscriber; overflow is dropped.
const subscriberBuffer = 256

// Hub tracks subscribers per agent run.
type Hub struct {
	mu   sync.Mutex
	subs map[int]map[chan Event]struct{}
}

// New creates an empty Hub.
func New() *Hub {
	return &Hub{subs: make(map[int]map[chan Event]struct{})}
}

// Publish delivers an event to all subscribers of the run, non-blocking.
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[ev.AgentRunID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe registers a subscriber channel for a run. The returned cancel
// function unregisters and closes the channel.
func (h *Hub) Subscribe(agentRunID int) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	h.mu.Lock()
	if h.subs[agentRunID] == nil {
		h.subs[agentRunID] = make(map[chan Event]struct{})
	}
	h.subs[agentRunID][ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if set, ok := h.subs[agentRunID]; ok {
			if _, ok := set[ch]; ok {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(h.subs, agentRunID)
			}
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// defaultHub is the process-wide hub used by ingestion handlers and SSE.
var defaultHub = New()

// Publish delivers an event to the default hub.
func Publish(ev Event) { defaultHub.Publish(ev) }

// Subscribe registers on the default hub.
func Subscribe(agentRunID int) (<-chan Event, func()) {
	return defaultHub.Subscribe(agentRunID)
}
