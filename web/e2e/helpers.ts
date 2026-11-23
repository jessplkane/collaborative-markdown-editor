import {
  type APIRequestContext,
  type Browser,
  expect,
  type Page,
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
 * user) as `name`, and waits until the editor is connected.
 */
export async function openEntry(
  browser: Browser,
  entryId: string,
  name: string,
): Promise<Page> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await page.goto(`/entries/${entryId}`)
  await page.getByLabel('Your name').fill(name)
  await page.getByRole('button', { name: 'Join' }).click()
  await expect(page.getByRole('status')).toHaveText('Connected')
  await page.locator('.cm-content').click()
  return page
}

/**
 * The editor's text. textContent rather than innerText: a remote cursor at the
 * end of a line makes CodeMirror add a <br>, which innerText reports as a
 * newline. Lines are separate elements, so use it for single-line documents.
 */
export async function editorText(page: Page): Promise<string> {
  return (await page.locator('.cm-content').textContent()) ?? ''
}
