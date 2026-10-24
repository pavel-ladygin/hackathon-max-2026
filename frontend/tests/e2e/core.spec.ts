import { expect, test, type Page } from '@playwright/test'

const user = { id: 'e2e-user', display_name: 'Е2Е пользователь', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const preferences = { city_id: user.city_id, interest_slugs: ['concerts', 'exhibitions', 'food'], budget_max_minor: 350_000, usual_day_types: ['weekend'], usual_time_slots: ['evening'], version: 1, updated_at: '2026-01-01T00:00:00.000Z' }
const event = { id: 'event-1', title: 'Джазовый вечер в Оранжерее', subtitle: 'Живой концерт', category_slug: 'concerts', starts_at: '2026-01-01T19:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января, 19:00', venue_name: 'Оранжерея', distance_m: 1200, distance_label: '1,2 км', price_from_minor: 150000, currency: 'RUB', price_label: 'от 1 500 ₽', image_url: null, saved: false, reasons: [] }

const openApp = (page: Page, path: string) => page.goto(path, { waitUntil: 'domcontentloaded' })

async function stubBackend(page: Page, initialState: 'new' | 'complete' = 'new') {
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  let onboardingState = initialState
  let storedPreferences = initialState === 'complete' ? preferences : null
  let preferencesWrites = 0
  let eventSaved = false
  const roomId = 'room-e2e'
  const room = { id: roomId, name: 'Куда идём?', city_id: user.city_id, state: 'collecting_intents', round_no: 1, version: 1, participants: [{ id: user.id, display_name: user.display_name, avatar_url: null, role: 'creator', intent_ready: false }], my_intent: null, pool: null, match: null, invite: { url: 'http://127.0.0.1:4173/join/invite-e2e', max_deep_link: 'https://max.ru/t255_hakaton_max_bot?startapp=invite-e2e', expires_at: '2030-01-01T00:00:00Z' }, allowed_actions: ['edit_intent', 'invite'], created_at: '2026-01-01T00:00:00Z', expires_at: '2030-01-01T00:00:00Z' }

  await page.route('**/api/v1/auth/max/bootstrap', async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ access_token: 'e2e-access-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: onboardingState, preferences: storedPreferences, invite_context: null }) }))
  await page.route('**/api/v1/me/preferences', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    const body = route.request().postDataJSON() as typeof preferences
    preferencesWrites += 1; storedPreferences = { ...preferences, ...body }; onboardingState = 'complete'
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(storedPreferences) })
  })
  await page.route('**/api/v1/feed/home**', async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ feed_id: 'feed-e2e', generated_at: '2026-01-01T00:00:00Z', sections: [{ type: 'for_you', title: 'Для вас', items: [{ ...event, saved: eventSaved }] }], active_room: null }) }))
  await page.route(/\/api\/v1\/events\/search(?:\?.*)?$/, async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ ...event, saved: eventSaved }], applied_filters: {}, total_estimate: null, next_cursor: null }) }))
  await page.route('**/api/v1/events/search/count**', (route) => route.fulfill({ json: { total: 1 } }))
  await page.route(`**/api/v1/events/${event.id}`, async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...event, saved: eventSaved, description: 'Вечер живой музыки в центре Москвы.', ends_at: '2026-01-01T21:00:00Z', venue: { id: 'venue-1', name: event.venue_name, address: 'Москва, ул. Пример, 1', latitude: 55.75, longitude: 37.61, metro: 'Тверская', district: 'ЦАО' }, images: [], ticket_available: true, status: 'published', age_rating: '16+', data_provenance: { source: 'demo', source_updated_at: null, is_demo: true } }) }))
  await page.route(`**/api/v1/me/saved-events/${event.id}`, async (route) => {
    eventSaved = Boolean((route.request().postDataJSON() as { saved?: boolean }).saved)
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ event_id: event.id, saved: eventSaved, saved_at: eventSaved ? '2026-01-01T00:00:00Z' : null }) })
  })
  await page.route(/\/api\/v1\/me\/saved-events(?:\?.*)?$/, async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: eventSaved ? [{ event: { ...event, saved: true }, saved_at: '2026-01-01T00:00:00Z', match: null }] : [], next_cursor: null }) }))
  await page.route('**/api/v1/behavior/events:batch', async (route) => route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ accepted: 1, duplicates: 0, rejected: 0 }) }))
  await page.route(`**/api/v1/rooms/${roomId}`, async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(room) }))
  await page.route('**/api/v1/rooms', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ room, invite: room.invite }) })
  })
  return { get preferencesWrites() { return preferencesWrites }, get savedPreferences() { return storedPreferences }, roomId }
}

test('real bootstrap and preferences work with HTTP discovery', async ({ page }) => {
  const backend = await stubBackend(page)
  await openApp(page, '/')
  await expect(page.getByRole('heading', { name: 'Что вам интересно?' })).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByRole('heading', { name: 'Настроим подборку' })).toBeVisible()
  await page.getByRole('button', { name: 'Ваш город' }).click()
  await page.getByRole('option', { name: /Санкт-Петербург/ }).click()
  await expect(page.getByRole('button', { name: 'Ваш город' })).toContainText('Санкт-Петербург')
  await page.getByRole('button', { name: 'Сформировать подборку' }).click()
  await expect.poll(() => backend.preferencesWrites).toBe(1)
  expect(backend.savedPreferences?.city_id).toBe('bf61fbf5-8c5b-53da-96c7-3560df3f8177')
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText('Для вас')).toBeVisible()
})

test('catalog uses the real HTTP discovery endpoints', async ({ page }) => {
  await stubBackend(page, 'complete')
  const countResponse = page.waitForResponse((response) => new URL(response.url()).pathname === '/api/v1/events/search/count')
  await openApp(page, '/events')
  await expect(page.getByRole('heading', { name: 'События' })).toBeVisible()
  expect(await (await countResponse).json()).toEqual({ total: 1 })
  await expect(page.getByText('1 найдено', { exact: true })).toBeVisible()
  await page.getByRole('searchbox', { name: 'Найти событие' }).fill('джаз')
  await expect(page.getByText('Джазовый вечер в Оранжерее')).toBeVisible()
})

test('creates a room and opens the invite route', async ({ page }) => {
  const backend = await stubBackend(page, 'complete')
  await openApp(page, '/rooms/new')
  await expect(page.getByRole('heading', { name: 'Создать комнату' })).toBeVisible()
  await page.getByRole('button', { name: 'Создать комнату' }).click()
  await expect(page).toHaveURL(new RegExp(`/rooms/${backend.roomId}/invite$`))
  await expect(page.getByRole('heading', { name: 'Пригласите друга' })).toBeVisible()
  await expect(page.getByTestId('invite-url')).toContainText('/join/invite-e2e')
})

test('room calendar drafts multiple dates, removes selected dates, and fits narrow screens', async ({ page }) => {
  await stubBackend(page, 'complete')
  await page.clock.install({ time: new Date('2099-11-20T12:00:00Z') })
  await openApp(page, '/rooms/new')
  await page.getByRole('checkbox', { name: /Добавить публичное предложение/ }).check()
  await expect(page.getByText('Быстрый выбор', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Создать комнату' }).click()
  const errorSummary = page.locator('[data-error-summary]')
  await expect(errorSummary).toBeVisible()
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    const summaryBox = await errorSummary.boundingBox()
    const nameBox = await page.getByLabel('Название комнаты').boundingBox()
    expect(summaryBox!.y + summaryBox!.height).toBeLessThan(nameBox!.y)
  }
  const calendar = page.getByRole('button', { name: 'Выбор дат', exact: true })
  await calendar.click()
  await page.getByRole('gridcell', { name: '20 ноября 2099 г.', exact: true }).click()
  await page.getByRole('gridcell', { name: '24 ноября 2099 г.', exact: true }).click()
  await page.getByRole('button', { name: 'Следующий месяц' }).click()
  await page.getByRole('gridcell', { name: '5 декабря 2099 г.', exact: true }).click()
  await page.getByRole('button', { name: 'Применить' }).click()
  const selected = page.getByRole('button', { name: /^Удалить / })
  await expect(selected).toHaveCount(3)
  const first = selected.first()
  await expect(first).toHaveCSS('background-color', 'rgb(23, 23, 22)')
  await first.click()
  await expect(selected).toHaveCount(2)
  await calendar.click()
  await page.getByRole('button', { name: 'Очистить' }).click()
  await page.keyboard.press('Escape')
  await expect(selected).toHaveCount(2)
  await calendar.click()
  await expect(page.getByRole('gridcell', { name: '24 ноября 2099 г.', exact: true })).toHaveAttribute('aria-selected', 'true')
  const dialogBox = await page.getByRole('dialog', { name: 'Выбор дат' }).boundingBox()
  expect(dialogBox!.x).toBeGreaterThanOrEqual(0)
  expect(dialogBox!.x + dialogBox!.width).toBeLessThanOrEqual(390)
})

test('home room CTA opens the real creation flow', async ({ page }) => {
  const backend = await stubBackend(page, 'complete')
  await openApp(page, '/')
  await page.getByRole('button', { name: 'Создать комнату' }).click()
  await expect(page).toHaveURL(/\/rooms\/new$/)
  await expect(page.getByRole('heading', { name: 'Создать комнату' })).toBeVisible()
  await page.getByRole('button', { name: 'Создать комнату' }).click()
  await expect(page).toHaveURL(new RegExp(`/rooms/${backend.roomId}/invite$`))
})

test('catalog filters, save action and saved navigation form one flow', async ({ page }) => {
  await stubBackend(page, 'complete')
  await openApp(page, '/events')
  await page.getByRole('button', { name: 'Фильтры' }).click()
  await page.getByRole('button', { name: 'Концерты' }).click()
  await expect(page.getByRole('button', { name: 'Фильтры · 1' })).toBeVisible()
  await page.getByRole('button', { name: /Сохранить «Джазовый вечер/ }).click()
  await page.getByRole('button', { name: /Избранное/ }).click()
  await expect(page).toHaveURL(/\/saved$/)
  await expect(page.getByText('Джазовый вечер в Оранжерее')).toBeVisible()
})

test('empty saved state returns to the catalog', async ({ page }) => {
  await stubBackend(page, 'complete')
  await openApp(page, '/saved')
  await expect(page.getByRole('heading', { name: 'Сохранённых событий пока нет' })).toBeVisible()
  await page.getByRole('button', { name: 'Открыть афишу' }).click()
  await expect(page).toHaveURL(/\/events$/)
})

test('event detail describes the external ticket action without opening it', async ({ page }) => {
  await stubBackend(page, 'complete')
  await openApp(page, `/events/${event.id}`)
  await expect(page.getByText('Концерт', { exact: true })).toBeVisible()
  await expect(page.getByText('Билетный сервис откроется во внешнем окне.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' })).toBeVisible()
})

test('navigation, favorite controls and warm hover keep the agreed geometry', async ({ page }) => {
  await stubBackend(page, 'complete')
  await openApp(page, '/events')

  const navItems = page.getByRole('navigation', { name: 'Основная навигация' }).getByRole('button')
  await expect(navItems).toHaveCount(3)
  const navBoxes = await navItems.evaluateAll((items) => items.map((item) => item.getBoundingClientRect().toJSON()))
  expect(navBoxes.every((box) => box.height >= 48)).toBe(true)
  expect(Math.max(...navBoxes.map((box) => box.width)) - Math.min(...navBoxes.map((box) => box.width))).toBeLessThan(1)

  const saveButton = page.getByRole('button', { name: /Сохранить «Джазовый вечер/ })
  await expect(saveButton).toHaveAttribute('aria-pressed', 'false')
  await expect(saveButton).toHaveCSS('border-radius', '14px')
  expect(await saveButton.evaluate((button) => button.getBoundingClientRect().width)).toBe(40)

  const supportsHover = await page.evaluate(() => matchMedia('(hover: hover) and (pointer: fine)').matches)
  if (supportsHover) {
    const filter = page.getByRole('button', { name: 'Фильтры' })
    const before = await filter.evaluate((button) => getComputedStyle(button).backgroundColor)
    await filter.hover()
    await expect.poll(() => filter.evaluate((button) => getComputedStyle(button).backgroundColor)).not.toBe(before)
  }

  await openApp(page, `/events/${event.id}`)
  const back = page.getByRole('button', { name: 'Назад' })
  const detailFavorite = page.getByRole('button', { name: 'Сохранить событие' })
  expect(await back.evaluate((button) => button.getBoundingClientRect().width)).toBe(48)
  expect(await detailFavorite.evaluate((button) => button.getBoundingClientRect().width)).toBe(48)
  await expect(detailFavorite).toHaveAttribute('aria-pressed', 'false')
})
