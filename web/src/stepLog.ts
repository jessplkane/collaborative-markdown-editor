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
export interface StepLog {
  /** The newest version: the one after every pushed step. */
  readonly version: number
  /** Records the step that takes the newest version to the next one. */
  push: (changes: ChangeSet, author: string) => void
  /**
   * Maps `pos`, a position in document `version` that belongs to `owner`, to
   * the newest version. Text inserted at the position by someone else lands
   * after it; text the owner inserted there lands before it, because that is
   * the owner typing at their own cursor, which moves along as they type.
   * Returns undefined if `version` is older than the log reaches or newer
   * than the newest version.
   */
  mapToNewest: (
    version: number,
    pos: number,
    owner: string,
  ) => number | undefined
}

/** `limit` bounds how many steps are remembered; older ones are forgotten. */
export function createStepLog(startVersion: number, limit = 1000): StepLog {
  // steps[i] takes version `first + i` to `first + i + 1`.
  const steps: LoggedStep[] = []
  let first = startVersion

  function newest() {
    return first + steps.length
  }

  function push(changes: ChangeSet, author: string) {
    steps.push({ changes, author })
    if (steps.length > limit) {
      steps.shift()
      first++
    }
  }

  function mapToNewest(version: number, pos: number, owner: string) {
    if (version < first || version > newest()) return undefined
    let mapped = pos
    for (let i = version - first; i < steps.length; i++) {
      const step = steps[i]
      mapped = step.changes.mapPos(mapped, step.author === owner ? 1 : -1)
    }
    return mapped
  }

  return {
    get version() {
      return newest()
    },
    push,
    mapToNewest,
  }
}
