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
  StateEffect,
} from '@codemirror/state'
import { type EditorView, ViewPlugin, type ViewUpdate } from '@codemirror/view'
import type {
  ClientMessage,
  ServerMessage,
  WireChange,
} from './protocol.ts'
import { createStepLog } from './stepLog.ts'

/**
 * Marks the transaction that applies another client's updates with that
 * client's id, so a viewer can tell whose edit moved things.
 */
export const updateAuthor = StateEffect.define<string>()

export interface CollabOptions {
  clientID: string
  /** Server version of the snapshot the editor was created from. */
  version: number
  /** Length (UTF-16 units) of the snapshot document. */
  docLength: number
  send: (message: ClientMessage) => void
  /** The server's updates could not be applied in order; the editor can no
   * longer be trusted to match the server. */
  onDesync: (reason: string) => void
}

export interface CollabClient {
  extension: Extension
  /** Call once, right after creating the EditorView with `extension`. */
  attach: (view: EditorView) => void
  /** Handle an update or rejection from the server. */
  handle: (
    message: Extract<ServerMessage, { type: 'updates' | 'reject' }>,
  ) => void
  /**
   * Turns a position in the editor into one the server's document can
   * interpret: the position as it was at the last synced version, ignoring
   * edits the server has not confirmed yet.
   */
  toSyncedPos: (pos: number) => { version: number; pos: number }
  /**
   * The reverse, for positions that arrive from other clients: a position of
   * client `owner` in server document `version` becomes a position in the
   * editor, carried through the updates since then and through our
   * unconfirmed edits. Undefined if that version is no longer remembered.
   */
  toEditorPos: (version: number, pos: number, owner: string) => number | undefined
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
  const history = createStepLog(opts.version)

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

  function handle(
    message: Extract<ServerMessage, { type: 'updates' | 'reject' }>,
  ) {
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
    for (const u of updates) history.push(u.changes, message.clientID)

    if (message.clientID === opts.clientID) inFlight = false
    const spec = receiveUpdates(state, updates)
    view.dispatch({
      ...spec,
      effects: [spec.effects ?? [], updateAuthor.of(message.clientID)].flat(),
    })
    push(view.state)
  }

  function toSyncedPos(pos: number) {
    const state = view!.state
    const unconfirmed = sendableUpdates(state)
    let synced = pos
    for (let i = unconfirmed.length - 1; i >= 0; i--) {
      synced = unconfirmed[i].changes.desc.invertedDesc.mapPos(synced, -1)
    }
    return { version: getSyncedVersion(state), pos: synced }
  }

  function toEditorPos(version: number, pos: number, owner: string) {
    const state = view!.state
    let mapped = history.mapToNewest(version, pos, owner)
    if (mapped === undefined) return undefined
    for (const u of sendableUpdates(state)) mapped = u.changes.mapPos(mapped, -1)
    return Math.min(mapped, state.doc.length)
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
    toSyncedPos,
    toEditorPos,
  }
}

function toWire(changes: ChangeSet): WireChange[] {
  const out: WireChange[] = []
  changes.iterChanges((fromA, toA, _fromB, _toB, inserted) => {
    out.push({ from: fromA, to: toA, insert: inserted.toString() })
  })
  return out
}
