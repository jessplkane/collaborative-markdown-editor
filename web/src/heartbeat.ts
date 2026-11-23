const INTERVAL_MS = 5_000
const DEAD_AFTER_MS = 15_000

export interface Heartbeat {
  /** Something arrived from the server: the connection is alive. */
  heard: () => void
  stop: () => void
}

/**
 * Detects a connection that has gone quiet without closing. A dropped network
 * or a proxy whose far end died can leave a WebSocket "open" for minutes, and
 * a browser cannot see protocol-level pings, so the client asks for an
 * application-level reply every few seconds and gives up on a connection
 * that has said nothing for three intervals.
 */
export function startHeartbeat(opts: {
  /** Asks the server for a message (it answers a ping with a pong). */
  ping: () => void
  /** Nothing was heard for too long. The heartbeat has already stopped. */
  onDead: () => void
}): Heartbeat {
  let lastHeard = Date.now()
  const timer = setInterval(() => {
    if (Date.now() - lastHeard >= DEAD_AFTER_MS) {
      stop()
      opts.onDead()
    } else {
      opts.ping()
    }
  }, INTERVAL_MS)

  function stop() {
    clearInterval(timer)
  }

  return {
    heard: () => {
      lastHeard = Date.now()
    },
    stop,
  }
}
