# Architecture

```
browser (React, CodeMirror)  <-- REST /api/* -->  Go server  <-->  SQLite
                             <-- WebSocket -->    (one room per open entry)
                                /ws/entries/{id}
```

Message formats, close codes and the reconnect design are in [protocol.md](protocol.md).

## Rooms and sequencing

**One authority per entry.** Each entry that has viewers gets a *room*: a single goroutine in `server/room.go` that owns the document, its version number and its client set. All edits go through that goroutine over a channel, so they are applied in a single order with no locks. When the last viewer leaves, the room saves, stays in memory for 30 more seconds so a client that dropped can resume into it, and then shuts down.

**Optimistic clients, server-sequenced.** The browser applies your keystrokes immediately and sends them as a batch tagged with the document version they were made on. The server accepts a batch only if that version is current, then broadcasts it to everyone in the room, author included. If the version is stale, the server replies `reject` after first sending the updates that made it stale; the client (using `@codemirror/collab`) rebases its unconfirmed edits on top of them and resends. All transformation happens in the browser; the Go server only sequences and applies.

**Positions are UTF-16 code units**, matching CodeMirror, so an emoji counts as two. The server keeps the document as `[]uint16` for this reason and converts to a string only to save or send a snapshot.

## Persistence

Edits are saved to SQLite about one second after the last change, and at most five seconds after the first unsaved one, so continuous typing still gets saved. They are also saved when the last viewer leaves, and on graceful shutdown. The entry title is derived from the first line or heading.

## Deleting an entry

`DELETE /api/entries/{id}` removes the row and then tells the hub (`Hub.Delete`), which tells the entry's room if it has one. The room closes every client with code 4010, discards unsaved edits instead of saving them, and exits. There is no gap in which a room could be created for the deleted entry: creating one reads the store while holding the hub's lock, so it either already exists when `Delete` looks, or finds the row gone and the client gets 4004. A viewer receiving 4010 is navigated to the list, which shows a dismissible notice (`/?notice=deleted`).

## Size cap

A document may not grow past 1,000,000 UTF-16 code units (about 1 MB of text; units rather than bytes because that is what the server stores and addresses). An update that would make it larger is refused: the room does not apply it, closes that client with 4013, and nobody else sees anything. The batch cannot be refused in-band, because a client treats a refusal as "stale" and retries; and the client's unconfirmed edits cannot be partly dropped, so the editor goes read-only with a message and a reload shows the saved text. An entry that is already over the cap (saved before the cap, or under a larger one) still opens and can shrink, but cannot grow. The websocket read limit is derived from the cap, allowing for JSON spending up to six bytes on one code unit, so the limit never rejects an update the cap allows.

## Shutdown

On SIGTERM the server stops accepting connections and then calls `Hub.Shutdown`, which asks every room to save and close its clients with 1001 ("going away", which the client treats as "reconnect"), and waits for them, all within five seconds (Fly's kill timeout). Only after that does the store close, so no save hits a closed database. `http.Server.Shutdown` does not wait for websocket connections, which is why the rooms are told separately.

## Code layout

```
server/
  main.go        wiring, config, graceful shutdown
  api.go         REST handlers
  store.go       SQLite store
  document.go    UTF-16 document, applying changes, title derivation
  room.go        hub, rooms, wire protocol, shutdown
  history.go     bounded log of accepted batches, for resume
  ws.go          WebSocket handler, close codes, size limits
  ratelimit.go   per-connection token bucket
  cors.go        CORS and allowed-origin matching
  *_test.go      unit and websocket integration tests
  Dockerfile, fly.toml
web/src/
  main.tsx, router.tsx
  EntriesPage.tsx, EntryPage.tsx, NamePrompt.tsx
  EntryEditor.tsx  CodeMirror editor, preview and socket lifecycle
  collab.ts        client half of the sync protocol
  presence.ts      viewers, remote cursors, throttled cursor reports
  stepLog.ts       recent steps, to carry a cursor to the newest version
  backoff.ts       reconnect delays
  heartbeat.ts     detects a connection that went quiet
  protocol.ts      message types and close codes (mirrors room.go)
  identity.ts      user id, name and colour
  api.ts, config.ts
  *.test.ts        Vitest unit tests
web/e2e/           Playwright specs and helpers
```
