import {
  type Extension,
  StateEffect,
  StateField,
  Transaction,
} from '@codemirror/state'
import {
  Decoration,
  EditorView,
  ViewPlugin,
  type ViewUpdate,
  WidgetType,
} from '@codemirror/view'
import { type CollabClient, updateAuthor } from './collab.ts'
import { colorFor } from './identity.ts'
import type { ClientMessage, ServerMessage, Viewer } from './protocol.ts'

/** How long to wait after the selection last changed before reporting it. */
const CURSOR_THROTTLE_MS = 75

export interface PresenceOptions {
  clientID: string
  userID: string
  name: string
  collab: Pick<CollabClient, 'toSyncedPos' | 'toEditorPos'>
  send: (message: ClientMessage) => void
  /** The set of viewers changed; the list includes this client. */
  onViewers: (viewers: Viewer[]) => void
}

export interface Presence {
  extension: Extension
  /** Call once, right after creating the EditorView with `extension`. */
  attach: (view: EditorView) => void
  /** Announce this client. Must come before any cursor is sent. */
  hello: () => void
  handle: (message: Extract<ServerMessage, { type: 'viewers' | 'cursor' }>) => void
  destroy: () => void
}

interface Remote {
  name: string
  color: string
}

// Who is in the room, and where their cursors are, in editor positions.
// Positions follow the document through every change, so a cursor stays put
// relative to the text around it between its owner's reports.
interface CursorState {
  viewers: Map<string, Remote>
  cursors: Map<string, number>
}

const setViewers = StateEffect.define<Map<string, Remote>>()
const setCursor = StateEffect.define<{ clientID: string; pos: number }>()

class CursorWidget extends WidgetType {
  readonly name: string
  readonly color: string

  constructor(name: string, color: string) {
    super()
    this.name = name
    this.color = color
  }

  eq(other: CursorWidget) {
    return other.name === this.name && other.color === this.color
  }

  toDOM() {
    const el = document.createElement('span')
    el.className = 'remote-cursor'
    el.dataset.name = this.name
    el.style.setProperty('--cursor-color', this.color)
    el.setAttribute('aria-hidden', 'true')
    return el
  }

  ignoreEvent() {
    return true
  }
}

const cursorField = StateField.define<CursorState>({
  create: () => ({ viewers: new Map(), cursors: new Map() }),

  update(value, tr) {
    let { viewers, cursors } = value

    if (tr.docChanged) {
      // Text typed by a cursor's owner lands at that cursor and should push
      // it along; anyone else's text at the same spot should not.
      let author: string | undefined
      for (const e of tr.effects) if (e.is(updateAuthor)) author = e.value
      cursors = new Map(
        [...cursors].map(([id, pos]) => [
          id,
          tr.changes.mapPos(pos, id === author ? 1 : -1),
        ]),
      )
    }

    for (const e of tr.effects) {
      if (e.is(setViewers)) {
        viewers = e.value
        cursors = new Map([...cursors].filter(([id]) => viewers.has(id)))
      } else if (e.is(setCursor) && viewers.has(e.value.clientID)) {
        cursors = new Map(cursors).set(e.value.clientID, e.value.pos)
      }
    }

    return viewers === value.viewers && cursors === value.cursors
      ? value
      : { viewers, cursors }
  },

  provide: (field) =>
    EditorView.decorations.compute([field], (state) => {
      const { viewers, cursors } = state.field(field)
      const widgets = []
      for (const [id, pos] of cursors) {
        const who = viewers.get(id)
        if (!who) continue
        widgets.push(
          Decoration.widget({
            widget: new CursorWidget(who.name, who.color),
            side: 1,
          }).range(pos),
        )
      }
      return Decoration.set(widgets, true)
    }),
})

/**
 * Who is here and where they are looking. The server relays presence without
 * interpreting it; a cursor arrives in terms of the document version its
 * owner had, and is carried to ours with the step log kept by collab.
 */
export function createPresence(opts: PresenceOptions): Presence {
  let view: EditorView | undefined
  let helloSent = false
  let timer: number | undefined
  let lastSent: { version: number; pos: number } | undefined
  let known = new Set<string>()

  function sendCursor() {
    timer = undefined
    if (!view || !helloSent) return
    const where = opts.collab.toSyncedPos(view.state.selection.main.head)
    if (lastSent?.version === where.version && lastSent.pos === where.pos) return
    lastSent = where
    opts.send({ type: 'cursor', ...where })
  }

  function scheduleCursor() {
    timer ??= window.setTimeout(sendCursor, CURSOR_THROTTLE_MS)
  }

  const plugin = ViewPlugin.fromClass(
    class {
      update(u: ViewUpdate) {
        // A remote edit moves the selection along but is not news: viewers
        // carry our cursor through it themselves.
        const localEdit = u.transactions.some(
          (tr) => tr.docChanged && !tr.annotation(Transaction.remote),
        )
        if (u.selectionSet || localEdit) scheduleCursor()
      }
    },
  )

  function handle(message: Extract<ServerMessage, { type: 'viewers' | 'cursor' }>) {
    if (!view) return

    if (message.type === 'cursor') {
      if (message.clientID === opts.clientID) return
      const pos = opts.collab.toEditorPos(
        message.version,
        message.pos,
        message.clientID,
      )
      if (pos === undefined) return // too old to place; the next report will do
      view.dispatch({ effects: setCursor.of({ clientID: message.clientID, pos }) })
      return
    }

    const viewers = new Map<string, Remote>()
    for (const v of message.viewers) {
      viewers.set(v.clientID, {
        name: v.name || 'Anonymous',
        color: colorFor(v.userID),
      })
    }
    view.dispatch({ effects: setViewers.of(viewers) })
    opts.onViewers(message.viewers)

    // Someone new does not know where we are, and cursors are otherwise only
    // sent when they move.
    const next = new Set(viewers.keys())
    const arrived = [...next].some((id) => id !== opts.clientID && !known.has(id))
    known = next
    if (arrived) {
      lastSent = undefined
      sendCursor()
    }
  }

  return {
    extension: [cursorField, plugin],
    attach: (v) => {
      view = v
    },
    hello: () => {
      opts.send({
        type: 'hello',
        clientID: opts.clientID,
        userID: opts.userID,
        name: opts.name,
      })
      helloSent = true
      scheduleCursor()
    },
    handle,
    destroy: () => window.clearTimeout(timer),
  }
}
