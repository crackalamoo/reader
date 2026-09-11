# Reader

A small Go web app for reading a URL (article or PDF) side by side with a chat
pane, where the chat is answered by a **live coding-agent session running on
this machine** (Claude Code, Codex, or similar), not by an API call. The server
is a mailbox: the browser posts questions, the server pushes them to the
attached agent session over a WebSocket, and the session posts replies back.
No API key, no API credits.

## Run

```
go run .                     # default port 8080; -port N or PORT=N to change
```

Flags: `-port`, `-host` (default `0.0.0.0`), `-data` (default `./data`, holds
downloaded docs and `state.json` with chat history). No build step for the
frontend; `static/` is embedded into the binary. Requires `pdftotext`
(Homebrew `poppler`) for PDFs.

Open `http://<host>:<port>` in a browser, paste a URL, chat.

## Attaching a session (how the chat gets answered)

**If you are an agent session (Claude Code, Codex, or similar) working in this
directory and the server is up (`curl -s localhost:<port>/api/status`), attach
right away as described below, and answer questions as they arrive.**

The server pushes every user message as one JSON text frame on
`ws://127.0.0.1:<port>/agent` (loopback only). On connect it first replays
every user message that has no reply yet. Attach by opening that socket and
keeping it open for the life of the session. Claude Code does this with its
`Monitor` tool:

```
Monitor({ ws: { url: "ws://127.0.0.1:<port>/agent" }, persistent: true, description: "reader questions" })
```

Other agents can use any WebSocket client that streams incoming frames, e.g.
`websocat ws://127.0.0.1:<port>/agent` run in the background.

Each frame looks like:

```json
{"msgId":"…","docId":"…","url":"https://…","title":"…","kind":"pdf","file":"/abs/data/docs/<id>.pdf","textFile":"/abs/data/docs/<id>.txt","quote":"selected text, if any","text":"the user's question"}
```

To answer: read `textFile` (PDFs) or `file` (HTML) as needed, then

```
curl -s -X POST http://127.0.0.1:<port>/api/reply -H 'Content-Type: application/json' \
  -d '{"msgId":"<msgId>","text":"<your answer>"}'
```

Only requests from 127.0.0.1 are accepted on `/agent` and `/api/reply`. Write
replies as plain text (the UI does not render Markdown). Use a heredoc or a
temp JSON file for long answers rather than escaping by hand.

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
