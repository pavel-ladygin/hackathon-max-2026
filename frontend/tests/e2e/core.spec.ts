import { expect, test, type Page } from '@playwright/test'

const user = { id: 'e2e-user', display_name: 'Е2Е пользователь', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const preferences = { city_id: user.city_id, interest_slugs: ['concerts', 'exhibitions', 'food'], budget_max_minor: 350_000, usual_day_types: ['weekend'], usual_time_slots: ['evening'], version: 1, updated_at: '2026-01-01T00:00:00.000Z' }
const event = { id: 'event-1', title: 'Джазовый вечер в Оранжерее', subtitle: 'Живой концерт', category_slug: 'concerts', starts_at: '2026-01-01T19:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января, 19:00', venue_name: 'Оранжерея', distance_m: 1200, distance_label: '1,2 км', price_from_minor: 150000, currency: 'RUB', price_label: 'от 1 500 ₽', image_url: null, saved: false, reasons: [] }

const openApp = (page: Page, path: string) => page.goto(path, { waitUntil: 'domcontentloaded' })

async function stubBackend(page: Page, initialState: 'new' | 'complete' = 'new') {
  let onboardingState = initialState
  let storedPreferences = initialState === 'complete' ? preferences : null
  let preferencesWrites = 0
  let eventSaved = false
  const roomId = 'room-e2e'
  const room = { id: roomId, name: 'Куда идём?', city_id: user.city_id, state: 'collecting_intents', round_no: 1, version: 1, participants: [{ id: user.id, display_name: user.display_name, avatar_url: null, role: 'creator', intent_ready: false }], my_intent: null, pool: null, match: null, invite: { url: 'http://127.0.0.1:4173/join/invite-e2e', max_deep_link: 'https://max.ru/t255_hakaton_max_bot?startapp=invite-e2e', expires_at: '2030-01-01T00:00:00Z' }, allowed_actions: ['edit_intent'], created_at: '2026-01-01T00:00:00Z', expires_at: '2030-01-01T00:00:00Z' }

  await page.route('**/api/v1/auth/max/bootstrap', async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ access_token: 'e2e-access-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: onboardingState, preferences: storedPreferences, invite_context: null }) }))
  await page.route('**/api/v1/me/preferences', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    const body = route.request().postDataJSON() as typeof preferences
    preferencesWrites += 1; storedPreferences = { ...preferences, ...body }; onboardingState = 'complete'
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(storedPreferences) })
  })
  await page.route('**/api/v1/feed/home**', async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ feed_id: 'feed-e2e', generated_at: '2026-01-01T00:00:00Z', sections: [{ type: 'for_you', title: 'Для вас', items: [{ ...event, saved: eventSaved }] }], active_room: null }) }))
  await page.route('**/api/v1/events/search**', async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ ...event, saved: eventSaved }], applied_filters: {}, total_estimate: 1, next_cursor: null }) }))
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
  return { get preferencesWrites() { return preferencesWrites }, roomId }
}

test('real bootstrap and preferences work with HTTP discovery', async ({ page }) => {
  const backend = await stubBackend(page)
  await openApp(page, '/')
  await expect(page.getByRole('heading', { name: 'Что вам интересно?' })).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByRole('heading', { name: 'Настроим подборку' })).toBeVisible()
  await page.getByRole('button', { name: 'Сформировать подборку' }).click()
  await expect.poll(() => backend.preferencesWrites).toBe(1)
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText('Для вас')).toBeVisible()
})

test('catalog uses the real HTTP discovery endpoints', async ({ page }) => {
  await stubBackend(page, 'complete')
  await openApp(page, '/events')
  await expect(page.getByRole('heading', { name: 'События' })).toBeVisible()
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
  await page.getByRole('button', { name: /Моё/ }).click()
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
