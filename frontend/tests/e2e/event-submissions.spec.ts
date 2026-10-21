import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'

const user = { id: 'user-event-test', display_name: 'Е2Е пользователь', avatar_url: null, city_id: 'city-moscow', locale: 'ru' }
const city = { id: 'city-moscow', name: 'Москва', timezone: 'Europe/Moscow' }
const submissionId = 'submission-e2e-1'
const eventId = 'published-e2e-1'
const categories = [
  ['concerts', 'Концерты'], ['cinema', 'Кино'], ['theatre', 'Театр'], ['standup', 'Стендап'],
  ['exhibitions', 'Выставки'], ['sports', 'Спорт'], ['food', 'Еда'], ['parties', 'Вечеринки'],
  ['festivals', 'Фестивали'], ['walks', 'Прогулки'], ['other', 'Другое'],
].map(([slug, label]) => ({ slug, label }))
const posterId = 'poster-upload-e2e-1'

async function setupBackend(page: Page) {
  let state: 'pending' | 'approved' | 'rejected' = 'pending'
  let submittedPayload: Record<string, unknown> | null = null
  let uploadAttempts = 0
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({ json: { access_token: 'e2e-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences: null, daily_notifications_enabled: false, invite_context: null } }))
  await page.route('**/api/v1/event-submissions/options', (route) => route.fulfill({ json: { cities: [city], categories } }))
  await page.route('**/api/v1/event-submissions/images', async (route) => {
    uploadAttempts += 1
    const contentType = route.request().headers()['content-type'] ?? ''
    expect(contentType).toContain('multipart/form-data; boundary=')
    expect(route.request().postData()).toContain('filename="concert-singer.png"')
    if (uploadAttempts === 1) return route.fulfill({ status: 503, json: { error: { code: 'UNAVAILABLE', message: 'Попробуйте ещё раз.' } } })
    await new Promise((resolve) => setTimeout(resolve, 900))
    await route.fulfill({ status: 201, json: { image_id: posterId } })
  })
  await page.route('**/api/v1/event-submissions', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    submittedPayload = route.request().postDataJSON() as Record<string, unknown>
    expect(route.request().headers()['idempotency-key']).toMatch(/^[0-9a-f-]{36}$/i)
    await route.fulfill({ status: 201, json: { id: submissionId, status: 'pending', created_at: '2099-01-01T00:00:00Z' } })
  })
  await page.route('**/api/v1/me/event-submissions**', async (route) => {
    if (!submittedPayload) return route.fulfill({ json: { items: [], total: 0 } })
    await route.fulfill({ json: { items: [{ id: submissionId, author_id: user.id, city_id: city.id, starts_at: '2099-06-01T07:00:00Z', timezone: city.timezone, payload: submittedPayload, status: state, created_at: '2099-01-01T00:00:00Z', decided_at: state === 'pending' ? null : '2099-01-02T00:00:00Z', decided_by: state === 'pending' ? null : 'moderator', rejection_reason: state === 'rejected' ? 'Нет подтверждения организатора.' : null, event_id: state === 'approved' ? eventId : null }], total: 1 } })
  })
  await page.route('**/api/v1/internal/event-submissions/domains', async (route) => {
    if (route.request().method() === 'GET') return route.fulfill({ json: { domains: [] } })
    return route.fulfill({ status: 201, json: { id: 'domain-1', hostname: 'tickets.example', purpose: 'ticket' } })
  })
  await page.route('**/api/v1/internal/event-submissions/domains/*', (route) => route.fulfill({ status: 204, body: '' }))
  await page.route('**/api/v1/internal/event-submissions?**', async (route) => {
    const status = new URL(route.request().url()).searchParams.get('status') ?? 'pending'
    await route.fulfill({ json: { items: state === status ? [{ id: submissionId, author_id: user.id, author_name: user.display_name, city_id: city.id, starts_at: '2099-06-01T07:00:00Z', timezone: city.timezone, payload: submittedPayload ?? {}, status: state, created_at: '2099-01-01T00:00:00Z', decided_at: null, decided_by: null, rejection_reason: null, event_id: state === 'approved' ? eventId : null }] : [], total: state === status ? 1 : 0, counts: { pending: state === 'pending' ? 1 : 0, approved: state === 'approved' ? 1 : 0, rejected: state === 'rejected' ? 1 : 0 } } })
  })
  await page.route(`**/api/v1/internal/event-submissions/${submissionId}`, async (route) => route.fulfill({ json: { submission: { id: submissionId, author_id: user.id, author_name: user.display_name, city_id: city.id, starts_at: '2099-06-01T07:00:00Z', timezone: city.timezone, payload: { ...(submittedPayload ?? {}), ends_at_local: '2099-06-01T13:30' }, status: state, created_at: '2099-01-01T00:00:00Z', decided_at: state === 'pending' ? null : '2099-01-02T00:00:00Z', decided_by: state === 'pending' ? null : 'moderator-e2e', rejection_reason: state === 'rejected' ? 'Нет подтверждения организатора.' : null, event_id: state === 'approved' ? eventId : null }, duplicates: [], resource_domains: [] } }))
  await page.route(`**/api/v1/internal/event-submissions/${submissionId}/image`, (route) => route.fulfill({ status: 200, path: 'public/events/concert-singer.png' }))
  await page.route(`**/api/v1/internal/event-submissions/${submissionId}/approve`, async (route) => { state = 'approved'; await route.fulfill({ json: { id: submissionId, status: state, event_id: eventId } }) })
  return { get payload() { return submittedPayload }, get uploadAttempts() { return uploadAttempts } }
}

async function activate(locator: Locator, touch: boolean) {
  if (touch) await locator.tap()
  else await locator.click()
}

test('submit an event, approve it in moderation, and see it in the user history', async ({ page, isMobile }, testInfo: TestInfo) => {
  const backend = await setupBackend(page)
  await page.clock.install({ time: new Date('2099-06-01T06:00:00Z') })
  await page.goto('/events/suggest', { waitUntil: 'domcontentloaded' })
  await page.screenshot({ path: testInfo.outputPath('event-form-empty.png'), fullPage: true })
  await page.getByRole('group', { name: 'Зона загрузки афиши' }).screenshot({ path: testInfo.outputPath('poster-empty-card.png') })
  await page.getByLabel('Название').fill('Вечер живого джаза')
  await page.getByLabel('Описание').fill('Камерный концерт с живым джазом в центре города.')
  await activate(page.getByRole('combobox', { name: 'Город' }), isMobile)
  await activate(page.getByRole('option', { name: city.name }), isMobile)
  await page.getByLabel('Место').fill('Клуб Сцена')
  await page.getByLabel('Адрес').fill('Москва, улица Тверская, 10')
  await activate(page.locator('#event-start-date'), isMobile)
  await page.getByRole('dialog', { name: 'Выбор даты' }).screenshot({ path: testInfo.outputPath('event-date-calendar.png') })
  await activate(page.getByRole('gridcell', { name: '1 июня 2099 г.', exact: true }), isMobile)
  await activate(page.getByRole('button', { name: 'Применить' }), isMobile)
  await page.getByLabel('Время начала, ЧЧ:ММ').fill('10:00')
  await activate(page.locator('#event-end-date'), isMobile)
  await activate(page.getByRole('gridcell', { name: '1 июня 2099 г.', exact: true }), isMobile)
  await activate(page.getByRole('button', { name: 'Применить' }), isMobile)
  await page.getByLabel('Время окончания, ЧЧ:ММ').fill('13:30')
  const startDateBox = await page.locator('#event-start-date').boundingBox()
  const startTimeBox = await page.getByLabel('Время начала, ЧЧ:ММ').boundingBox()
  const endDateBox = await page.locator('#event-end-date').boundingBox()
  const endTimeBox = await page.getByLabel('Время окончания, ЧЧ:ММ').boundingBox()
  expect(startDateBox).not.toBeNull()
  expect(startTimeBox).not.toBeNull()
  expect(endDateBox).not.toBeNull()
  expect(endTimeBox).not.toBeNull()
  const startDateTimeOffset = Math.abs(startDateBox!.y - startTimeBox!.y)
  const endDateTimeOffset = Math.abs(endDateBox!.y - endTimeBox!.y)
  console.log(`[${testInfo.project.name}] event date/time vertical offsets: start=${startDateTimeOffset}px, end=${endDateTimeOffset}px`)
  expect(startDateTimeOffset, 'start date and time controls should align').toBeLessThanOrEqual(5)
  expect(endDateTimeOffset, 'optional end-date and end-time controls should align').toBeLessThanOrEqual(5)
  await page.locator('#event-start-date').locator('xpath=../..').screenshot({ path: testInfo.outputPath('event-start-date-time-row.png') })
  await page.locator('#event-end-date').locator('xpath=../..').screenshot({ path: testInfo.outputPath('event-end-date-time-row.png') })
  await activate(page.getByRole('combobox', { name: 'Категория' }), isMobile)
  await page.getByRole('listbox', { name: 'Категория' }).screenshot({ path: testInfo.outputPath('event-category-options.png') })
  await activate(page.getByRole('option', { name: 'Концерты' }), isMobile)
  await activate(page.getByRole('combobox', { name: /Возрастной рейтинг/ }), isMobile)
  await activate(page.getByRole('option', { name: '0+', exact: true }), isMobile)
  await expect(page.getByRole('combobox', { name: /Возрастной рейтинг/ })).toContainText('0+')
  await page.getByRole('button', { name: 'Есть цена' }).click()
  const priceFrom = page.getByLabel('Цена от, ₽')
  await expect(priceFrom).toHaveAttribute('inputmode', 'decimal')
  await priceFrom.fill('1999,82')
  await page.getByLabel('Ссылка на событие или билеты').fill('https://tickets.example/jazz')
  const posterZone = page.locator('[aria-label="Зона загрузки афиши"]')
  await posterZone.evaluate((zone) => zone.dispatchEvent(new DragEvent('dragenter', { bubbles: true, cancelable: true })))
  await expect(posterZone).toHaveClass(/dropzoneActive/)
  await posterZone.evaluate(async (zone) => {
    const response = await fetch('/events/concert-singer.png')
    const blob = await response.blob()
    const file = new File([blob], 'concert-singer.png', { type: blob.type })
    const transfer = new DataTransfer()
    transfer.items.add(file)
    zone.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, dataTransfer: transfer }))
    zone.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: transfer }))
  })
  await expect(page.getByRole('alert')).toContainText('Попробуйте ещё раз')
  await page.screenshot({ path: testInfo.outputPath('poster-upload-error.png'), fullPage: true })
  await posterZone.screenshot({ path: testInfo.outputPath('poster-error-card.png') })
  await page.getByRole('button', { name: 'Повторить' }).click()
  await expect(page.locator('[aria-label^="Загрузка "]')).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('poster-uploading.png'), fullPage: true })
  await posterZone.screenshot({ path: testInfo.outputPath('poster-uploading-card.png') })
  await expect(page.getByText('Афиша загружена')).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('poster-upload-ready.png'), fullPage: true })
  await posterZone.screenshot({ path: testInfo.outputPath('poster-ready-card.png') })
  await page.getByRole('button', { name: 'Проверить перед отправкой' }).click()
  await expect(page.getByText(/01\.06\.2099, 10:00/)).toBeVisible()
  await expect(page.getByRole('img', { name: 'Афиша: Вечер живого джаза' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('event-submission-form.png'), fullPage: true })
  await page.screenshot({ path: testInfo.outputPath('event-submission-form-viewport.png') })
  expect(backend.uploadAttempts).toBe(2)
  await page.getByRole('button', { name: 'Отправить на модерацию' }).click()
  await expect(page.getByRole('heading', { name: 'Отправлено на модерацию' })).toBeVisible()
  expect(backend.payload).toMatchObject({ starts_at_local: '2099-06-01T10:00', ends_at_local: '2099-06-01T13:30', category_slug: 'concerts', age_rating: '0+', image_id: posterId, price_from_minor: 199982, ticket_url: 'https://tickets.example/jazz' })
  expect(backend.payload).not.toHaveProperty('image_url')

  page.on('dialog', (dialog) => dialog.accept())
  await page.goto('/internal/event-moderation')
  await expect(page.getByRole('heading', { name: 'Очередь модерации' })).toBeVisible()
  await page.getByRole('button', { name: /Вечер живого джаза/ }).click()
  await expect(page.getByRole('heading', { name: 'Вечер живого джаза' })).toBeVisible()
  await expect(page.getByRole('img', { name: /Афиша/ })).toBeVisible()
  await expect(page.getByText('Возможные дубликаты')).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('event-submission-moderation.png'), fullPage: true })
  await page.getByRole('button', { name: 'Одобрить и опубликовать' }).click()
  await expect(page.getByRole('status')).toContainText('Событие одобрено')
  await page.getByRole('button', { name: 'Одобрено' }).click()
  await page.getByRole('button', { name: /Вечер живого джаза/ }).click()
  await expect(page.getByText('01.06.2099, 13:30 · Europe/Moscow')).toBeVisible()
  await expect(page.getByText('2 янв. 2099 г., 03:00')).toBeVisible()
  await expect(page.getByText('moderator-e2e')).toBeVisible()

  await page.goto('/my/event-submissions')
  await expect(page.getByText('Одобрено')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Открыть в афише' })).toBeVisible()
})
