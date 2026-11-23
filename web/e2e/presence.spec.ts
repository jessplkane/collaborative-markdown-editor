import { expect, type Locator, type Page, test } from '@playwright/test'
import { createEntry, openEntry } from './helpers.ts'

function viewerList(page: Page) {
  return page.getByRole('list', { name: 'Viewers' }).getByRole('listitem')
}

function remoteCursor(page: Page, name: string) {
  return page.locator(`.remote-cursor[data-name="${name}"]`)
}

/**
 * Where a cursor is drawn: UTF-16 units of text before it on its line. The
 * editor redraws widgets on every update, so the element can be detached by
 * the time it is measured; that reads as -1 and the caller's poll tries again.
 */
function offsetOf(cursor: Locator): Promise<number> {
  return cursor.evaluate((node) => {
    const line = node.closest('.cm-line')
    if (!line) return -1
    const range = document.createRange()
    range.selectNodeContents(line)
    range.setEndBefore(node)
    return range.toString().length
  })
}

test('the name prompt appears once and the name is remembered', async ({
  page,
  request,
}) => {
  const id = await createEntry(request)
  await page.goto(`/entries/${id}`)
  await expect(page.locator('.cm-content')).toHaveCount(0)

  await page.getByLabel('Your name').fill('  Ada  ')
  await page.getByRole('button', { name: 'Join' }).click()
  await expect(page.getByRole('status')).toHaveText('Connected')
  await expect(viewerList(page)).toHaveText(['Ada (you)'])

  await page.reload()
  await expect(page.getByLabel('Your name')).toHaveCount(0)
  await expect(viewerList(page)).toHaveText(['Ada (you)'])
})

test('viewers see each other, and leave the list when they close the page', async ({
  browser,
  request,
}) => {
  const id = await createEntry(request)
  const a = await openEntry(browser, id, 'Ada')
  const b = await openEntry(browser, id, 'Grace')

  await expect(viewerList(a)).toHaveText(['Ada (you)', 'Grace'])
  await expect(viewerList(b)).toHaveText(['Ada', 'Grace (you)'])

  // A viewer's colour is derived from their id, so both pages show the same one.
  function dotColor(page: Page) {
    return page
      .locator('.viewers li', { hasText: 'Grace' })
      .locator('.viewer-dot')
      .evaluate((el) => getComputedStyle(el).backgroundColor)
  }

  expect(await dotColor(a)).toBe(await dotColor(b))

  await b.context().close()
  await expect(viewerList(a)).toHaveText(['Ada (you)'])
})

test('remote cursors show where the other person is, through edits on both sides', async ({
  browser,
  request,
}) => {
  const id = await createEntry(request)
  const a = await openEntry(browser, id, 'Ada')
  const b = await openEntry(browser, id, 'Grace')
  await expect(viewerList(a)).toHaveCount(2)
  await expect(viewerList(b)).toHaveCount(2)

  // Ada types a line containing an astral character (two UTF-16 units).
  const line = 'hi 😀 there'
  await a.keyboard.type(line, { delay: 5 })
  await expect(b.locator('.cm-line').first()).toHaveText(line)

  // Grace sees Ada's cursor at the end of what she typed, and Ada sees
  // Grace's still at the start.
  await expect.poll(() => offsetOf(remoteCursor(b, 'Ada'))).toBe(line.length)
  await expect.poll(() => offsetOf(remoteCursor(a, 'Grace'))).toBe(0)

  // Grace moves to the end of the line; Ada's view follows.
  await b.keyboard.press('End')
  await expect.poll(() => offsetOf(remoteCursor(a, 'Grace'))).toBe(line.length)

  // Ada types at the spot Grace is at: on Ada's screen Grace's cursor stays
  // before the new text, and on Grace's Ada's cursor moves past it.
  await a.keyboard.type('!')
  await expect(b.locator('.cm-line').first()).toHaveText(`${line}!`)
  await expect.poll(() => offsetOf(remoteCursor(b, 'Ada'))).toBe(line.length + 1)
  await expect.poll(() => offsetOf(remoteCursor(a, 'Grace'))).toBe(line.length)

  // Someone who arrives later learns where everyone already is.
  const c = await openEntry(browser, id, 'Linus')
  await expect.poll(() => offsetOf(remoteCursor(c, 'Ada'))).toBe(line.length + 1)
  await expect.poll(() => offsetOf(remoteCursor(c, 'Grace'))).toBe(line.length)

  // A cursor goes when its owner does.
  await a.context().close()
  await expect(remoteCursor(b, 'Ada')).toHaveCount(0)
})
