package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Broadcaster fans "data changed" notifications out to SSE subscribers.
// The SPA invalidates its query cache on each event, so every open view
// updates live (docs/v2-plan.md §5.5).
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan string]bool
}

// NewBroadcaster builds an empty broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[chan string]bool)}
}

// Notify tells every subscriber that data changed.
func (b *Broadcaster) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := time.Now().UTC().Format(time.RFC3339)
	for ch := range b.subs {
		select {
		case ch <- msg:
		default: // slow subscriber: drop rather than block ingest
		}
	}
}

func (b *Broadcaster) subscribe() chan string {
	ch := make(chan string, 4)
	b.mu.Lock()
	b.subs[ch] = true
	b.mu.Unlock()
	return ch
}

func (b *Broadcaster) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// events serves /api/v1/events as Server-Sent Events.
func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	if h.deps.Events == nil {
		http.Error(w, "live updates unavailable", http.StatusNotImplemented)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	ch := h.deps.Events.subscribe()
	defer h.deps.Events.unsubscribe(ch)

	// Initial hello so clients know the stream is live.
	fmt.Fprintf(w, "event: hello\ndata: connected\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ts := <-ch:
			fmt.Fprintf(w, "event: changed\ndata: %s\n\n", ts)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
