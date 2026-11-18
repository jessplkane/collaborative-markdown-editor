import { markdown } from '@codemirror/lang-markdown'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useDeferredValue, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { type CollabClient, createCollab } from './collab.ts'
import { wsUrl } from './config.ts'
import { CLOSE_NOT_FOUND, type ServerMessage } from './protocol.ts'

type Status = 'connecting' | 'live' | 'closed' | 'desync' | 'notfound'

const statusText: Record<Status, string> = {
  connecting: 'Connecting…',
  live: 'Connected',
  closed: 'Disconnected. Reload the page to reconnect.',
  desync: 'Out of sync with the server. Reload the page.',
  notfound: 'This entry no longer exists.',
}

export function EntryEditor({ entryId }: { entryId: string }) {
  const host = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [text, setText] = useState('')
  // Rendering markdown is the expensive part of a keystroke; let typing win.
  const preview = useDeferredValue(text)

  useEffect(() => {
    const parent = host.current
    if (!parent) return

    const clientID = crypto.randomUUID()
    const readOnly = new Compartment()
    let view: EditorView | undefined
    let client: CollabClient | undefined
    let disposed = false

    setStatus('connecting')
    setText('')

    const ws = new WebSocket(wsUrl(`/ws/entries/${encodeURIComponent(entryId)}`))

    function freeze(next: Status) {
      setStatus(next)
      view?.dispatch({
        effects: readOnly.reconfigure([
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
        ]),
      })
    }

    ws.onmessage = (event) => {
      if (disposed) return
      const message = JSON.parse(String(event.data)) as ServerMessage

      if (message.type !== 'snapshot') {
        client?.handle(message)
        return
      }
      if (view) {
        // The server sends one snapshot per connection.
        freeze('desync')
        ws.close()
        return
      }

      client = createCollab({
        clientID,
        version: message.version,
        docLength: message.doc.length,
        send: (m) => ws.send(JSON.stringify(m)),
        onDesync: (reason) => {
          console.error('collab desync:', reason)
          freeze('desync')
          ws.close()
        },
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
            EditorView.updateListener.of((u) => {
              if (u.docChanged) setText(u.state.doc.toString())
            }),
          ],
        }),
      })
      client.attach(view)
      setText(message.doc)
      setStatus('live')
      view.focus()
    }

    ws.onclose = (event) => {
      if (disposed) return
      freeze(event.code === CLOSE_NOT_FOUND ? 'notfound' : 'closed')
    }

    return () => {
      disposed = true
      ws.close()
      view?.destroy()
    }
  }, [entryId])

  return (
    <div className="editor-page">
      <p className="status" data-status={status} role="status">
        {statusText[status]}
      </p>
      <div className="panes">
        <div ref={host} className="pane editor" aria-label="Markdown editor" />
        <div className="pane preview" aria-label="Preview">
          <Markdown remarkPlugins={[remarkGfm]}>{preview}</Markdown>
        </div>
      </div>
    </div>
  )
}
