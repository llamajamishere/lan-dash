// Package sse implements a small fan-out hub for Server-Sent Events.
package sse

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Event types streamed to the browser.
const (
	EventDeviceStatus   = "device_status"
	EventMetric         = "metric"
	EventAlertFired     = "alert_fired"
	EventAlertResolved  = "alert_resolved"
	EventAlertUpdated   = "alert_updated"
	EventCollectorError = "collector_error"
	EventDevicesChanged = "devices_changed"
)

// Event is one message. Data is marshalled to JSON.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	Ts   int64  `json:"ts"`
}

// Publisher is what producers (collectors, alert engine) depend on.
type Publisher interface {
	Publish(eventType string, data any)
}

// Hub fans events out to connected clients. Slow clients are dropped
// rather than blocking producers.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{clients: map[chan []byte]struct{}{}} }

// Publish broadcasts an event to all clients (non-blocking).
func (h *Hub) Publish(eventType string, data any) {
	msg, err := json.Marshal(Event{Type: eventType, Data: data, Ts: time.Now().Unix()})
	if err != nil {
		slog.Error("sse marshal", "err", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default: // client buffer full; skip this event for that client
		}
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeHTTP streams events until the client disconnects.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}()

	fmt.Fprint(w, "retry: 3000\n: connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg := <-ch:
			// Every event uses the default "message" event name; the type is
			// inside the JSON so the client needs a single handler.
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
