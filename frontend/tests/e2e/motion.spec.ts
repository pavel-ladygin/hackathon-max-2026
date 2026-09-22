import { expect, test, type Page } from '@playwright/test'

const user = { id: 'motion-user', display_name: 'Ирина', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const preferences = { city_id: user.city_id, interest_slugs: ['concerts'], budget_max_minor: 350_000, usual_day_types: ['weekend'], usual_time_slots: ['evening'], version: 1, updated_at: '2026-01-01T00:00:00.000Z' }
const event = { id: 'motion-event', title: 'Джазовый вечер', subtitle: 'Живой концерт', category_slug: 'concerts', starts_at: '2026-01-01T19:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января, 19:00', venue_name: 'Оранжерея', distance_m: 1200, distance_label: '1,2 км', price_from_minor: 150000, currency: 'RUB', price_label: 'от 1 500 ₽', image_url: null, saved: false, reasons: [] }

async function stubMotionBackend(page: Page) {
  let saved = false
  const room = { id: 'motion-room', name: 'Куда идём?', city_id: user.city_id, state: 'collecting_intents', round_no: 1, version: 1, participants: [{ id: user.id, display_name: user.display_name, avatar_url: null, role: 'creator', intent_ready: false }], my_intent: null, pool: null, match: null, invite: { url: 'http://127.0.0.1:4173/join/motion', max_deep_link: 'https://max.ru/test?startapp=motion', expires_at: '2030-01-01T00:00:00Z' }, allowed_actions: ['edit_intent'], created_at: '2026-01-01T00:00:00Z', expires_at: '2030-01-01T00:00:00Z' }
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ access_token: 'motion-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }) }))
  await page.route('**/api/v1/events/search**', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ ...event, saved }], applied_filters: {}, total_estimate: 1, next_cursor: null }) }))
  await page.route(`**/api/v1/me/saved-events/${event.id}`, async (route) => { saved = Boolean((route.request().postDataJSON() as { saved: boolean }).saved); await new Promise((resolve) => setTimeout(resolve, 120)); await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ event_id: event.id, saved, saved_at: saved ? '2026-01-01T00:00:00Z' : null }) }) })
  await page.route('**/api/v1/rooms', async (route) => { if (route.request().method() !== 'POST') return route.fallback(); await new Promise((resolve) => setTimeout(resolve, 180)); await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ room, invite: room.invite }) }) })
  await page.route(`**/api/v1/rooms/${room.id}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(room) }))
}

for (const reducedMotion of ['no-preference', 'reduce'] as const) {
  test(`catalog and room feedback stay functional with motion=${reducedMotion}`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion })
    await stubMotionBackend(page)
    await page.goto('/events')

    const filters = page.getByRole('button', { name: 'Фильтры' })
    await filters.click()
    await expect(filters).toHaveAttribute('aria-expanded', 'true')
    await page.getByRole('button', { name: 'Концерты' }).click()
    await expect(page.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'true')

    const favorite = page.getByRole('button', { name: /Сохранить «Джазовый вечер/ })
    await favorite.click()
    await expect(page.getByRole('button', { name: /Убрать «Джазовый вечер/ })).toHaveAttribute('aria-pressed', 'true')

    await page.getByRole('tab', { name: 'Карта' }).click()
    await expect(page.getByRole('tab', { name: 'Карта' })).toHaveAttribute('aria-selected', 'true')

    await page.goto('/rooms/new')
    const create = page.getByRole('button', { name: 'Создать комнату' })
    await create.click()
    await expect(page.getByRole('button', { name: 'Создаём…' })).toHaveAttribute('aria-busy', 'true')
    if (reducedMotion === 'no-preference') await expect(page.getByRole('button', { name: /Комната создана/ })).toBeVisible()
    await expect(page).toHaveURL(/\/rooms\/motion-room\/invite$/)

    if (reducedMotion === 'reduce') {
      await page.goto('/events')
      const chip = page.getByRole('button', { name: 'Бесплатно' })
      expect(await chip.evaluate((node) => getComputedStyle(node).animationName)).toBe('none')
    }
  })
}
