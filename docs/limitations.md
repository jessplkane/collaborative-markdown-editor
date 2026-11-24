# Limitations and out of scope

## Known limitations

- A lone UTF-16 surrogate in an edit (half of an emoji sent on its own) becomes U+FFFD when the server decodes the JSON. Positions stay aligned, but that character's content differs between that client and the server. Full characters, including emoji, are handled correctly.
- Entries have no owner or access control: anyone with the URL can read and edit it. In particular, `clientID`s are visible in the viewer list, so anyone can close another viewer's connection by claiming its `clientID`.
- Reconnecting depends on the client noticing the connection is gone, which takes up to 15 seconds when the network dies without closing the socket (see [Reconnect and resume](protocol.md#reconnect-and-resume)). Edits typed in that window are kept, not lost.
- A hard kill (`SIGKILL`, a crash, power loss) still loses edits from roughly the last second: only graceful shutdown flushes. A server restart also starts new room instances, so a client reconnecting afterwards gets a snapshot and loses edits it had not yet had confirmed (it is told so).

## Follow-ups / out of scope

- **Live list updates and viewer counts.** The entries list is fetched when the page opens. It does not update when someone else creates, edits or deletes an entry, and does not show who has an entry open.
- **Remote selections.** Other viewers' cursors are shown, but not their selected ranges.
- **Renaming.** A viewer's name can only change by clearing `localStorage`.
- **WYSIWYG.** The editor is plain markdown beside a preview.
- **Auth and ownership.** There are no accounts, so there is no private entry and no way to tell who deleted one.
- **More than one server instance.** Rooms live in one process's memory, so two instances would split an entry's editors into separate rooms that overwrite each other's saves. Scaling out needs every connection for an entry routed to the same instance (consistent routing on the entry id), or a shared layer between instances for sequencing and fan-out.
