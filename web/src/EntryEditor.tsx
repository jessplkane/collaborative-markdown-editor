import { markdown } from '@codemirror/lang-markdown'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useDeferredValue, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { type CollabClient, createCollab } from './collab.ts'
import { wsUrl } from './config.ts'
import { colorFor, getUserID } from './identity.ts'
import { type Presence, createPresence } from './presence.ts'
import {
  CLOSE_NOT_FOUND,
  type ClientMessage,
  type ServerMessage,
  type Viewer,
} from './protocol.ts'

type Status = 'connecting' | 'live' | 'closed' | 'desync' | 'notfound'

const statusText: Record<Status, string> = {
  connecting: 'Connecting…',
  live: 'Connected',
  closed: 'Disconnected. Reload the page to reconnect.',
  desync: 'Out of sync with the server. Reload the page.',
  notfound: 'This entry no longer exists.',
}

export function EntryEditor({
  entryId,
  name,
}: {
  entryId: string
  /** The name other viewers see for this user. */
  name: string
}) {
  const host = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [text, setText] = useState('')
  const [viewers, setViewers] = useState<Viewer[]>([])
  const [selfID, setSelfID] = useState('')
  // Rendering markdown is the expensive part of a keystroke; let typing win.
  const preview = useDeferredValue(text)

  useEffect(() => {
    const mount = host.current
    if (!mount) return
    // Inferred as non-null, which function declarations below can rely on;
    // they would not see the narrowing of `mount` itself.
    const parent = mount

    const clientID = crypto.randomUUID()
    const readOnly = new Compartment()
    let view: EditorView | undefined
    let client: CollabClient | undefined
    let presence: Presence | undefined
    let disposed = false

    setStatus('connecting')
    setText('')
    setViewers([])
    setSelfID(clientID)

    let ws: WebSocket | undefined

    function send(message: ClientMessage) {
      ws?.send(JSON.stringify(message))
    }

    function freeze(next: Status) {
      setStatus(next)
      setViewers([])
      view?.dispatch({
        effects: readOnly.reconfigure([
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
        ]),
      })
    }

    function onMessage(event: MessageEvent) {
      if (disposed) return
      const message = JSON.parse(String(event.data)) as ServerMessage

      if (message.type === 'viewers' || message.type === 'cursor') {
        presence?.handle(message)
        return
      }
      if (message.type !== 'snapshot') {
        client?.handle(message)
        return
      }
      if (view) {
        // The server sends one snapshot per connection.
        freeze('desync')
        ws?.close()
        return
      }

      client = createCollab({
        clientID,
        version: message.version,
        docLength: message.doc.length,
        send,
        onDesync: (reason) => {
          console.error('collab desync:', reason)
          freeze('desync')
          ws?.close()
        },
      })
      presence = createPresence({
        clientID,
        userID: getUserID(),
        name,
        collab: client,
        send,
        onViewers: setViewers,
      })
      view = new EditorView({
        parent,
        state: EditorState.create({
          doc: message.doc,
          extensions: [
            basicSetup,
            markdown(),
            EditorView.lineWrapping,
            readOnly.of([]),
            client.extension,
            presence.extension,
            EditorView.updateListener.of((u) => {
              if (u.docChanged) setText(u.state.doc.toString())
            }),
          ],
        }),
      })
      client.attach(view)
      presence.attach(view)
      presence.hello()
      setText(message.doc)
      setStatus('live')
      view.focus()
    }

    function onClose(event: CloseEvent) {
      if (disposed) return
      freeze(event.code === CLOSE_NOT_FOUND ? 'notfound' : 'closed')
    }

    // Opened on the next tick, not now. In development React mounts, unmounts
    // and remounts every effect straight away (StrictMode), and a socket
    // closed before its handshake finishes is logged by the Vite proxy as a
    // connection reset. The cleanup cancels the first, unwanted attempt.
    const connecting = window.setTimeout(() => {
      ws = new WebSocket(wsUrl(`/ws/entries/${encodeURIComponent(entryId)}`))
      ws.onmessage = onMessage
      ws.onclose = onClose
    })

    return () => {
      window.clearTimeout(connecting)
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
