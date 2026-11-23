# markdown-editor

A real-time collaborative markdown editor. Multiple people can edit the same entry at once and see each other's changes as they type. The sync layer is a WebSocket protocol written by hand in Go; the frontend is React with CodeMirror and a live preview.

This is a portfolio project built to learn WebSockets, so the interesting parts are the room and sequencing logic in `server/`, not the UI.

## Status

Built so far:

- Entries list, with create and delete, backed by SQLite.
- A single entry view: CodeMirror editor beside a rendered markdown preview.
- Live editing over a WebSocket. Several browsers can edit one entry and converge on the same text. Edits are saved to SQLite, and the entry title is derived from the first line or heading.
- Names, a viewer list and remote cursors. Each viewer is asked for a name once; everyone in the entry sees who else is there and where their cursor is.
- Reconnect and resume. A dropped connection comes back with backoff and picks up where it left off; edits typed meanwhile are kept.
- Deleting an entry sends everyone viewing it back to the list with a notice.
- A document size cap, and unsaved edits are flushed on graceful shutdown, so a restart no longer loses the last second of typing.

## How it works

```
browser (React, CodeMirror)  <-- REST /api/* -->  Go server  <-->  SQLite
                             <-- WebSocket -->    (one room per open entry)
                                /ws/entries/{id}
```

**One authority per entry.** Each entry that has viewers gets a *room*: a single goroutine in `server/room.go` that owns the document, its version number and its client set. All edits go through that goroutine over a channel, so they are applied in a single order with no locks. When the last viewer leaves, the room saves, stays in memory for 30 more seconds so a client that dropped can resume into it, and then shuts down.

**Optimistic clients, server-sequenced.** The browser applies your keystrokes immediately and sends them as a batch tagged with the document version they were made on. The server accepts a batch only if that version is current, then broadcasts it to everyone in the room, author included. If the version is stale, the server replies `reject` after first sending the updates that made it stale; the client (using `@codemirror/collab`) rebases its unconfirmed edits on top of them and resends. All transformation happens in the browser; the Go server only sequences and applies.

**Positions are UTF-16 code units**, matching CodeMirror, so an emoji counts as two. The server keeps the document as `[]uint16` for this reason and converts to a string only to save or send a snapshot.

**Persistence.** Edits are saved to SQLite about one second after the last change, and at most five seconds after the first unsaved one, so continuous typing still gets saved. They are also saved when the last viewer leaves, and on graceful shutdown (see below).

### Wire protocol

JSON text frames. Also documented in `server/room.go` and `web/src/protocol.ts`.

| Direction | Message | Meaning |
|---|---|---|
| server → client | `{"type":"snapshot","doc":"…","version":N,"epoch":"…"}` | Sent first on connect, unless a resume succeeded. |
| server → client | `{"type":"resumed","version":N}` | Sent first when a resume succeeded, after the missed `updates`. The client is caught up to version N. |
| client → server | `{"type":"update","clientID":"…","version":N,"updates":[{"changes":[{"from":0,"to":0,"insert":"hi"}]}]}` | Steps made on top of version N. Each step is one version; positions in a step refer to the document before that step. |
| server → client | `{"type":"updates","version":N,"clientID":"…","updates":[…]}` | An accepted batch, based on version N. A matching `clientID` tells the author it is confirmed. |
| server → client | `{"type":"reject","version":N}` | The batch was stale. N is the current version. |
| client → server | `{"type":"ping"}` | Liveness probe, answered with `{"type":"pong"}`. See Reconnect and resume. |

A malformed message or an out-of-range update closes the connection with code 1007 or 1008. Other close codes: 4004 the entry does not exist, 4009 the same client opened a newer connection, 4010 the entry was deleted, 4013 the update would have made the document too large, 1013 the client was too slow and may try again, 1001 the server is shutting down.

### Presence

Presence is ephemeral and per connection; the server keeps no cursor state.

| Direction | Message | Meaning |
|---|---|---|
| client → server | `{"type":"hello","clientID":"…","userID":"…","name":"Ada"}` | Who this connection is. Sent right after the snapshot. The connection is listed as a viewer from then on. |
| client → server | `{"type":"cursor","version":N,"pos":P}` | The cursor is at position P of document version N. Throttled to one message per 75 ms. |
| server → client | `{"type":"viewers","viewers":[{"clientID":"…","userID":"…","name":"…"}]}` | Everyone who has said hello, in join order, including the receiver. Sent whenever that changes. |
| server → client | `{"type":"cursor","clientID":"…","version":N,"pos":P}` | Another viewer's cursor, relayed to everyone but its owner. |

**Identity** lives in `localStorage`: a random user id and the name typed at the prompt. The colour is derived from the user id by every browser, so it is never sent. Two tabs of one browser are two viewers.

**Cursors are mapped in the browser.** A cursor is reported against the version its owner had synced, with the owner's unconfirmed edits subtracted, so it means the same thing to everyone. The server relays it after every update it had already broadcast, so a receiver has always seen that version. The receiver carries the position forward through the steps since then (`web/src/stepLog.ts`) and through its own unconfirmed edits, then stores it in an editor state field that moves it with every later change. Text a viewer types at their own cursor pushes it along; anyone else's text at the same spot does not. A cursor is only re-sent when its owner moves or edits, and when someone new joins.

**Dead connections.** The server pings every connection every 20 seconds and closes one that does not answer within 10, which removes it from the viewer list. Browsers answer pings on their own.

### Reconnect and resume

When the socket closes for any reason but the terminal ones below, the editor shows "Reconnecting…", stays editable, and retries with exponential backoff (0.5 s doubling to 10 s, randomised) until it is back. Nothing is buffered on the wire: edits typed meanwhile stay in the editor as unconfirmed edits, the same as when the server has not yet acknowledged them.

**Noticing a dead connection.** A network that dies without closing the socket (or a dev proxy whose far end died) leaves a WebSocket looking open, and browsers do not expose protocol-level pings. So the client sends `{"type":"ping"}` every 5 seconds and treats any message from the server as proof of life; after 15 seconds of silence it abandons the socket and reconnects (`web/src/heartbeat.ts`).

**Resuming.** The new socket's URL carries `?epoch=E&version=N`: the room instance and document version the client had last caught up to. The room keeps its accepted batches (`server/history.go`, bounded by estimated size, 4 MiB by default) and, if `E` is its epoch and the history reaches `N`, replays the missed batches as ordinary `updates` messages, each with its original `clientID`, then sends `resumed`. The client then sends whatever is still unconfirmed.

- **A lost acknowledgement does not double-apply.** If the server applied the client's batch but the connection died before the client saw it, the replay contains that batch tagged with the client's own `clientID`, which the client treats as the confirmation. The `clientID` is kept across reconnects for this reason. A batch the server never got is not in the replay and is sent again after `resumed`.
- **Versions only mean something within one room instance.** A room's version starts at 0 whenever the room is created, so a version number alone could match a different history after a restart. Each room instance has a random `epoch`, sent in the snapshot and echoed by the client; a mismatch forces a snapshot.
- **Snapshot fallback.** If the epoch differs (server restarted, or the room was gone), the history no longer reaches the client's version, or the gap is bigger than the client's send buffer (256 messages), the server sends a snapshot instead. The editor replaces its document and drops the edits it had not yet had confirmed, and says so in a notice.
- **Linger.** An empty room stays for 30 seconds, so that the only person in an entry can drop and resume instead of always falling back to a snapshot.
- **One viewer per client.** When a client says hello, the room closes any other connection with the same `clientID` (code 4009); the server may not have noticed yet that the old one is dead.

**Which closes reconnect.** Everything except 1007, 1008, 1009 (the server refused what the client sent, and would again), 4004 (no such entry), 4009 (replaced), 4010 (deleted) and 4013 (document too large). Those freeze the editor read-only with a message, or, for 4010, leave the page. The list is `isTerminalClose` in `web/src/protocol.ts`.

### Deleting an entry

`DELETE /api/entries/{id}` removes the row and then tells the hub (`Hub.Delete`), which tells the entry's room if it has one. The room closes every client with code 4010, discards unsaved edits instead of saving them, and exits. There is no gap in which a room could be created for the deleted entry: creating one reads the store while holding the hub's lock, so it either already exists when `Delete` looks, or finds the row gone and the client gets 4004. A viewer receiving 4010 is navigated to the list, which shows a dismissible notice (`/?notice=deleted`).

### Size cap

A document may not grow past 1,000,000 UTF-16 code units (about 1 MB of text; units rather than bytes because that is what the server stores and addresses). An update that would make it larger is refused: the room does not apply it, closes that client with 4013, and nobody else sees anything. The batch cannot be refused in-band, because a client treats a refusal as "stale" and retries; and the client's unconfirmed edits cannot be partly dropped, so the editor goes read-only with a message and a reload shows the saved text. An entry that is already over the cap (saved before the cap, or under a larger one) still opens and can shrink, but cannot grow. The websocket read limit is derived from the cap, allowing for JSON spending up to six bytes on one code unit, so the limit never rejects an update the cap allows.

### Shutdown

On SIGTERM the server stops accepting connections and then calls `Hub.Shutdown`, which asks every room to save and close its clients with 1001 ("going away", which the client treats as "reconnect"), and waits for them, all within five seconds (Fly's kill timeout). Only after that does the store close, so no save hits a closed database. `http.Server.Shutdown` does not wait for websocket connections, which is why the rooms are told separately.

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
| `MAX_DOC_UNITS` | server | `1000000` | Document size cap in UTF-16 code units. Can only be lowered. The end-to-end tests set it small. |
| `RESUME_LOG_BYTES` | server | `4194304` | Estimated size of the batches a room keeps to replay to reconnecting clients. A client away for longer gets a snapshot. The end-to-end tests set it small. |
| `VITE_API_URL` | web, build time | none | Base URL of the Go server, such as `https://my-app.fly.dev`. Unset means same origin. See `web/.env.example`. |
| `SERVER_URL` | web, dev only | `http://localhost:8080` | Where the Vite dev proxy forwards `/api` and `/ws`. Read from the shell environment. |

## Tests

```sh
cd server && go test -race ./...     # document logic, rooms, websocket integration, CORS
cd web && npx tsc -b && npm run lint && npm test
```

The Go tests cover applying edits (including emoji and invalid ranges), title derivation, version sequencing and stale rejection, saving, slow-client dropping, join/leave races, presence (viewer list order, leaving, relayed cursors, malformed messages, a connection that stops answering pings), resume (replay order and authorship, the lost-acknowledgement case, each reason for falling back to a snapshot, the room lingering and being revived, a reconnecting client listed once), and real WebSocket clients against an `httptest` server. `TestTwoClientsTypingConcurrentlyConverge` runs two such clients editing at once, each sending as soon as its last batch is confirmed or rejected, and checks that they and the server end with identical text and that no edit is lost or duplicated.

The lifecycle tests cover deleting an entry in a room (every client closed with 4010, nothing saved afterwards, the room gone, a later join finding no entry, an empty lingering room ended), the delete endpoint end to end over a real websocket, the size cap (landing exactly on it counts UTF-16 units and is accepted, one unit over is refused and the document is untouched, an entry already over the cap opens and can shrink, and an update at the cap in the worst-case JSON encoding passes the read limit), and shutdown (edits typed since the last save, in an occupied and in an empty room, survive a restart against a file-backed database; joins during shutdown are told to come back).

The browser tests live in `web/e2e/` and need Chromium once (`npx playwright install chromium`). `npm run e2e` starts its own Go server (throwaway database, port 8181) and Vite (port 5273), so it can run while `make dev` is up.

- `convergence.spec.ts` opens two browser contexts on one entry, types into both at the same time, and checks that both editors, the preview and a third, fresh browser show the same text, with each writer's keystrokes complete and in order.
- `presence.spec.ts` covers the name prompt, the viewer list (including leaving when a page closes), and remote cursors: where they are drawn, how they follow edits from either side, and that a late joiner sees existing cursors.
- `lifecycle.spec.ts` deletes an entry (through the REST API) while two pages have it open and checks both land on the list with a notice, and that a paste past the size cap is refused, leaves the other viewer and the saved text untouched, and is not retried.
- `reconnect.spec.ts` severs a page's socket with `page.routeWebSocket` while it and another page keep typing, and checks that nothing is lost and both converge; that a lone client resumes too; and that when the history cannot cover the gap the editor reloads and reports the dropped edits.

`npm test` runs the Vitest unit tests: `web/src/stepLog.ts` (the log that carries a cursor from the version its owner reported to the version you have), the reconnect backoff, and which close codes are worth retrying.

## Deploying

The frontend and the server deploy separately.

- **Frontend** (Netlify): `netlify.toml` at the repo root builds `web/` (`npm run build`) and publishes `web/dist`, with `VITE_API_URL` set to the server's URL at build time. It also rewrites unknown paths to `index.html`, which client-side routing needs. Publishing the unbuilt `web/` directory instead makes the browser request `/src/main.tsx` and fail with a MIME-type error for module scripts.
- **Server** (Fly.io): `server/Dockerfile` and `server/fly.toml` build and run the Go server. It needs a long-lived process and a persistent disk, so it cannot run on serverless functions. `fly.toml` mounts a volume named `data` at `/data` and sets `DB_PATH=/data/entries.db`; a Fly machine's root filesystem is wiped on every restart and deploy, so the database must live on the volume. Run **one machine**: rooms live in that process's memory, so two machines would split an entry's editors into separate rooms (`fly scale count 1`, and `fly deploy --ha=false` on the first deploy). Set `ALLOWED_ORIGINS` (uncomment it in `fly.toml`, or use `fly secrets set`) to the frontend's host.

## Known limitations

- A lone UTF-16 surrogate in an edit (half of an emoji sent on its own) becomes U+FFFD when the server decodes the JSON. Positions stay aligned, but that character's content differs between that client and the server. Full characters, including emoji, are handled correctly.
- Entries have no owner or access control: anyone with the URL can read and edit it. In particular, `clientID`s are visible in the viewer list, so anyone can close another viewer's connection by claiming its `clientID`.
- Reconnecting depends on the client noticing the connection is gone, which takes up to 15 seconds when the network dies without closing the socket (see the heartbeat under Reconnect and resume). Edits typed in that window are kept, not lost.

## Layout

```
server/
  main.go        wiring, config, graceful HTTP shutdown
  api.go         REST handlers
  store.go       SQLite store
  document.go    UTF-16 document, applying changes, title derivation
  room.go        hub, rooms, wire protocol
  history.go     bounded log of accepted batches, for resume
  ws.go          WebSocket handler
  cors.go        CORS and allowed-origin matching
web/src/
  router.tsx, EntriesPage.tsx, EntryPage.tsx
  EntryEditor.tsx  CodeMirror editor, preview and socket lifecycle
  collab.ts        client half of the sync protocol
  backoff.ts       reconnect delays
  heartbeat.ts     detects a connection that went quiet
  protocol.ts, config.ts, api.ts
```
