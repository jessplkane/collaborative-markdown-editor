# Development

## Running it

Requirements: Go 1.27 or newer, and Node 22.12+ (or 20.19+).

```sh
cd web && npm install && cd ..
make dev
```

Open http://localhost:5173. `make dev` starts the Go server on :8080 and the Vite dev server on :5173, and Vite proxies `/api` and `/ws` to Go, so the browser only ever sees one origin. The SQLite file is created at `server/data.db`.

To run the pieces separately: `make server` and `make web`.

In development, the Vite proxy logs `ws proxy error` (`ECONNRESET`, `EPIPE`) when a socket is killed abruptly. Reconnecting makes that a normal event; the messages are harmless.

## Configuration

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

### Go

The Go tests cover applying edits (including emoji and invalid ranges), title derivation, version sequencing and stale rejection, saving, slow-client dropping, join/leave races, presence (viewer list order, leaving, relayed cursors, malformed messages, a connection that stops answering pings), resume (replay order and authorship, the lost-acknowledgement case, each reason for falling back to a snapshot, the room lingering and being revived, a reconnecting client listed once), and real WebSocket clients against an `httptest` server. `TestTwoClientsTypingConcurrentlyConverge` runs two such clients editing at once, each sending as soon as its last batch is confirmed or rejected, and checks that they and the server end with identical text and that no edit is lost or duplicated.

The lifecycle tests cover deleting an entry in a room (every client closed with 4010, nothing saved afterwards, the room gone, a later join finding no entry, an empty lingering room ended), the delete endpoint end to end over a real websocket, the size cap (landing exactly on it counts UTF-16 units and is accepted, one unit over is refused and the document is untouched, an entry already over the cap opens and can shrink, and an update at the cap in the worst-case JSON encoding passes the read limit), and shutdown (edits typed since the last save, in an occupied and in an empty room, survive a restart against a file-backed database; joins during shutdown are told to come back).

### Unit tests (Vitest)

`npm test` runs `web/src/stepLog.ts` (the log that carries a cursor from the version its owner reported to the version you have), the reconnect backoff, and which close codes are worth retrying.

### Browser tests (Playwright)

The specs live in `web/e2e/` and need Chromium once (`npx playwright install chromium`). `npm run e2e` starts its own Go server (throwaway database, port 8181) and Vite (port 5273), so it can run while `make dev` is up. Races have shown up about one run in ten, so after touching anything concurrent run `npm run e2e -- --repeat-each=10`.

- `entries.spec.ts` walks the list UI: create an entry, type a heading, see it listed under its derived title, reopen it, decline and then accept the delete confirmation, and see the entry's address report that it no longer exists. It also deletes an entry that another client already deleted and checks that is not reported as an error.
- `convergence.spec.ts` opens two browser contexts on one entry, types into both at the same time, and checks that both editors, the preview and a third, fresh browser show the same text, with each writer's keystrokes complete and in order.
- `presence.spec.ts` covers the name prompt, the viewer list (including leaving when a page closes), and remote cursors: where they are drawn, how they follow edits from either side, and that a late joiner sees existing cursors.
- `lifecycle.spec.ts` deletes an entry (through the REST API) while two pages have it open and checks both land on the list with a notice, and that a paste past the size cap is refused, leaves the other viewer and the saved text untouched, and is not retried.
- `reconnect.spec.ts` severs a page's socket with `page.routeWebSocket` while it and another page keep typing, and checks that nothing is lost and both converge; that a lone client resumes too; that when the history cannot cover the gap the editor reloads and reports the dropped edits; and that a failed re-check of the entry (the page refetches it on focus) leaves the open editor in place.
