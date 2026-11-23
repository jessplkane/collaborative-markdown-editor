const USER_ID_KEY = 'markdown-editor:userID'
const NAME_KEY = 'markdown-editor:name'

export const MAX_NAME_LENGTH = 40

/** The id that identifies this browser across tabs and visits. */
export function getUserID(): string {
  let id = localStorage.getItem(USER_ID_KEY)
  if (!id) {
    id = crypto.randomUUID()
    localStorage.setItem(USER_ID_KEY, id)
  }
  return id
}

/** The name the user typed, or an empty string if they have not yet. */
export function getName(): string {
  return localStorage.getItem(NAME_KEY) ?? ''
}

export function setName(name: string) {
  localStorage.setItem(NAME_KEY, name)
}

/**
 * A colour for a user, derived from their id so every browser agrees on it
 * without the server having to store or relay one.
 */
export function colorFor(userID: string): string {
  let hash = 0
  for (const ch of userID) hash = (Math.imul(hash, 31) + ch.charCodeAt(0)) | 0
  return `hsl(${Math.abs(hash) % 360} 65% 42%)`
}
