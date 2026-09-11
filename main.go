package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

type server struct {
	store   *Store
	hub     *Hub
	dataDir string
}

func main() {
	defaultPort := 8080
	if p := os.Getenv("PORT"); p != "" {
		fmt.Sscanf(p, "%d", &defaultPort)
	}
	port := flag.Int("port", defaultPort, "port to listen on (or set PORT)")
	host := flag.String("host", "0.0.0.0", "address to bind")
	dataDir := flag.String("data", "data", "directory for downloaded docs and chat state")
	flag.Parse()

	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	store, err := openStore(abs)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	s := &server{store: store, hub: newHub(), dataDir: abs}

	static, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("POST /api/open", s.handleOpen)
	mux.HandleFunc("GET /api/docs", s.handleDocs)
	mux.HandleFunc("GET /docs/{id}/raw", s.handleRaw)
	mux.HandleFunc("GET /docs/{id}/view", s.handleView)
	mux.HandleFunc("GET /selection.js", serveSelectionScript)
	mux.HandleFunc("GET /api/messages", s.handleMessages)
	mux.HandleFunc("POST /api/messages", s.handlePostMessage)
	mux.HandleFunc("GET /api/events", s.serveSSE)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /agent", s.serveAgentWS)
	mux.HandleFunc("POST /api/reply", s.handleReply)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("reader listening on http://%s (data in %s)", addr, abs)
	srv := &http.Server{Addr: addr, Handler: logRequests(mux), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/events") {
			log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

func (s *server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if d, ok := s.store.TouchDocByURL(in.URL); ok {
		writeJSON(w, http.StatusOK, d)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	d, err := openURL(ctx, s.dataDir, in.URL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := s.store.AddDoc(d); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) handleDocs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Docs())
}

func (s *server) handleRaw(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Doc(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if d.Kind == "pdf" {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "inline")
		http.ServeFile(w, r, d.File)
		return
	}
	b, err := os.ReadFile(d.File)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(injectIntoHTML(b, d.URL))
}

// handleView opens a PDF in the vendored PDF.js viewer (static/pdfjs), whose
// text layer is ordinary HTML so selections can be quoted. The viewer takes the
// document as a same-origin ?file= URL; it must be served from its own path so
// its relative assets resolve, hence the redirect.
func (s *server) handleView(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.Doc(r.PathValue("id"))
	if !ok || d.Kind != "pdf" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/pdfjs/web/viewer.html?file="+url.QueryEscape("/docs/"+d.ID+"/raw"), http.StatusFound)
}

// serveSelectionScript serves selectionScript as a file for the PDF.js viewer,
// whose Content-Security-Policy allows same-origin scripts but not inline ones.
func serveSelectionScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	io.WriteString(w, selectionScript)
}

// injectIntoHTML adds a <base> tag and a script that reports text selection to the parent.
func injectIntoHTML(body []byte, base string) []byte {
	inject := `<base href="` + template.HTMLEscapeString(base) + `"><script>` + selectionScript + `</script>`
	lower := strings.ToLower(string(body))
	if i := strings.Index(lower, "<head>"); i >= 0 {
		i += len("<head>")
		return append(append(append([]byte{}, body[:i]...), inject...), body[i:]...)
	}
	return append([]byte(inject), body...)
}

// selectionScript posts the current text selection to the parent window; the
// app's "Quote selection" button listens for it. Injected inline into HTML docs
// and loaded via /selection.js by the PDF.js viewer.
const selectionScript = `document.addEventListener("selectionchange",function(){` +
	`var t=String(document.getSelection());window.parent.postMessage({type:"reader-selection",text:t},"*");});`

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Messages(r.URL.Query().Get("docId")))
}

func (s *server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DocID string `json:"docId"`
		Text  string `json:"text"`
		Quote string `json:"quote"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := s.store.Doc(in.DocID); !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown docId"))
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("empty message"))
		return
	}
	m := &Msg{ID: newID(), DocID: in.DocID, Role: "user", Text: in.Text, Quote: in.Quote, Time: time.Now()}
	if err := s.store.AddMsg(m); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.hub.Publish(m)
	writeJSON(w, http.StatusOK, m)
}

func (s *server) handleReply(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "reply is localhost only", http.StatusForbidden)
		return
	}
	var in struct {
		MsgID string `json:"msgId"`
		Text  string `json:"text"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	orig, ok := s.store.Msg(in.MsgID)
	if !ok || orig.Role != "user" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown msgId"))
		return
	}
	m := &Msg{ID: newID(), DocID: orig.DocID, Role: "assistant", Text: in.Text, ReplyTo: orig.ID, Time: time.Now()}
	if err := s.store.AddMsg(m); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.hub.Publish(m)
	writeJSON(w, http.StatusOK, m)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":     s.hub.AgentCount(),
		"unanswered": len(s.store.Unanswered()),
	})
}
