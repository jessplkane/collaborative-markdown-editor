import { useEffect, useRef, useState } from 'react'

// Slice-0 wiring check. Removed once the real entry websocket exists.
export function EchoPage() {
  const socket = useRef<WebSocket | null>(null)
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>(
    'connecting',
  )
  const [input, setInput] = useState('')
  const [log, setLog] = useState<string[]>([])

  useEffect(() => {
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${scheme}://${location.host}/ws/echo`)
    socket.current = ws
    ws.onopen = () => setStatus('open')
    ws.onclose = () => setStatus('closed')
    ws.onmessage = (e) => setLog((l) => [...l, `← ${String(e.data)}`])
    return () => ws.close()
  }, [])

  function send() {
    if (!input || socket.current?.readyState !== WebSocket.OPEN) return
    socket.current.send(input)
    setLog((l) => [...l, `→ ${input}`])
    setInput('')
  }

  return (
    <main>
      <h1>WebSocket echo</h1>
      <p data-testid="status">{status}</p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          send()
        }}
      >
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="type a message"
        />
        <button type="submit" disabled={status !== 'open'}>
          Send
        </button>
      </form>
      <ul data-testid="log">
        {log.map((line, i) => (
          <li key={i}>{line}</li>
        ))}
      </ul>
    </main>
  )
}
