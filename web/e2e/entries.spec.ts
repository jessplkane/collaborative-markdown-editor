import { expect, test } from '@playwright/test'
import { editorText } from './helpers.ts'

test('an entry can be created, listed, reopened and deleted from the UI', async ({
  page,
}) => {
  // Titles come from the first line; a unique one tells this run's entry
  // apart from whatever other specs left in the shared database.
  const title = `Walkthrough ${Date.now()}`

  await page.goto('/')
  await page.getByRole('button', { name: 'New entry' }).click()
  await expect(page).toHaveURL(/\/entries\/[^/]+$/)
  await page.getByLabel('Your name').fill('Ada')
  await page.getByRole('button', { name: 'Join' }).click()
  await expect(page.getByRole('status')).toHaveText('Connected')

  await page.locator('.cm-content').click()
  await page.keyboard.type(`# ${title}`)
  await expect(page.getByLabel('Preview').getByRole('heading')).toHaveText(title)

  // Leaving saves at once, and the list shows the derived title.
  await page.getByRole('link', { name: '← All entries' }).click()
  const row = page.getByRole('listitem').filter({ hasText: title })
  await expect(row).toBeVisible()

  // Reopening shows what was typed.
  await row.getByRole('link', { name: title }).click()
  await expect(page.getByRole('status')).toHaveText('Connected')
  await expect.poll(() => editorText(page)).toBe(`# ${title}`)
  const entryUrl = page.url()

  // Declining the confirmation keeps the entry; accepting removes it.
  await page.getByRole('link', { name: '← All entries' }).click()
  page.once('dialog', (dialog) => void dialog.dismiss())
  await row.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toBeVisible()

  page.once('dialog', (dialog) => void dialog.accept())
  await row.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toHaveCount(0)

  // Its address now says so rather than opening an editor.
  await page.goto(entryUrl)
  await expect(page.getByRole('alert')).toHaveText('This entry does not exist.')
})
