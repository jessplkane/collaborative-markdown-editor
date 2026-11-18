export interface Entry {
  id: string
  title: string
  /** unix milliseconds */
  updatedAt: number
}

export class NotFoundError extends Error {
  constructor() {
    super('not found')
  }
}

async function request(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(path, init)
  if (res.status === 404) throw new NotFoundError()
  if (!res.ok) throw new Error(`${init?.method ?? 'GET'} ${path}: ${res.status}`)
  return res
}

export async function listEntries(): Promise<Entry[]> {
  return (await request('/api/entries')).json()
}

export async function getEntry(id: string): Promise<Entry> {
  return (await request(`/api/entries/${encodeURIComponent(id)}`)).json()
}

export async function createEntry(): Promise<Entry> {
  return (await request('/api/entries', { method: 'POST' })).json()
}

export async function deleteEntry(id: string): Promise<void> {
  await request(`/api/entries/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
