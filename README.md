# markdown-editor

A real-time collaborative markdown editor. Multiple people can edit the same entry at once and see each other's changes as they type. The sync layer is a WebSocket protocol written by hand in Go; the frontend is React with CodeMirror and a live preview.

This is a portfolio project built to learn WebSockets, so the interesting parts are the room and sequencing logic in `server/`, not the UI.

## Features

- Entries list with create and delete, backed by SQLite.
- CodeMirror editor beside a rendered markdown preview; the title comes from the first line or heading.
- Live editing over a WebSocket: several browsers converge on the same text.
- Names, a viewer list and remote cursors.
- Reconnect and resume with backoff; edits typed while offline are kept.
- Deleting an entry sends everyone viewing it back to the list.
- Document size cap, and unsaved edits are flushed on graceful shutdown.

## How it works

```
browser (React, CodeMirror)  <-- REST /api/* -->  Go server  <-->  SQLite
                             <-- WebSocket -->    (one room per open entry)
                                /ws/entries/{id}
```

Each open entry has a *room*: one goroutine that owns the document and applies every edit in a single order. Clients apply keystrokes optimistically and tag each batch with the version it was made on; the server accepts it only if that version is current, otherwise it rejects it and the client rebases (`@codemirror/collab`). All transformation happens in the browser.

## Quick start

Requires Go 1.27+ and Node 22.12+ (or 20.19+).

```sh
cd web && npm install && cd ..
make dev
```

Open http://localhost:5173.

## Documentation

| Doc | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Rooms, sequencing, persistence, deletion, size cap, shutdown, code layout |
| [docs/protocol.md](docs/protocol.md) | WebSocket messages, presence, close codes, reconnect and resume |
| [docs/development.md](docs/development.md) | Running locally, configuration variables, Go, Vitest and Playwright tests |
| [docs/deploying.md](docs/deploying.md) | Netlify frontend and Fly.io server |
| [docs/limitations.md](docs/limitations.md) | Known limitations and what is out of scope |
| [AGENTS.md](AGENTS.md) | Conventions for contributors and coding agents |
