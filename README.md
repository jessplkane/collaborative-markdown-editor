# markdown-editor

A real-time collaborative markdown editor. Multiple people can edit the same entry at once and see each other's changes as they type. The sync layer is a WebSocket protocol written by hand in Go; the frontend is React with CodeMirror and a live preview.

This is a portfolio project built to learn WebSockets, so the interesting parts are the room and sequencing logic in `server/`, not the UI.

## Status

Built so far:

- Entries list, with create and delete, backed by SQLite.
- A single entry view: CodeMirror editor beside a rendered markdown preview.
- Live editing over a WebSocket. Several browsers can edit one entry and converge on the same text. Edits are saved to SQLite, and the entry title is derived from the first line or heading.

Not built yet:

- Names, colours and remote cursors (who is viewing the entry).
- Automatic reconnect and resume after a dropped connection. Today the editor goes read-only and asks you to reload.
- Kicking viewers back to the list when an entry is deleted under them.
- Document size cap and flushing unsaved edits on graceful shutdown. A restart can lose edits from roughly the last second.
- Playwright tests for presence, reconnect and delete-while-viewing.

## How it works

```
browser (React, CodeMirror)  <-- REST /api/* -->  Go server  <-->  SQLite
                             <-- WebSocket -->    (one room per open entry)
                                /ws/entries/{id}
```

**One authority per entry.** Each entry that has viewers gets a *room*: a single goroutine in `server/room.go` that owns the document, its version number and its client set. All edits go through that goroutine over a channel, so they are applied in a single order with no locks. When the last viewer leaves, the room saves and shuts down.

**Optimistic clients, server-sequenced.** The browser applies your keystrokes immediately and sends them as a batch tagged with the document version they were made on. The server accepts a batch only if that version is current, then broadcasts it to everyone in the room, author included. If the version is stale, the server replies `reject` after first sending the updates that made it stale; the client (using `@codemirror/collab`) rebases its unconfirmed edits on top of them and resends. All transformation happens in the browser; the Go server only sequences and applies.

**Positions are UTF-16 code units**, matching CodeMirror, so an emoji counts as two. The server keeps the document as `[]uint16` for this reason and converts to a string only to save or send a snapshot.

**Persistence.** Edits are saved to SQLite about one second after the last change, and at most five seconds after the first unsaved one, so continuous typing still gets saved. They are also saved when the last viewer leaves.

### Wire protocol

JSON text frames. Also documented in `server/room.go` and `web/src/protocol.ts`.

| Direction | Message | Meaning |
|---|---|---|
| server → client | `{"type":"snapshot","doc":"…","version":N}` | Sent first on connect. |
| client → server | `{"type":"update","clientID":"…","version":N,"updates":[{"changes":[{"from":0,"to":0,"insert":"hi"}]}]}` | Steps made on top of version N. Each step is one version; positions in a step refer to the document before that step. |
| server → client | `{"type":"updates","version":N,"clientID":"…","updates":[…]}` | An accepted batch, based on version N. A matching `clientID` tells the author it is confirmed. |
| server → client | `{"type":"reject","version":N}` | The batch was stale. N is the current version. |

A malformed or out-of-range update closes the connection (close code 1007 or 1008). Close code 4004 means the entry does not exist.

## Running it

Requirements: Go 1.27 or newer, and Node 22.12+ (or 20.19+).

```sh
cd web && npm install && cd ..
make dev
```

Open http://localhost:5173. `make dev` starts the Go server on :8080 and the Vite dev server on :5173, and Vite proxies `/api` and `/ws` to Go, so the browser only ever sees one origin. The SQLite file is created at `server/data.db`.

To run the pieces separately: `make server` and `make web`.

### Configuration

| Variable | Where | Default | Purpose |
|---|---|---|---|
| `PORT` | server | `8080` | Port to listen on. |
| `DB_PATH` | server | `data.db` | SQLite file. Relative to the server's working directory, so set an absolute path in production. |
| `ALLOWED_ORIGINS` | server | none | Comma-separated host patterns (`app.example.com`, `*.example.com`, `localhost:5173`) allowed to call the API and open WebSockets from another origin. Unset means same-origin only. |
| `VITE_API_URL` | web, build time | none | Base URL of the Go server, such as `https://my-app.fly.dev`. Unset means same origin. See `web/.env.example`. |
| `SERVER_URL` | web, dev only | `http://localhost:8080` | Where the Vite dev proxy forwards `/api` and `/ws`. Read from the shell environment. |

## Tests

```sh
cd server && go test -race ./...     # document logic, rooms, websocket integration, CORS
cd web && npx tsc -b && npm run lint
```

The Go tests cover applying edits (including emoji and invalid ranges), title derivation, version sequencing and stale rejection, saving, slow-client dropping, join/leave races, and real WebSocket clients against an `httptest` server. `TestTwoClientsTypingConcurrentlyConverge` runs two such clients editing at once, each sending as soon as its last batch is confirmed or rejected, and checks that they and the server end with identical text and that no edit is lost or duplicated.

The browser test lives in `web/e2e/` and needs Chromium once (`npx playwright install chromium`). `npm run e2e` starts its own Go server (throwaway database, port 8181) and Vite (port 5273), so it can run while `make dev` is up. It opens two browser contexts on one entry, types into both at the same time, and checks that both editors, the preview and a third, fresh browser show the same text, with each writer's keystrokes complete and in order.

## Deploying

The frontend and the server deploy separately.

- **Frontend** (Netlify): `netlify.toml` at the repo root builds `web/` (`npm run build`) and publishes `web/dist`, with `VITE_API_URL` set to the server's URL at build time. It also rewrites unknown paths to `index.html`, which client-side routing needs. Publishing the unbuilt `web/` directory instead makes the browser request `/src/main.tsx` and fail with a MIME-type error for module scripts.
- **Server** (Fly.io): `server/Dockerfile` and `server/fly.toml` build and run the Go server. It needs a long-lived process and a persistent disk, so it cannot run on serverless functions. `fly.toml` mounts a volume named `data` at `/data` and sets `DB_PATH=/data/entries.db`; a Fly machine's root filesystem is wiped on every restart and deploy, so the database must live on the volume. Run **one machine**: rooms live in that process's memory, so two machines would split an entry's editors into separate rooms (`fly scale count 1`, and `fly deploy --ha=false` on the first deploy). Set `ALLOWED_ORIGINS` (uncomment it in `fly.toml`, or use `fly secrets set`) to the frontend's host.

## Known limitations

- A lone UTF-16 surrogate in an edit (half of an emoji sent on its own) becomes U+FFFD when the server decodes the JSON. Positions stay aligned, but that character's content differs between that client and the server. Full characters, including emoji, are handled correctly.
- Entries have no owner or access control: anyone with the URL can read and edit it.

## Layout

```
server/
  main.go        wiring, config, graceful HTTP shutdown
  api.go         REST handlers
  store.go       SQLite store
  document.go    UTF-16 document, applying changes, title derivation
  room.go        hub, rooms, wire protocol
  ws.go          WebSocket handler
  cors.go        CORS and allowed-origin matching
web/src/
  router.tsx, EntriesPage.tsx, EntryPage.tsx
  EntryEditor.tsx  CodeMirror editor, preview and socket lifecycle
  collab.ts        client half of the sync protocol
  protocol.ts, config.ts, api.ts
```
