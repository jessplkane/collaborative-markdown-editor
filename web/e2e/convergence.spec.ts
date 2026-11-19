import { type Browser, expect, type Page, test } from '@playwright/test'

async function openEntry(browser: Browser, entryId: string): Promise<Page> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await page.goto(`/entries/${entryId}`)
  await expect(page.getByRole('status')).toHaveText('Connected')
  await page.locator('.cm-content').click()
  return page
}

const editorText = (page: Page) => page.locator('.cm-content').innerText()

test('two browsers typing at once end with identical text', async ({
  browser,
  request,
}) => {
  const created = await request.post('/api/entries')
  expect(created.ok()).toBe(true)
  const { id } = (await created.json()) as { id: string }

  const [a, b] = await Promise.all([openEntry(browser, id), openEntry(browser, id)])

  // Both type into the same spot of an empty document with no pause between
  // keys, so each one's edits are made on top of versions the other has
  // already moved past and the two streams interleave. The writers use
  // disjoint alphabets, which lets the result be checked afterwards. Letters
  // and an astral character only: the editor's bracket and quote auto-closing
  // would add text the test did not type.
  const typedA = 'a😀'.repeat(40)
  const typedB = 'b'.repeat(60)
  await Promise.all([
    a.keyboard.type(typedA, { delay: 8 }),
    b.keyboard.type(typedB, { delay: 8 }),
  ])

  // Convergence is eventual: wait until both editors hold every typed character.
  const total = [...typedA].length + typedB.length
  for (const page of [a, b]) {
    await expect
      .poll(async () => [...(await editorText(page))].length, { timeout: 15_000 })
      .toBe(total)
  }

  const [textOnA, textOnB] = await Promise.all([editorText(a), editorText(b)])
  expect(textOnA).toBe(textOnB)

  // Nothing lost, duplicated or reordered within either writer's stream.
  expect(textOnA.replace(/b/g, '')).toBe(typedA)
  expect(textOnA.replace(/[a😀]/gu, '')).toBe(typedB)

  // The preview, rendered from the same document, matches too.
  await expect(a.getByLabel('Preview')).toHaveText(textOnA)

  // The server holds the same document: a fresh browser sees it.
  const c = await openEntry(browser, id)
  expect(await editorText(c)).toBe(textOnA)
})
