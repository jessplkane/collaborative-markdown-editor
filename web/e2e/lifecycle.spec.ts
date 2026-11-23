import { expect, test } from '@playwright/test'
import { createEntry, editorText, openEntry } from './helpers.ts'

test('deleting an entry sends everyone viewing it back to the list with a notice', async ({
  browser,
  request,
}) => {
  const id = await createEntry(request)
  const a = await openEntry(browser, id, 'Ada')
  const b = await openEntry(browser, id, 'Grace')
  await a.keyboard.type('doomed')
  await expect.poll(() => editorText(b)).toBe('doomed')

  const res = await request.delete(`/api/entries/${id}`)
  expect(res.status()).toBe(204)

  for (const page of [a, b]) {
    await expect(page).toHaveURL(/\/\?notice=deleted$/)
    await expect(page.getByRole('status')).toContainText('was deleted')
    await expect(page.getByRole('heading', { name: 'Entries' })).toBeVisible()
    await expect(page.locator(`a[href="/entries/${id}"]`)).toHaveCount(0)
  }

  // The notice goes away when dismissed, and so does its trace in the URL.
  await a.getByRole('button', { name: 'Dismiss' }).click()
  await expect(a.getByRole('status')).toHaveCount(0)
  await expect(a).toHaveURL(/\/$/)
})

test('an edit that would make the entry too large is refused, and the saved text is intact', async ({
  browser,
  request,
}) => {
  // The test server's cap is MAX_DOC_UNITS in playwright.config.ts.
  const id = await createEntry(request)
  const a = await openEntry(browser, id, 'Ada')
  const b = await openEntry(browser, id, 'Grace')
  await a.keyboard.type('hello')
  await expect.poll(() => editorText(b)).toBe('hello')

  await a.keyboard.insertText('x'.repeat(30_000))
  await expect(a.getByRole('status')).toContainText('size limit')
  await expect(a.locator('.cm-content')).toHaveAttribute('contenteditable', 'false')

  // Nobody else saw it, and the connection that sent it is not retried.
  await expect(b.getByRole('status')).toHaveText('Connected')
  expect(await editorText(b)).toBe('hello')
  await a.waitForTimeout(1500) // longer than the first reconnect delay
  await expect(a.getByRole('status')).toContainText('size limit')

  await a.reload()
  await expect(a.getByRole('status')).toHaveText('Connected')
  await expect.poll(() => editorText(a)).toBe('hello')
})
