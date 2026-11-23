import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startHeartbeat } from './heartbeat.ts'

describe('startHeartbeat', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('keeps pinging while the server keeps answering', () => {
    const onDead = vi.fn()
    const ping = vi.fn(() => setTimeout(() => beat.heard(), 100))
    const beat = startHeartbeat({ ping, onDead })

    vi.advanceTimersByTime(60_000)

    expect(ping.mock.calls.length).toBeGreaterThan(5)
    expect(onDead).not.toHaveBeenCalled()
    beat.stop()
  })

  it('declares the connection dead once the server has been silent too long, and then stops', () => {
    const onDead = vi.fn()
    const ping = vi.fn()
    startHeartbeat({ ping, onDead })

    vi.advanceTimersByTime(14_000)
    expect(onDead).not.toHaveBeenCalled()
    vi.advanceTimersByTime(2_000)
    expect(onDead).toHaveBeenCalledTimes(1)

    const pings = ping.mock.calls.length
    vi.advanceTimersByTime(60_000)
    expect(onDead).toHaveBeenCalledTimes(1)
    expect(ping.mock.calls.length).toBe(pings)
  })

  it('counts any message from the server as proof of life', () => {
    const onDead = vi.fn()
    const beat = startHeartbeat({ ping: vi.fn(), onDead })

    // An edit arriving every 10 s, but no pong ever.
    for (let i = 0; i < 6; i++) {
      vi.advanceTimersByTime(10_000)
      beat.heard()
    }

    expect(onDead).not.toHaveBeenCalled()
    beat.stop()
  })

  it('stops pinging when stopped', () => {
    const ping = vi.fn()
    const beat = startHeartbeat({ ping, onDead: vi.fn() })
    beat.stop()
    vi.advanceTimersByTime(60_000)
    expect(ping).not.toHaveBeenCalled()
  })
})
