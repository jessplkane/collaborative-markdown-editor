import { markdown } from '@codemirror/lang-markdown'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useDeferredValue, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { backoffDelay } from './backoff.ts'
import { type CollabClient, createCollab } from './collab.ts'
import { type Heartbeat, startHeartbeat } from './heartbeat.ts'
import { wsUrl } from './config.ts'
import { colorFor, getUserID } from './identity.ts'
import { type Presence, createPresence } from './presence.ts'
import {
  CLOSE_DELETED,
  CLOSE_NOT_FOUND,
  CLOSE_TOO_LARGE,
  type ClientMessage,
  type ServerMessage,
  type Viewer,
  isTerminalClose,
} from './protocol.ts'

type Status =
  | 'connecting'
  | 'live'
  | 'reconnecting'
  | 'closed'
  | 'desync'
  | 'notfound'
  | 'deleted'
  | 'toolarge'

const statusText: Record<Status, string> = {
  connecting: 'Connecting…',
  live: 'Connected',
  reconnecting: 'Connection lost. Reconnecting… Your edits are kept.',
  closed: 'Disconnected. Reload the page to reconnect.',
  desync: 'Out of sync with the server. Reload the page.',
  notfound: 'This entry no longer exists.',
  deleted: 'This entry was deleted.',
  toolarge:
    'This entry has reached its size limit and your last change was not saved. Reload the page to see the saved text.',
}

const droppedEditsNotice =
  'While you were disconnected the server lost track of this entry’s ' +
  'history, so the document was reloaded from the server and the edits you ' +
  'had not sent yet were dropped.'

export function EntryEditor({
  entryId,
  name,
  onDeleted,
}: {
  entryId: string
  /** The name other viewers see for this user. */
  name: string
  /** The entry was deleted while it was open. */
  onDeleted: () => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [notice, setNotice] = useState<string | null>(null)
  const [text, setText] = useState('')
  const [viewers, setViewers] = useState<Viewer[]>([])
  const [selfID, setSelfID] = useState('')
  // Rendering markdown is the expensive part of a keystroke; let typing win.
  const preview = useDeferredValue(text)
  // Read from inside the connection's handlers, which outlive a render.
  const onDeletedRef = useRef(onDeleted)
  useEffect(() => {
    onDeletedRef.current = onDeleted
  })

  useEffect(() => {
    const mount = host.current
    if (!mount) return
    // Inferred as non-null, which function declarations below can rely on;
    // they would not see the narrowing of `mount` itself.
    const parent = mount

    // Stays the same across reconnects: the server recognises our own updates
    // in a replay by it, and lists us once.
    const clientID = crypto.randomUUID()
    const readOnly = new Compartment()
    let view: EditorView | undefined
    let client: CollabClient | undefined
    let presence: Presence | undefined
    // Identifies the server's room instance the document version belongs to.
    let epoch = ''
    let disposed = false
    // Set once a close or desync means reconnecting is pointless.
    let ended = false
    let ws: WebSocket | undefined
    // Whether this connection has had its snapshot or been resumed.
    let handshaken = false
    let attempt = 0
    let retry: number | undefined
    let heartbeat: Heartbeat | undefined

    setStatus('connecting')
    setNotice(null)
    setText('')
    setViewers([])
    setSelfID(clientID)

    function send(message: ClientMessage) {
      // Sending on a socket that is still connecting throws, and on one that
      // is closing loses the data. Edits are not buffered here: collab is
      // paused meanwhile and sends what is unconfirmed once resumed.
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(message))
    }

    function freeze(next: Status) {
      ended = true
      heartbeat?.stop()
      setStatus(next)
      setViewers([])
      view?.dispatch({
        effects: readOnly.reconfigure([
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
        ]),
      })
    }

    function desync(reason: string) {
      console.error('collab desync:', reason)
      freeze('desync')
      ws?.close()
    }

    // Builds the editor and the sync state from a snapshot. On a reconnect
    // that could not be resumed this replaces the document and drops edits
    // the server never confirmed; those are reported.
    function install(snapshot: Extract<ServerMessage, { type: 'snapshot' }>) {
      const dropped = client?.hasUnconfirmed() ?? false
      presence?.destroy()
      epoch = snapshot.epoch

      const nextClient = createCollab({
        clientID,
        version: snapshot.version,
        docLength: snapshot.doc.length,
        send,
        onDesync: desync,
      })
      const nextPresence = createPresence({
        clientID,
        userID: getUserID(),
        name,
        collab: nextClient,
        send,
        onViewers: setViewers,
      })
      client = nextClient
      presence = nextPresence

      const state = EditorState.create({
        doc: snapshot.doc,
        extensions: [
          basicSetup,
          markdown(),
          EditorView.lineWrapping,
          readOnly.of([]),
          nextClient.extension,
          nextPresence.extension,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) setText(u.state.doc.toString())
          }),
        ],
      })
      const first = !view
      if (view) view.setState(state)
      else view = new EditorView({ parent, state })
      nextClient.attach(view)
      nextPresence.attach(view)
      nextPresence.hello()

      setText(snapshot.doc)
      setStatus('live')
      if (dropped) setNotice(droppedEditsNotice)
      if (first) view.focus()
    }

    function onMessage(event: MessageEvent) {
      const message = JSON.parse(String(event.data)) as ServerMessage

      switch (message.type) {
        case 'viewers':
        case 'cursor':
          presence?.handle(message)
          return
        case 'snapshot':
          if (handshaken) return desync('second snapshot on one connection')
          handshaken = true
          attempt = 0
          install(message)
          return
        case 'resumed':
          if (handshaken || !client || !presence) {
            return desync('unexpected resumed message')
          }
          handshaken = true
          attempt = 0
          client.resume(message.version)
          if (ended) return // resume found we are not where the server says
          presence.hello()
          setStatus('live')
          return
        case 'pong':
          return
        default:
          client?.handle(message)
      }
    }

    function onClose(event: CloseEvent) {
      if (disposed || ended) return
      if (event.code === CLOSE_DELETED) {
        freeze('deleted')
        onDeletedRef.current()
        return
      }
      if (isTerminalClose(event.code)) {
        const terminal: Record<number, Status> = {
          [CLOSE_NOT_FOUND]: 'notfound',
          [CLOSE_TOO_LARGE]: 'toolarge',
        }
        freeze(terminal[event.code] ?? 'closed')
        return
      }
      connectionLost()
    }

    function connectionLost() {
      heartbeat?.stop()
      // The editor stays editable: what is typed now is sent once resumed.
      client?.pause()
      presence?.reset()
      setStatus('reconnecting')
      retry = window.setTimeout(connect, backoffDelay(attempt++))
    }

    function connect() {
      handshaken = false
      // With a document already on screen, ask to continue from where we
      // left off instead of starting over.
      const resume =
        view && client
          ? `?${new URLSearchParams({ epoch, version: String(client.version()) })}`
          : ''
      const socket = new WebSocket(
        wsUrl(`/ws/entries/${encodeURIComponent(entryId)}${resume}`),
      )
      ws = socket
      heartbeat = startHeartbeat({
        ping: () => send({ type: 'ping' }),
        onDead: () => {
          // The close handshake with a peer that has gone quiet may never
          // finish, so stop listening to this socket instead of waiting for it.
          socket.onmessage = null
          socket.onclose = null
          socket.close()
          connectionLost()
        },
      })
      // An old socket can still deliver events after a new one replaced it.
      socket.onmessage = (event) => {
        if (socket !== ws || disposed) return
        heartbeat?.heard()
        onMessage(event)
      }
      socket.onclose = (event) => {
        if (socket === ws) onClose(event)
      }
    }

    // Opened on the next tick, not now. In development React mounts, unmounts
    // and remounts every effect straight away (StrictMode), and a socket
    // closed before its handshake finishes is logged by the Vite proxy as a
    // connection reset. The cleanup cancels the first, unwanted attempt.
    retry = window.setTimeout(connect)

    return () => {
      window.clearTimeout(retry)
      heartbeat?.stop()
      disposed = true
      presence?.destroy()
      ws?.close()
      view?.destroy()
    }
  }, [entryId, name])

  return (
    <div className="editor-page">
      <p className="status" data-status={status} role="status">
        {statusText[status]}
      </p>
      {notice && (
        <div className="notice" role="alert">
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </div>
      )}
      <ul className="viewers" aria-label="Viewers">
        {viewers.map((v) => (
          <li key={v.clientID}>
            <span
              className="viewer-dot"
              style={{ background: colorFor(v.userID) }}
              aria-hidden="true"
            />
            {v.name || 'Anonymous'}
            {v.clientID === selfID && ' (you)'}
          </li>
        ))}
      </ul>
      <div className="panes">
        <div ref={host} className="pane editor" aria-label="Markdown editor" />
        <div className="pane preview" aria-label="Preview">
          <Markdown remarkPlugins={[remarkGfm]}>{preview}</Markdown>
        </div>
      </div>
    </div>
  )
}
