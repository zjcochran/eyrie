// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/engine"
)

// hub sends events to the browsers that listen at /api/v1/events. A slow
// browser misses events rather than holding the others up.
type hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newHub() *hub { return &hub{clients: map[chan []byte]struct{}{}} }

// publish sends an event to every listener. It never blocks.
func (h *hub) publish(kind string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", kind, payload)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- msg:
		default:
		}
	}
}

// Publish sends a heartbeat from the engine to listeners.
func (s *Server) Publish(ev engine.Event) { s.hub.publish("beat", viewBeat(ev.Beat)) }

// keepalive is how often an idle stream gets a comment, so proxies keep it
// open.
const keepalive = 25 * time.Second

// events streams Server-Sent Events: "beat" with a heartbeat, "monitors"
// when a monitor is added, changed or deleted.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		invalid(w, http.StatusInternalServerError, "Streaming is not supported here.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer
	c := make(chan []byte, 64)
	s.hub.mu.Lock()
	s.hub.clients[c] = struct{}{}
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		delete(s.hub.clients, c)
		s.hub.mu.Unlock()
	}()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c:
			w.Write(msg) //nolint:errcheck,gosec // the next write or the context ends a gone client
		case <-tick.C:
			fmt.Fprint(w, ": keepalive\n\n")
		}
		flusher.Flush()
	}
}
