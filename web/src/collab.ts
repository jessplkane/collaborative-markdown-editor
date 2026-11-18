import {
  collab,
  getSyncedVersion,
  receiveUpdates,
  sendableUpdates,
} from '@codemirror/collab'
import {
  ChangeSet,
  type EditorState,
  type Extension,
} from '@codemirror/state'
import { type EditorView, ViewPlugin, type ViewUpdate } from '@codemirror/view'
import type { ServerMessage, UpdateMessage, WireChange } from './protocol.ts'

export interface CollabOptions {
  clientID: string
  /** Server version of the snapshot the editor was created from. */
  version: number
  /** Length (UTF-16 units) of the snapshot document. */
  docLength: number
  send: (message: UpdateMessage) => void
  /** The server's updates could not be applied in order; the editor can no
   * longer be trusted to match the server. */
  onDesync: (reason: string) => void
}

export interface CollabClient {
  extension: Extension
  /** Call once, right after creating the EditorView with `extension`. */
  attach: (view: EditorView) => void
  /** Handle a server message other than the snapshot. */
  handle: (message: Exclude<ServerMessage, { type: 'snapshot' }>) => void
}

/**
 * Client half of the sync protocol, built on @codemirror/collab: the server is
 * the only authority, so local edits stay "unconfirmed" until it broadcasts
 * them back, and edits that arrive first are rebased under them.
 *
 * At most one batch is in flight. It is either confirmed (the server
 * broadcasts it back tagged with our clientID) or rejected as stale. The server
 * sends everything that made it stale before the rejection, so by the time a
 * rejection arrives the unconfirmed edits are already rebased and can be sent
 * again.
 */
export function createCollab(opts: CollabOptions): CollabClient {
  let view: EditorView | undefined
  let inFlight = false
  // Length of the document as the server has it, i.e. without our unconfirmed
  // edits. Wire changes carry no length, so it is tracked here to build
  // ChangeSets for incoming steps.
  let syncedLength = opts.docLength

  function push(state: EditorState) {
    if (inFlight) return
    const updates = sendableUpdates(state)
    if (updates.length === 0) return
    inFlight = true
    opts.send({
      type: 'update',
      clientID: opts.clientID,
      version: getSyncedVersion(state),
      updates: updates.map((u) => ({ changes: toWire(u.changes) })),
    })
  }

  const plugin = ViewPlugin.fromClass(
    class {
      update(u: ViewUpdate) {
        if (u.docChanged) push(u.state)
      }
    },
  )

  function handle(message: Exclude<ServerMessage, { type: 'snapshot' }>) {
    if (!view) return

    if (message.type === 'reject') {
      inFlight = false
      push(view.state)
      return
    }

    const state = view.state
    const expected = getSyncedVersion(state)
    if (message.version !== expected) {
      opts.onDesync(`expected updates at version ${expected}, got ${message.version}`)
      return
    }

    let updates
    try {
      updates = message.updates.map((step) => {
        const changes = ChangeSet.of(step.changes, syncedLength)
        syncedLength = changes.newLength
        return { changes, clientID: message.clientID }
      })
    } catch (err) {
      opts.onDesync(`unusable update: ${String(err)}`)
      return
    }

    if (message.clientID === opts.clientID) inFlight = false
    view.dispatch(receiveUpdates(state, updates))
    push(view.state)
  }

  return {
    extension: [
      collab({ startVersion: opts.version, clientID: opts.clientID }),
      plugin,
    ],
    attach: (v) => {
      view = v
    },
    handle,
  }
}

function toWire(changes: ChangeSet): WireChange[] {
  const out: WireChange[] = []
  changes.iterChanges((fromA, toA, _fromB, _toB, inserted) => {
    out.push({ from: fromA, to: toA, insert: inserted.toString() })
  })
  return out
}
