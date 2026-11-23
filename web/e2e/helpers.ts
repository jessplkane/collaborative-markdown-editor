import {
  type APIRequestContext,
  type Browser,
  expect,
  type Page,
  type WebSocketRoute,
} from '@playwright/test'

/** Creates an empty entry through the REST API and returns its id. */
export async function createEntry(request: APIRequestContext): Promise<string> {
  const response = await request.post('/api/entries')
  expect(response.ok()).toBe(true)
  const entry: { id: string } = await response.json()
  return entry.id
}

/**
 * Opens an entry in a fresh browser context (its own localStorage, so its own
 * user) as `name`, and waits until the editor is connected. `before` runs on
 * the page ahead of navigation, for things like `createNetwork().install` that must
 * be in place before the page opens its socket.
 */
export async function openEntry(
  browser: Browser,
  entryId: string,
  name: string,
  before?: (page: Page) => Promise<void>,
): Promise<Page> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await before?.(page)
  await page.goto(`/entries/${entryId}`)
  await page.getByLabel('Your name').fill(name)
  await page.getByRole('button', { name: 'Join' }).click()
  await expect(page.getByRole('status')).toHaveText('Connected')
  await page.locator('.cm-content').click()
  return page
}

/**
 * A page's connection to the server that a test can sever and restore.
 * `install` must run before the page navigates.
 */
export function createNetwork() {
  let up = true
  let silent = false
  const open = new Set<{ page: WebSocketRoute; server: WebSocketRoute }>()

  return {
    install: (page: Page) =>
      page.routeWebSocket(/\/ws\/entries\//, (route) => {
        if (!up) {
          // The browser sees a connection that fails.
          void route.close({ code: 1006 })
          return
        }
        const server = route.connectToServer()
        const link = { page: route, server }
        open.add(link)
        // Handling messages and closes ourselves means nothing is forwarded
        // unless we do it.
        route.onMessage((message) => {
          if (!silent) server.send(message)
        })
        server.onMessage((message) => {
          if (!silent) route.send(message)
        })
        route.onClose(() => {
          open.delete(link)
          void server.close()
        })
      }),

    /** Drops every open socket, as the server and the page each see it, and refuses new ones. */
    cut: () => {
      up = false
      for (const { page, server } of open) {
        void server.close()
        void page.close({ code: 1006 })
      }
      open.clear()
    },

    /**
     * Keeps sockets open but delivers nothing in either direction, like a
     * network that died without telling anyone.
     */
    blackhole: () => {
      silent = true
    },

    restore: () => {
      up = true
      silent = false
    },
  }
}

/**
 * The editor's text. textContent rather than innerText: a remote cursor at the
 * end of a line makes CodeMirror add a <br>, which innerText reports as a
 * newline. Lines are separate elements, so use it for single-line documents.
 */
export async function editorText(page: Page): Promise<string> {
  return (await page.locator('.cm-content').textContent()) ?? ''
}
