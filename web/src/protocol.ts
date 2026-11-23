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

/** Says who this connection is. Sent right after the snapshot. */
export interface HelloMessage {
  type: 'hello'
  clientID: string
  userID: string
  name: string
}

/** The sender's cursor, as a position in document `version`. */
export interface CursorMessage {
  type: 'cursor'
  version: number
  pos: number
}

export type ClientMessage = UpdateMessage | HelloMessage | CursorMessage

export interface Viewer {
  clientID: string
  userID: string
  name: string
}

export type ServerMessage =
  | { type: 'snapshot'; doc: string; version: number }
  | { type: 'updates'; version: number; clientID: string; updates: WireStep[] }
  | { type: 'reject'; version: number }
  | { type: 'viewers'; viewers: Viewer[] }
  | { type: 'cursor'; clientID: string; version: number; pos: number }

/** Close code the server uses when the entry does not exist. */
export const CLOSE_NOT_FOUND = 4004
