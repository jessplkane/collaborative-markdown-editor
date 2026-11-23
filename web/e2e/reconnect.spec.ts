import { expect, test, type Page } from '@playwright/test'
import { createEntry, createNetwork, editorText, openEntry } from './helpers.ts'

async function waitForText(page: Page, text: string) {
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toBe(text)
}

test('edits made while disconnected are kept and sent on reconnect', async ({
  browser,
  request,
}) => {
  const id = await createEntry(request)
  const network = createNetwork()
  const a = await openEntry(browser, id, 'Ada', network.install)
  const b = await openEntry(browser, id, 'Grace')

  await a.keyboard.type('AA')
  await expect.poll(() => editorText(b)).toBe('AA')

  network.cut()
  await expect(a.getByRole('status')).toContainText('Reconnecting')
  // The viewer list is not trusted while disconnected.
  await expect(a.getByLabel('Viewers').getByRole('listitem')).toHaveCount(0)

  // Both sides keep typing through the outage. A's editor stays editable.
  // Disjoint alphabets (upper case for A) keep the result checkable.
  await a.keyboard.type('AAAA')
  await b.keyboard.type('bbbb')
  await expect(b.getByLabel('Preview')).toContainText('bbbb')

  network.restore()
  await expect(a.getByRole('status')).toHaveText('Connected', { timeout: 15_000 })

  const total = 'AAAAAA'.length + 'bbbb'.length
  for (const page of [a, b]) {
    await expect
      .poll(async () => (await editorText(page)).length, { timeout: 15_000 })
      .toBe(total)
  }
  const [textOnA, textOnB] = await Promise.all([editorText(a), editorText(b)])
  expect(textOnA).toBe(textOnB)
  expect(textOnA.replace(/b/g, '')).toBe('AAAAAA')
  expect(textOnA.replace(/A/g, '')).toBe('bbbb')

  // Resumed, not reloaded: nothing was dropped, and Ada is listed once.
  await expect(a.getByRole('alert')).toHaveCount(0)
  await expect(b.getByLabel('Viewers').getByRole('listitem')).toHaveCount(2)

  // The connection is fully usable again, both ways.
  await a.keyboard.type('Z')
  await expect.poll(() => editorText(b)).toContain('Z')
})

test('a lone client that drops can resume its own session', async ({
  browser,
  request,
}) => {
  const id = await createEntry(request)
  const network = createNetwork()
  const a = await openEntry(browser, id, 'Ada', network.install)
  await a.keyboard.type('one')

  network.cut()
  await expect(a.getByRole('status')).toContainText('Reconnecting')
  await a.keyboard.type('two')

  network.restore()
  await expect(a.getByRole('status')).toHaveText('Connected', { timeout: 15_000 })
  await expect(a.getByRole('alert')).toHaveCount(0)
  await waitForText(a, 'onetwo')

  const reader = await openEntry(browser, id, 'Grace')
  await waitForText(reader, 'onetwo')
})

test('a connection that goes silent without closing is noticed and replaced', async ({
  browser,
  request,
}) => {
  // The client gives up on a connection after 15 s of silence.
  test.setTimeout(60_000)
  const id = await createEntry(request)
  const network = createNetwork()
  const a = await openEntry(browser, id, 'Ada', network.install)
  const b = await openEntry(browser, id, 'Grace')
  await a.keyboard.type('AA')
  await expect.poll(() => editorText(b)).toBe('AA')

  network.blackhole()
  await a.keyboard.type('AA') // goes nowhere
  await b.keyboard.type('bb')
  await expect(a.getByRole('status')).toContainText('Reconnecting', { timeout: 30_000 })

  network.restore()
  await expect(a.getByRole('status')).toHaveText('Connected', { timeout: 30_000 })
  for (const page of [a, b]) {
    await expect
      .poll(async () => (await editorText(page)).length, { timeout: 15_000 })
      .toBe(6)
  }
  expect(await editorText(a)).toBe(await editorText(b))
  await expect(a.getByRole('alert')).toHaveCount(0)
})

test('when the server cannot replay what was missed, the editor reloads the document and says what it dropped', async ({
  browser,
  request,
}) => {
  // The test server keeps a very small history (RESUME_LOG_BYTES in
  // playwright.config.ts); a paste larger than it empties it.
  const id = await createEntry(request)
  const network = createNetwork()
  const a = await openEntry(browser, id, 'Ada', network.install)
  const b = await openEntry(browser, id, 'Grace')

  await a.keyboard.type('AA')
  await expect.poll(() => editorText(b)).toBe('AA')

  network.cut()
  await expect(a.getByRole('status')).toContainText('Reconnecting')
  await a.keyboard.type('QQQ') // never reaches the server
  const paste = 'z'.repeat(5000)
  await b.keyboard.insertText(paste)
  await expect.poll(async () => (await editorText(b)).length).toBe(2 + 5000)

  network.restore()
  await expect(a.getByRole('alert')).toContainText('edits you had not sent yet were dropped', {
    timeout: 15_000,
  })
  await expect(a.getByRole('status')).toHaveText('Connected')

  const textOnB = await editorText(b)
  await waitForText(a, textOnB)
  expect(textOnB).not.toContain('QQQ')

  // Editing carries on from the reloaded document.
  await a.keyboard.type('Y')
  await expect.poll(() => editorText(b)).toContain('Y')
})

// The page re-checks that the entry exists whenever the tab regains focus. If
// that check fails (a blip, a deploy, a cold start) the open editor must stay:
// it holds edits the server has not confirmed. A 404 is used because it is not
// retried, so the failure lands at once; any failure ends the same way.
test('a failed re-check of the entry does not tear down the open editor', async ({
  browser,
  request,
}) => {
  const id = await createEntry(request)
  const a = await openEntry(browser, id, 'Ada')
  await a.keyboard.type('kept')
  await expect.poll(() => editorText(a)).toBe('kept')

  await a.route(`**/api/entries/${id}`, (route) => route.fulfill({ status: 404 }))
  const refetched = a.waitForResponse(`**/api/entries/${id}`)
  await a.evaluate(() => window.dispatchEvent(new Event('visibilitychange')))
  await refetched
  // Let the failure reach the page's state before looking.
  // (The e2e tsconfig's lib predates Promise.withResolvers.)
  await a.evaluate(
    () => new Promise((done) => requestAnimationFrame(() => setTimeout(done))),
  )

  await expect(a.getByRole('status')).toHaveText('Connected')
  await expect(a.getByText('This entry does not exist.')).toHaveCount(0)
  expect(await editorText(a)).toBe('kept')
})
