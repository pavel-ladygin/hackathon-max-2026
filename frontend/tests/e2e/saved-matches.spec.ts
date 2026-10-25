import { expect, test, type Page, type TestInfo } from '@playwright/test'

const user = { id: 'matched-event-user', display_name: 'Павел', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const cards = [
  { id: 'past-match', title: 'Прошедший джазовый концерт', starts_at: '2020-10-24T16:00:00Z', date_label: '24 октября 2020, 19:00' },
  { id: 'future-match', title: 'Будущий джазовый концерт', starts_at: '2099-11-20T16:00:00Z', date_label: '20 ноября 2099, 19:00' },
  { id: 'removed-match', title: 'Недоступный будущий концерт', starts_at: '2099-11-21T16:00:00Z', date_label: '21 ноября 2099, 19:00' },
  { id: 'retry-match', title: 'Концерт с временной ошибкой', starts_at: '2099-11-22T16:00:00Z', date_label: '22 ноября 2099, 19:00' },
].map((event) => ({
  ...event, subtitle: 'Живая музыка', category_slug: 'concerts', timezone: 'Europe/Moscow', venue_name: 'Оранжерея, Москва',
  currency: 'RUB', price_from_minor: 150000, price_label: 'от 1 500 ₽', image_url: null, saved: false, reasons: [],
  distance_m: null, distance_label: null,
}))

async function stubMatches(page: Page) {
  const detailCalls: Record<string, number> = {}
  let serviceUnavailable = true
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({ json: {
    access_token: 'matched-event-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences: null, invite_context: null,
  } }))
  await page.route(/\/api\/v1\/me\/saved-events(?:\?.*)?$/, (route) => route.fulfill({ json: {
    items: cards.map((event) => ({ event, saved_at: null, match: { id: `match-${event.id}`, room_id: 'old-room', event_id: event.id, matched_at: '2020-10-01T12:00:00Z', participants: [] } })), next_cursor: null,
  } }))
  await page.route(/\/api\/v1\/events\/[^/?]+$/, (route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-1)!
    detailCalls[id] = (detailCalls[id] ?? 0) + 1
    const event = cards.find((item) => item.id === id)
    if (!event || id === 'past-match' || id === 'removed-match') return route.fulfill({ status: 404, json: { error: { code: 'NOT_FOUND', message: 'Event not found' } } })
    if (id === 'retry-match' && serviceUnavailable) return route.fulfill({ status: 503, json: { error: { code: 'UNAVAILABLE', message: 'Unavailable' } } })
    return route.fulfill({ json: {
      ...event, description: 'Полное описание будущего мероприятия', ends_at: '2099-11-22T20:00:00Z',
      venue: { id: 'venue-match', name: 'Оранжерея', address: 'Москва, Большая Никитская, 24', latitude: null, longitude: null, metro: null, district: null },
      images: [], ticket_available: true, status: 'published', age_rating: null, data_provenance: { source: 'submission-fixture', source_updated_at: null, is_demo: false },
    } })
  })
  await page.route('**/api/v1/behavior/events:batch', (route) => route.fulfill({ status: 202, json: { accepted: 1, duplicates: 0, rejected: 0 } }))
  return { detailCalls, restoreService: () => { serviceUnavailable = false } }
}

test('past match shows a stable splash after reload and returns to the matches tab', async ({ page }, testInfo: TestInfo) => {
  const backend = await stubMatches(page)
  await page.goto('/saved?tab=matches', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: /Прошедший джазовый концерт/ }).click()
  await expect(page).toHaveURL(/\/events\/past-match$/)
  const title = page.getByRole('heading', { name: 'Мероприятие уже началось или завершилось' })
  await expect(title).toBeVisible()
  await expect(page.getByRole('group', { name: 'Информация о событии' })).toContainText('Прошедший джазовый концерт')
  await expect(page.locator('time')).toContainText('24 октября 2020')
  await expect(page.locator('time')).toContainText('19:00')
  await expect(page.getByRole('button', { name: /Открыть билеты/ })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('past-match-splash.png'), fullPage: true })
  await page.reload({ waitUntil: 'domcontentloaded' })
  await expect(title).toBeVisible()
  expect(backend.detailCalls['past-match'] ?? 0).toBe(0)
  await page.getByRole('button', { name: 'Назад', exact: true }).click()
  await expect(page).toHaveURL(/\/saved\?tab=matches$/)
  await expect(page.getByRole('tab', { name: 'Мэтчи' })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('button', { name: /Прошедший джазовый концерт/ }).click()
  await page.getByRole('button', { name: 'Вернуться к мэтчам' }).click()
  await expect(page).toHaveURL(/\/saved\?tab=matches$/)
})

test('future match opens the normal event detail with available tickets', async ({ page }) => {
  const backend = await stubMatches(page)
  await page.goto('/saved?tab=matches', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: /Будущий джазовый концерт/ }).click()
  await expect(page.getByRole('heading', { name: 'Будущий джазовый концерт' })).toBeVisible()
  await expect(page.getByText('Полное описание будущего мероприятия')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' })).toBeEnabled()
  await expect(page.getByText('Этот мэтч остаётся в вашей истории')).toHaveCount(0)
  expect(backend.detailCalls['future-match']).toBeGreaterThan(0)
  await page.getByRole('button', { name: 'Назад', exact: true }).click()
  await expect(page).toHaveURL(/\/saved\?tab=matches$/)
})

test('unavailable future match keeps its summary and a return to matches after reload', async ({ page }) => {
  await stubMatches(page)
  await page.goto('/saved?tab=matches', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: /Недоступный будущий концерт/ }).click()
  await expect(page.getByRole('heading', { name: 'Событие больше недоступно' })).toBeVisible()
  await expect(page.getByRole('group', { name: 'Информация о событии' })).toContainText('Недоступный будущий концерт')
  await expect(page.getByText('Событие не найдено')).toHaveCount(0)
  await page.reload({ waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Событие больше недоступно' })).toBeVisible()
  await page.getByRole('button', { name: 'Вернуться к мэтчам' }).click()
  await expect(page).toHaveURL(/\/saved\?tab=matches$/)
})

test('temporary service errors remain retryable and do not mark the match unavailable', async ({ page }) => {
  const backend = await stubMatches(page)
  await page.goto('/saved?tab=matches', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: /Концерт с временной ошибкой/ }).click()
  await expect(page.getByRole('heading', { name: 'Не удалось загрузить событие' })).toBeVisible()
  await expect(page.getByText('Событие больше недоступно')).toHaveCount(0)
  backend.restoreService()
  await page.getByRole('button', { name: 'Повторить' }).click()
  await expect(page.getByRole('heading', { name: 'Концерт с временной ошибкой' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' })).toBeEnabled()
})

test('direct event links without match context retain the ordinary not-found state', async ({ page }) => {
  await stubMatches(page)
  await page.goto('/events/removed-match', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Событие не найдено' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Вернуться к мэтчам' })).toHaveCount(0)
})
