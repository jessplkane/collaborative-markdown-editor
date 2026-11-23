import { describe, expect, it } from 'vitest'
import { isTerminalClose } from './protocol.ts'

describe('isTerminalClose', () => {
  it.each([
    ['abnormal drop', 1006],
    ['server going away (restart)', 1001],
    ['too slow, try again later', 1013],
  ])('retries after %s', (_, code) => {
    expect(isTerminalClose(code)).toBe(false)
  })

  it.each([
    ['unknown entry', 4004],
    ['replaced by a newer connection', 4009],
    ['invalid payload', 1007],
    ['policy violation', 1008],
    ['message too big', 1009],
  ])('gives up after %s', (_, code) => {
    expect(isTerminalClose(code)).toBe(true)
  })
})
