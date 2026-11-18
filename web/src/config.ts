/**
 * Base URL of the Go server, set at build time with VITE_API_URL (for example
 * `https://my-app.fly.dev`). Empty means the server is on the same origin as
 * the page, which is the case in development, where Vite proxies /api and /ws.
 */
export const apiBase: string = (import.meta.env.VITE_API_URL ?? '').replace(
  /\/+$/,
  '',
)

/** Absolute ws:// or wss:// URL for a server path such as `/ws/entries/abc`. */
export function wsUrl(path: string): string {
  const url = new URL(path, apiBase || location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}
