import { expect, test } from '@playwright/test'

declare const process: { env: Record<string, string | undefined> }

test('internal event sources work through the live Go handler and PostgreSQL', async ({ page }) => {
  const name = process.env.GENERIC_E2E_SOURCE_NAME ?? `Browser Source ${Date.now()}`
  const sourceKey = process.env.GENERIC_E2E_SOURCE_KEY
  if (!sourceKey) throw new Error('GENERIC_E2E_SOURCE_KEY must be supplied by the live handler fixture')

  await page.route('https://st.max.ru/**', (route) => route.fulfill({ contentType: 'application/javascript', body: '' }))
  await page.addInitScript(() => {
    const opened: string[] = []
    Object.defineProperty(window, '__openedTicketLinks', { value: opened, configurable: false })
    Object.defineProperty(window, 'WebApp', { value: { initData: 'fixture', platform: 'web', ready() {}, openLink(url: string) { opened.push(url) } }, configurable: true })
    window.open = ((url?: string | URL) => { if (url) opened.push(String(url)); return {} as Window }) as typeof window.open
  })

  const directImageRequests: string[] = []
  page.on('request', (request) => { if (new URL(request.url()).hostname === 'images.example.test') directImageRequests.push(request.url()) })
  await page.goto('/internal/event-sources')
  await expect(page.getByRole('heading', { name: 'Источники событий' }).first()).toBeVisible()
  await page.getByRole('button', { name: '＋ Добавить источник' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Название', { exact: true }).fill(name)
  await dialog.getByLabel('Endpoint URL', { exact: true }).fill('https://public.example.test/events')
  await dialog.getByRole('combobox').nth(0).selectOption('bearer')
  await dialog.getByLabel('Секрет', { exact: true }).fill('live-browser-secret')
  await dialog.getByLabel('External ID path', { exact: true }).fill('id')
  await dialog.getByLabel('Title path', { exact: true }).fill('title')
  await dialog.getByLabel('Start date path', { exact: true }).fill('starts_at')
  await dialog.getByLabel('Start date transform', { exact: true }).selectOption('iso_datetime')
  await dialog.getByLabel('Venue name path', { exact: true }).fill('venue.name')
  await dialog.getByLabel('Ticket URL path', { exact: true }).fill('ticket_url')
  await dialog.getByLabel('Ticket available path', { exact: true }).fill('ticket_available')
  await dialog.getByLabel('Price from path', { exact: true }).fill('price')
  await dialog.getByLabel('Price from transform', { exact: true }).selectOption('number')

  await dialog.getByLabel('Image path', { exact: true }).fill('image_url')
  await dialog.getByRole('button', { name: 'Проверить' }).click()
  await expect(dialog.getByRole('heading', { name: 'Результат проверки' })).toBeVisible()
  await expect(dialog.getByText('Live handler event')).toBeVisible()
  const imageDomain = dialog.locator('article').filter({ hasText: 'images.example.test' })
  const ticketDomain = dialog.locator('article').filter({ hasText: 'tickets.example.test' })
  await expect(imageDomain).toContainText('Изображения')
  await expect(imageDomain).toContainText(/1 событий/)
  await expect(ticketDomain).toContainText('Билеты')
  await expect(imageDomain).toContainText('Не разрешён')
  await imageDomain.getByRole('button', { name: 'Сохранить источник и разрешить домен' }).click()
  await expect(imageDomain).toContainText('✓ Разрешён')
  await ticketDomain.getByRole('button', { name: 'Разрешить', exact: true }).click()
  await expect(ticketDomain).toContainText('✓ Разрешён')
  await dialog.getByRole('button', { name: 'Сохранить' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText(name)).toBeVisible()

  const sourceCard = page.locator('article').filter({ hasText: name })
  await sourceCard.getByRole('button', { name: 'Синхронизировать' }).click()
  await expect(page.getByRole('status')).toContainText('Синхронизация завершена: succeeded.', { timeout: 15_000 })

  const eventResponse = await page.request.get('/__test__/generic-event')
  expect(eventResponse.ok()).toBeTruthy()
  const { event_id: eventId } = await eventResponse.json() as { event_id: string }
  await page.goto(`/events/${eventId}`)
  await expect(page.getByRole('heading', { name: 'Live handler event' })).toBeVisible()
  const image = page.locator('img').first()
  await expect(image).toHaveAttribute('src', new RegExp(`/api/v1/event-images/[0-9a-f-]+/content`))
  await expect.poll(async () => image.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0)).toBe(true)
  const imagePath = await image.getAttribute('src')
  expect(imagePath).not.toContain('images.example.test')
  expect(directImageRequests).toEqual([])

  await page.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' }).click()
  await expect.poll(() => page.evaluate(() => (window as unknown as { __openedTicketLinks: string[] }).__openedTicketLinks.length)).toBe(1)
  const ticketPath = await page.evaluate(() => (window as unknown as { __openedTicketLinks: string[] }).__openedTicketLinks[0])
  expect(ticketPath).toMatch(/^https?:\/\/[^/]+\/api\/v1\/events\/[0-9a-f-]+\/ticket$/)
  const ticketResponse = await page.request.get(ticketPath, { maxRedirects: 0 })
  expect(ticketResponse.status()).toBe(302)
  expect(ticketResponse.headers()['location']).toMatch(/^https:\/\/tickets\.example\.test\//)

  await page.goto('/internal/event-sources')
  const sourceCardAfterReturn = page.locator('article').filter({ hasText: sourceKey })
  await sourceCardAfterReturn.getByRole('button', { name: 'Редактировать' }).click()
  const editDialog = page.getByRole('dialog')
  const permission = editDialog.locator('li').filter({ hasText: 'images.example.test' })
  await permission.getByRole('button', { name: 'Отозвать' }).click()
  const imageId = imagePath?.match(/event-images\/([^/]+)\/content/)?.[1]
  expect(imageId).toBeTruthy()
  // click() waits for the input action, not the asynchronous domain mutation.
  // The permission disappears only after DELETE and the refreshed domain list.
  await expect(permission).toHaveCount(0)
  expect((await page.request.get(`/api/v1/event-images/${imageId}/content`)).status()).toBe(403)
  await expect(editDialog.locator('li').filter({ hasText: 'tickets.example.test' })).toBeVisible()
  // Keep the mutation observably asynchronous even on a fast local runner,
  // so this scenario also covers the slower request scheduling seen in CI.
  await page.route('**/api/v1/internal/event-sources/*/domains', async (route) => {
    if (route.request().method() === 'POST') await new Promise((resolve) => setTimeout(resolve, 250))
    await route.continue()
  })
  await editDialog.getByLabel('Hostname').fill('images.example.test')
  const [reapproval] = await Promise.all([
    page.waitForResponse((response) => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/domains')),
    editDialog.getByRole('button', { name: 'Разрешить домен' }).click(),
  ])
  expect(reapproval.status()).toBe(200)
  await expect(permission).toBeVisible()
  expect((await page.request.get(`/api/v1/event-images/${imageId}/content`)).status()).toBe(200)
  await editDialog.getByRole('button', { name: 'Отмена' }).click()

  await sourceCardAfterReturn.getByRole('button', { name: 'Редактировать' }).click()
  const finalEditDialog = page.getByRole('dialog')
  await expect(finalEditDialog.getByLabel('Секрет', { exact: true })).toHaveValue('')
  await expect(finalEditDialog.getByLabel('Секрет', { exact: true })).toHaveAttribute('placeholder', /Секрет сохранён/)
  await finalEditDialog.getByRole('button', { name: 'Сохранить' }).click()
  await expect(finalEditDialog).toBeHidden()

  await sourceCardAfterReturn.getByRole('button', { name: 'Выключить' }).click()
  await expect(page.getByRole('status')).toContainText('выключен')
  await expect(sourceCardAfterReturn).toContainText('Выключен')
})
