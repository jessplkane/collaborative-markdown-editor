import type { ChangeSet } from '@codemirror/state'

interface LoggedStep {
  changes: ChangeSet
  /** The client whose edit this was. */
  author: string
}

/**
 * The most recent document steps, kept so a position given for an older
 * version can be carried forward to the newest one. Remote cursors need this:
 * a cursor is reported against the version its owner had synced, which is
 * usually a little behind ours.
 */
export class StepLog {
  /** steps[i] takes version `first + i` to `first + i + 1`. */
  private steps: LoggedStep[] = []
  private first: number
  private readonly limit: number

  constructor(startVersion: number, limit = 1000) {
    this.first = startVersion
    this.limit = limit
  }

  /** The newest version: the one after every pushed step. */
  get version(): number {
    return this.first + this.steps.length
  }

  /** Records the step that takes the newest version to the next one. */
  push(changes: ChangeSet, author: string) {
    this.steps.push({ changes, author })
    if (this.steps.length > this.limit) {
      this.steps.shift()
      this.first++
    }
  }

  /**
   * Maps `pos`, a position in document `version` that belongs to `owner`, to
   * the newest version. Text inserted at the position by someone else lands
   * after it; text the owner inserted there lands before it, because that is
   * the owner typing at their own cursor, which moves along as they type.
   * Returns undefined if `version` is older than the log reaches or newer
   * than the newest version.
   */
  mapToNewest(version: number, pos: number, owner: string): number | undefined {
    if (version < this.first || version > this.version) return undefined
    let mapped = pos
    for (let i = version - this.first; i < this.steps.length; i++) {
      const step = this.steps[i]
      mapped = step.changes.mapPos(mapped, step.author === owner ? 1 : -1)
    }
    return mapped
  }
}
