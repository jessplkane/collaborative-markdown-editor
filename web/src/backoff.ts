const BASE_MS = 500
const CAP_MS = 10_000

/**
 * How long to wait before reconnect attempt number `attempt` (0 for the
 * first). The ceiling doubles from 500 ms up to 10 s, and the wait is a random
 * point between half the ceiling and the ceiling: clients that lost the server
 * together should not all come back at the same instant.
 *
 * `random` is injectable for tests and returns a number in [0, 1).
 */
export function backoffDelay(attempt: number, random = Math.random): number {
  const ceiling = Math.min(CAP_MS, BASE_MS * 2 ** attempt)
  return ceiling / 2 + (ceiling / 2) * random()
}
