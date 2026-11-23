# AGENTS.md

Collaborative markdown editor: Go server in `server/`, React + Vite + TypeScript app in `web/`.

`README.md` holds the architecture, wire protocol, reconnect/resume design and the run and test commands. Read its "How it works" section before changing rooms, sync or reconnect, and its protocol sections before changing any message or close code.

## Changing the protocol

Update together: the doc comment in `server/room.go`, `web/src/protocol.ts`, and the README tables. A new close code also goes in `ws.go` and, if reconnecting cannot help, in `isTerminalClose` (`protocol.ts`).

## Conventions

Nothing enforces these; the code follows them.

- TypeScript: named helpers are `function` declarations, arrows are for inline callbacks. Factories are `createX(...)` returning an object. Classes only where an API demands one (CodeMirror `WidgetType`, `ViewPlugin.fromClass`).
- Read JSON into a typed variable (`const entry: { id: string } = await res.json()`) and use its members.
- Comments explain why.
- Tests assert behaviour and invariants and stay deterministic. Prefer a `Hub` timing field (see the `Hub` struct) over a sleep, and add new knobs the same way.

## Verifying

After touching anything concurrent, run `npm run e2e -- --repeat-each=10` from `web/`: races have shown up about one run in ten.

## Workflow

The owner commits. Leave changes uncommitted, and leave other uncommitted work in the tree intact (no `git checkout` or `reset` on a file that has any).
