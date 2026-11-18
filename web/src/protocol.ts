// Wire protocol with the Go server (server/room.go documents it too).
// Positions are UTF-16 code units, which is what CodeMirror uses, and every
// position in a list of changes refers to the document before that step.

export interface WireChange {
  from: number
  to: number
  insert: string
}

/** One document version: the changes that take version n to n + 1. */
export interface WireStep {
  changes: WireChange[]
}

export interface UpdateMessage {
  type: 'update'
  clientID: string
  /** Version the steps were made on top of. */
  version: number
  updates: WireStep[]
}

export type ServerMessage =
  | { type: 'snapshot'; doc: string; version: number }
  | { type: 'updates'; version: number; clientID: string; updates: WireStep[] }
  | { type: 'reject'; version: number }

/** Close code the server uses when the entry does not exist. */
export const CLOSE_NOT_FOUND = 4004
