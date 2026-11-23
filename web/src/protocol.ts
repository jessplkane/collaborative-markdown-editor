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

/** Asks for a `pong`, so the client can tell a live connection from a dead one. */
export interface PingMessage {
  type: 'ping'
}

export type ClientMessage =
  | UpdateMessage
  | HelloMessage
  | CursorMessage
  | PingMessage

export interface Viewer {
  clientID: string
  userID: string
  name: string
}

export type ServerMessage =
  | { type: 'snapshot'; doc: string; version: number; epoch: string }
  | { type: 'pong' }
  | { type: 'resumed'; version: number }
  | { type: 'updates'; version: number; clientID: string; updates: WireStep[] }
  | { type: 'reject'; version: number }
  | { type: 'viewers'; viewers: Viewer[] }
  | { type: 'cursor'; clientID: string; version: number; pos: number }

/** Close code the server uses when the entry does not exist. */
export const CLOSE_NOT_FOUND = 4004

/** The server closed this connection because the same client opened another. */
export const CLOSE_REPLACED = 4009

/** The entry was deleted while this client was viewing it. */
export const CLOSE_DELETED = 4010

/**
 * The client's last update would have made the document larger than the
 * server allows. It was not applied.
 */
export const CLOSE_TOO_LARGE = 4013

/**
 * Close codes after which reconnecting cannot help: the entry is gone, the
 * server is no longer talking to this client, or it refused what the client
 * sent (1007 malformed message, 1008 policy violation, 1009 message too
 * big, 4013 document too large), which would be refused again. Every other
 * close, including the server's "try again later" (1013) and "going away"
 * (1001) and an abnormal drop (1006), is worth retrying.
 */
const TERMINAL_CLOSE_CODES = new Set([
  1007,
  1008,
  1009,
  CLOSE_NOT_FOUND,
  CLOSE_REPLACED,
  CLOSE_DELETED,
  CLOSE_TOO_LARGE,
])

export function isTerminalClose(code: number): boolean {
  return TERMINAL_CLOSE_CODES.has(code)
}
