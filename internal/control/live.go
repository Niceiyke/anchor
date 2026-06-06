package control

import (
	"encoding/json"
	"net/http"
	"sync"
)

// broadcaster is a tiny topic-based pub/sub used to push live events
// (deploy logs/status) to connected browsers over SSE.
type broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan any]struct{} // topic -> set of subscriber channels
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[string]map[chan any]struct{}{}}
}

func (b *broadcaster) subscribe(topic string) chan any {
	ch := make(chan any, 64)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = map[chan any]struct{}{}
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broadcaster) unsubscribe(topic string, ch chan any) {
	b.mu.Lock()
	if set := b.subs[topic]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(b.subs, topic)
		}
	}
	b.mu.Unlock()
}

func (b *broadcaster) publish(topic string, msg any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[topic] {
		select {
		case ch <- msg:
		default: // drop if subscriber is slow
		}
	}
}

func deploymentTopic(id string) string { return "deployment:" + id }

// broadcast is a convenience used by event ingestion.
func (s *Server) broadcast(topic string, msg any) { s.live.publish(topic, msg) }

// handleEventStream is the browser-facing SSE endpoint. ?topic=deployment:<id>
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	topic := r.URL.Query().Get("topic")
	if topic == "" {
		http.Error(w, "topic required", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.live.subscribe(topic)
	defer s.live.unsubscribe(topic, ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			b, _ := json.Marshal(msg)
			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}
