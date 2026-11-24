# Protocol

The sync layer is JSON text frames over a WebSocket at `/ws/entries/{id}`. The same definitions live in the doc comment in `server/room.go` and in `web/src/protocol.ts`; keep the three in step. For how rooms and sequencing work, see [architecture.md](architecture.md).

## Sync messages

| Direction | Message | Meaning |
|---|---|---|
| server → client | `{"type":"snapshot","doc":"…","version":N,"epoch":"…"}` | Sent first on connect, unless a resume succeeded. |
| server → client | `{"type":"resumed","version":N}` | Sent first when a resume succeeded, after the missed `updates`. The client is caught up to version N. |
| client → server | `{"type":"update","clientID":"…","version":N,"updates":[{"changes":[{"from":0,"to":0,"insert":"hi"}]}]}` | Steps made on top of version N. Each step is one version; positions in a step refer to the document before that step. |
| server → client | `{"type":"updates","version":N,"clientID":"…","updates":[…]}` | An accepted batch, based on version N. A matching `clientID` tells the author it is confirmed. |
| server → client | `{"type":"reject","version":N}` | The batch was stale. N is the current version. |
| client → server | `{"type":"ping"}` | Liveness probe, answered with `{"type":"pong"}`. See [Reconnect and resume](#reconnect-and-resume). |

## Presence messages

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

## Close codes

A malformed message or an out-of-range update closes the connection with code 1007 or 1008, and so does sending messages faster than the per-connection limit (a token bucket refilled at 100 messages a second, holding 200).

| Code | Meaning | Client reconnects? |
|---|---|---|
| 1001 | The server is shutting down. | yes |
| 1007, 1008 | Malformed message, out-of-range update, or rate limit exceeded. | no |
| 1009 | Message too big. | no |
| 1013 | The client was too slow; it may try again. | yes |
| 4004 | The entry does not exist. | no |
| 4009 | The same client opened a newer connection. | no |
| 4010 | The entry was deleted. | no (leaves the page) |
| 4013 | The update would have made the document too large. | no |

The "no" rows freeze the editor read-only with a message, or, for 4010, navigate to the list. They are listed in `isTerminalClose` in `web/src/protocol.ts`; any other close reconnects.

## Reconnect and resume

When the socket closes for any reason but the terminal ones above, the editor shows "Reconnecting…", stays editable, and retries with exponential backoff (0.5 s doubling to 10 s, randomised) until it is back. Nothing is buffered on the wire: edits typed meanwhile stay in the editor as unconfirmed edits, the same as when the server has not yet acknowledged them.

**Noticing a dead connection.** A network that dies without closing the socket (or a dev proxy whose far end died) leaves a WebSocket looking open, and browsers do not expose protocol-level pings. So the client sends `{"type":"ping"}` every 5 seconds and treats any message from the server as proof of life; after 15 seconds of silence it abandons the socket and reconnects (`web/src/heartbeat.ts`).

**Resuming.** The new socket's URL carries `?epoch=E&version=N`: the room instance and document version the client had last caught up to. The room keeps its accepted batches (`server/history.go`, bounded by estimated size, 4 MiB by default) and, if `E` is its epoch and the history reaches `N`, replays the missed batches as ordinary `updates` messages, each with its original `clientID`, then sends `resumed`. The client then sends whatever is still unconfirmed.

- **A lost acknowledgement does not double-apply.** If the server applied the client's batch but the connection died before the client saw it, the replay contains that batch tagged with the client's own `clientID`, which the client treats as the confirmation. The `clientID` is kept across reconnects for this reason. A batch the server never got is not in the replay and is sent again after `resumed`.
- **Versions only mean something within one room instance.** A room's version starts at 0 whenever the room is created, so a version number alone could match a different history after a restart. Each room instance has a random `epoch`, sent in the snapshot and echoed by the client; a mismatch forces a snapshot.
- **Snapshot fallback.** If the epoch differs (server restarted, or the room was gone), the history no longer reaches the client's version, or the gap is bigger than the client's send buffer (256 messages), the server sends a snapshot instead. The editor replaces its document and drops the edits it had not yet had confirmed, and says so in a notice.
- **Linger.** An empty room stays for 30 seconds, so that the only person in an entry can drop and resume instead of always falling back to a snapshot.
- **One viewer per client.** When a client says hello, the room closes any other connection with the same `clientID` (code 4009); the server may not have noticed yet that the old one is dead.
