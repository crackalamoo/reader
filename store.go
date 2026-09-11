package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxDocs caps the number of stored documents. Opening one more evicts the
// least recently opened doc along with its chat history and files.
const maxDocs = 10

type Doc struct {
	ID       string    `json:"id"`
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Kind     string    `json:"kind"` // "pdf" or "html"
	File     string    `json:"file"`
	TextFile string    `json:"textFile,omitempty"`
	Opened   time.Time `json:"opened"`
}

// Msg is a chat message; Role is "user" or "assistant".
type Msg struct {
	ID      string    `json:"id"`
	DocID   string    `json:"docId"`
	Role    string    `json:"role"`
	Text    string    `json:"text"`
	Quote   string    `json:"quote,omitempty"`
	ReplyTo string    `json:"replyTo,omitempty"`
	Time    time.Time `json:"time"`
}

type state struct {
	Docs     []*Doc `json:"docs"`
	Messages []*Msg `json:"messages"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	st   state
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func openStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "docs"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dataDir, "state.json")}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return nil, err
	}
	return s, nil
}

// save must be called with mu held.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// AddDoc stores d, first evicting the least recently opened docs so that at
// most maxDocs remain. Docs are kept in order of Opened, oldest first.
func (s *Store) AddDoc(d *Doc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.st.Docs) >= maxDocs {
		s.evictOldest()
	}
	s.st.Docs = append(s.st.Docs, d)
	return s.save()
}

// evictOldest drops the least recently opened doc, its messages and its
// files. Must be called with mu held and a non-empty doc list.
func (s *Store) evictOldest() {
	oldest := 0
	for i, d := range s.st.Docs {
		if d.Opened.Before(s.st.Docs[oldest].Opened) {
			oldest = i
		}
	}
	old := s.st.Docs[oldest]
	s.st.Docs = append(s.st.Docs[:oldest], s.st.Docs[oldest+1:]...)
	kept := s.st.Messages[:0]
	for _, m := range s.st.Messages {
		if m.DocID != old.ID {
			kept = append(kept, m)
		}
	}
	s.st.Messages = kept
	for _, f := range []string{old.File, old.TextFile} {
		if f == "" {
			continue
		}
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("evict %s: %v", old.ID, err)
		}
	}
	log.Printf("evicted doc %s (%s)", old.ID, old.URL)
}

// TouchDocByURL marks the doc for url as opened now, moving it to the end of
// the list so it is the newest, and persists the change.
func (s *Store) TouchDocByURL(url string) (*Doc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.st.Docs {
		if d.URL != url {
			continue
		}
		nd := *d
		nd.Opened = time.Now()
		s.st.Docs = append(append(s.st.Docs[:i:i], s.st.Docs[i+1:]...), &nd)
		if err := s.save(); err != nil {
			log.Printf("touch doc %s: %v", d.ID, err)
		}
		return &nd, true
	}
	return nil, false
}

func (s *Store) Doc(id string) (*Doc, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.st.Docs {
		if d.ID == id {
			return d, true
		}
	}
	return nil, false
}

func (s *Store) Docs() []*Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Doc, len(s.st.Docs))
	copy(out, s.st.Docs)
	return out
}

func (s *Store) AddMsg(m *Msg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Messages = append(s.st.Messages, m)
	return s.save()
}

func (s *Store) Msg(id string) (*Msg, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.st.Messages {
		if m.ID == id {
			return m, true
		}
	}
	return nil, false
}

func (s *Store) Messages(docID string) []*Msg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Msg
	for _, m := range s.st.Messages {
		if docID == "" || m.DocID == docID {
			out = append(out, m)
		}
	}
	if out == nil {
		out = []*Msg{}
	}
	return out
}

func (s *Store) Unanswered() []*Msg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	answered := map[string]bool{}
	for _, m := range s.st.Messages {
		if m.Role == "assistant" && m.ReplyTo != "" {
			answered[m.ReplyTo] = true
		}
	}
	var out []*Msg
	for _, m := range s.st.Messages {
		if m.Role == "user" && !answered[m.ID] {
			out = append(out, m)
		}
	}
	return out
}
