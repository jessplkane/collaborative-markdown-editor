import { describe, expect, it } from 'vitest'
import { backoffDelay } from './backoff.ts'

describe('backoffDelay', () => {
  it('grows with each attempt until it reaches the cap, and stays there', () => {
    const delays = Array.from({ length: 12 }, (_, n) => backoffDelay(n, () => 1))
    expect(delays[0]).toBe(500)
    for (let n = 1; n < delays.length; n++) {
      expect(delays[n]).toBeGreaterThanOrEqual(delays[n - 1])
    }
    expect(delays.at(-1)).toBe(10_000)
    expect(backoffDelay(1000, () => 1)).toBe(10_000)
  })

  it('spreads clients over the upper half of the ceiling', () => {
    expect(backoffDelay(3, () => 0)).toBe(2000)
    expect(backoffDelay(3, () => 0.999999)).toBeLessThan(4000)
  })
})
