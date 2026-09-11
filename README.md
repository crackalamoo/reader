# Reader

A small Go web app for reading a URL (article or PDF) side by side with a chat
pane, where the chat is answered by a **live coding-agent session running on
this machine** (Claude Code, Codex, or similar), not by an API call. No API
key, no API credits.

## Run

```
go run .                     # default port 8080; -port N or PORT=N to change
```

Flags: `-port`, `-host` (default `0.0.0.0`), `-data` (default `./data`, holds
downloaded docs and `state.json` with chat history). No build step for the
frontend; `static/` is embedded into the binary. Requires `pdftotext`
(Homebrew `poppler`) for PDFs.

Open `http://<host>:<port>` in a browser, paste a URL, chat.

## How the chat gets answered

Start an agent session (Claude Code, Codex, or similar) in this directory
while the server is running; the instructions in `AGENTS.md` tell it to attach to the server's loopback-only WebSocket (`/agent`), read the
document for each incoming question, and post replies via `/api/reply`. The
header in the UI shows whether a session is attached.

Replies are plain text; the UI does not render Markdown.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/open` | `{url}` → Doc. Downloads the page/PDF into `data/docs/`, extracts PDF text. Re-uses an earlier download of the same URL (and marks it as newest). At most 10 docs are kept: opening an 11th evicts the least recently opened doc, deleting its files and chat history. |
| GET | `/api/docs` | List opened docs. |
| GET | `/docs/{id}/raw` | The stored file. HTML gets a `<base>` tag and a selection-reporting script injected; the viewer iframe loads HTML docs from here. |
| GET | `/docs/{id}/view` | PDFs only: redirects to the vendored PDF.js viewer (`static/pdfjs/`, version in `VERSION`) opened on `/raw`, so text is selectable and quoting works. |
| GET | `/selection.js` | The selection-reporting script as a standalone file, loaded by the PDF.js viewer (its CSP forbids inline scripts). |
| GET | `/api/messages?docId=` | Chat history for a doc. |
| POST | `/api/messages` | `{docId,text,quote}` from the browser. Stored, pushed to agents, broadcast to browsers. |
| GET | `/api/events?docId=` | Server-sent events stream of new messages for the browser. |
| GET | `/api/status` | `{agents, unanswered}` so the UI can show whether a session is attached. |
| GET | `/agent` | WebSocket push channel to agent sessions (loopback only). |
| POST | `/api/reply` | `{msgId,text}` from an agent session (loopback only). |

## Layout

- `main.go` — flags, routes, HTTP handlers.
- `store.go` — `Doc`/`Msg` types and JSON persistence (`data/state.json`).
- `fetch.go` — URL download, PDF text extraction, title guessing.
- `hub.go` — fan-out to SSE browsers and WebSocket agents.
- `static/` — vanilla HTML/JS/CSS frontend.

`data/` holds downloaded docs and chat state and is not part of the source.
Only the 10 most recently opened docs are kept (`maxDocs` in `store.go`);
older ones are removed from `state.json` along with their chat messages and
their files under `data/docs/`.
