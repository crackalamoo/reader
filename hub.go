package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub fans messages out to browsers (SSE, per doc) and agents (WebSocket).
type Hub struct {
	mu       sync.Mutex
	browsers map[chan *Msg]string // channel -> docId filter ("" = all)
	agents   map[chan *Msg]struct{}
}

func newHub() *Hub {
	return &Hub{browsers: map[chan *Msg]string{}, agents: map[chan *Msg]struct{}{}}
}

func (h *Hub) Publish(m *Msg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, docID := range h.browsers {
		if docID == "" || docID == m.DocID {
			select {
			case ch <- m:
			default:
			}
		}
	}
	if m.Role == "user" {
		for ch := range h.agents {
			select {
			case ch <- m:
			default:
			}
		}
	}
}

func (h *Hub) subscribeBrowser(docID string) (chan *Msg, func()) {
	ch := make(chan *Msg, 32)
	h.mu.Lock()
	h.browsers[ch] = docID
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.browsers, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) subscribeAgent() (chan *Msg, func()) {
	ch := make(chan *Msg, 32)
	h.mu.Lock()
	h.agents[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.agents, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) AgentCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.agents)
}

func (s *server) serveSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := s.hub.subscribeBrowser(r.URL.Query().Get("docId"))
	defer cancel()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			fl.Flush()
		case m := <-ch:
			b, _ := json.Marshal(m)
			if _, err := w.Write([]byte("event: message\ndata: " + string(b) + "\n\n")); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// agentEvent is one WebSocket frame sent to an attached agent.
type agentEvent struct {
	MsgID    string `json:"msgId"`
	DocID    string `json:"docId"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	TextFile string `json:"textFile,omitempty"`
	Quote    string `json:"quote,omitempty"`
	Text     string `json:"text"`
}

func (s *server) agentEventFor(m *Msg) agentEvent {
	ev := agentEvent{MsgID: m.ID, DocID: m.DocID, Quote: m.Quote, Text: m.Text}
	if d, ok := s.store.Doc(m.DocID); ok {
		ev.URL, ev.Title, ev.Kind, ev.File, ev.TextFile = d.URL, d.Title, d.Kind, d.File, d.TextFile
	}
	return ev
}

// serveAgentWS replays unanswered messages, then streams new ones.
func (s *server) serveAgentWS(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "agent socket is localhost only", http.StatusForbidden)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("agent ws accept: %v", err)
		return
	}
	defer c.CloseNow()
	log.Printf("agent attached from %s", r.RemoteAddr)
	defer log.Printf("agent detached")

	ctx := r.Context()
	send := func(m *Msg) error {
		b, _ := json.Marshal(s.agentEventFor(m))
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, b)
	}

	ch, cancel := s.hub.subscribeAgent()
	defer cancel()

	for _, m := range s.store.Unanswered() {
		if err := send(m); err != nil {
			return
		}
	}

	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		case m := <-ch:
			if err := send(m); err != nil {
				return
			}
		}
	}
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
