import { ChangeSet } from '@codemirror/state'
import { describe, expect, it } from 'vitest'
import { createStepLog } from './stepLog.ts'

function insert(docLength: number, at: number, text: string) {
  return ChangeSet.of({ from: at, insert: text }, docLength)
}

describe('createStepLog', () => {
  it('carries a position through every step after its version', () => {
    const log = createStepLog(10)
    log.push(insert(5, 0, 'ab'), 'x') // version 10 -> 11, doc length 5 -> 7
    log.push(insert(7, 0, 'c'), 'x') // version 11 -> 12

    expect(log.version).toBe(12)
    expect(log.mapToNewest(10, 3, 'me')).toBe(6)
    expect(log.mapToNewest(11, 3, 'me')).toBe(4)
    expect(log.mapToNewest(12, 3, 'me')).toBe(3)
  })

  it("keeps a position that sits at someone else's insertion before the new text", () => {
    const log = createStepLog(0)
    log.push(insert(4, 2, 'xyz'), 'other')
    expect(log.mapToNewest(0, 2, 'me')).toBe(2)
    expect(log.mapToNewest(0, 3, 'me')).toBe(6)
  })

  it("moves a position past the owner's own insertion at it, as typing does", () => {
    // A cursor reported at 2, then its owner types "xyz" there.
    const log = createStepLog(0)
    log.push(insert(4, 2, 'x'), 'me')
    log.push(insert(5, 3, 'yz'), 'me')
    expect(log.mapToNewest(0, 2, 'me')).toBe(5)
  })

  it('moves a position inside deleted text to where the deletion was', () => {
    const log = createStepLog(0)
    log.push(ChangeSet.of({ from: 2, to: 6 }, 10), 'other')
    expect(log.mapToNewest(0, 4, 'me')).toBe(2)
    expect(log.mapToNewest(0, 8, 'me')).toBe(4)
  })

  it('counts UTF-16 units, so an astral character shifts positions by two', () => {
    const log = createStepLog(0)
    log.push(insert(1, 0, '😀'), 'other')
    expect(log.mapToNewest(0, 1, 'me')).toBe(3)
  })

  it('rejects versions it cannot answer for', () => {
    const log = createStepLog(5, 2)
    log.push(insert(0, 0, 'a'), 'x')
    log.push(insert(1, 0, 'b'), 'x')
    log.push(insert(2, 0, 'c'), 'x') // evicts the step from version 5

    expect(log.version).toBe(8)
    expect(log.mapToNewest(5, 0, 'me')).toBeUndefined() // older than the log
    expect(log.mapToNewest(6, 1, 'me')).toBe(3)
    expect(log.mapToNewest(9, 0, 'me')).toBeUndefined() // not reached yet
  })
})
