import { expect, test, type Page } from '@playwright/test'

const moscowId = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
const petersburgId = 'bf61fbf5-8c5b-53da-96c7-3560df3f8177'

const initialPreferences = {
  city_id: moscowId,
  interest_slugs: ['concerts', 'food'],
  budget_max_minor: 125_000,
  usual_day_types: ['weekend'],
  usual_time_slots: ['evening'],
  version: 3,
  updated_at: '2026-01-01T00:00:00.000Z',
}

async function stubCityBackend(page: Page, failFirstSave = false) {
  let preferences = { ...initialPreferences }
  let failNextSave = failFirstSave
  const preferenceWrites: Array<Record<string, unknown>> = []
  const homeCityIds: string[] = []
  const searchCityIds: string[] = []
  const countCityIds: string[] = []

  await page.route('**/api/v1/auth/max/bootstrap', async (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      access_token: 'city-e2e-token',
      token_type: 'Bearer',
      expires_in: 3600,
      user: { id: 'city-e2e-user', display_name: 'Городской тест', avatar_url: null, city_id: preferences.city_id, locale: 'ru' },
      onboarding_state: 'complete',
      preferences,
      invite_context: null,
      daily_notifications_enabled: false,
    }),
  }))

  await page.route('**/api/v1/me/preferences', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    const body = route.request().postDataJSON() as Record<string, unknown>
    preferenceWrites.push(body)
    if (failNextSave) {
      failNextSave = false
      return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'UNAVAILABLE', message: 'Try again' }) })
    }
    preferences = { ...preferences, ...body, version: preferences.version + 1, updated_at: '2026-01-02T00:00:00.000Z' }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(preferences) })
  })

  await page.route('**/api/v1/feed/home**', async (route) => {
    const url = new URL(route.request().url())
    homeCityIds.push(url.searchParams.get('city_id') ?? '')
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ feed_id: `feed-${homeCityIds.length}`, generated_at: '2026-01-01T00:00:00Z', sections: [], active_room: null }),
    })
  })
  await page.route(/\/api\/v1\/events\/search(?:\?.*)?$/, async (route) => {
    searchCityIds.push(new URL(route.request().url()).searchParams.get('city_id') ?? '')
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [], applied_filters: {}, total_estimate: 0, next_cursor: null }) })
  })
  await page.route('**/api/v1/events/search/count**', async (route) => {
    countCityIds.push(new URL(route.request().url()).searchParams.get('city_id') ?? '')
    await route.fulfill({ json: { total: 0 } })
  })
  await page.route('**/api/v1/behavior/events:batch', (route) => route.fulfill({ json: { accepted: 1, duplicates: 0, rejected: 0 } }))

  return {
    preferenceWrites,
    homeCityIds,
    searchCityIds,
    countCityIds,
    get savedPreferences() { return preferences },
  }
}

test('home city picker searches aliases, disables future cities, saves the full profile and survives reload', async ({ page }) => {
  const backend = await stubCityBackend(page)
  await page.goto('/', { waitUntil: 'domcontentloaded' })

  const homeCity = page.getByRole('button', { name: 'Изменить город: Москва' })
  await expect(homeCity).toBeVisible()
  await homeCity.click()
  const dialog = page.getByRole('dialog', { name: 'Выберите город' })
  await expect(dialog).toBeVisible()
  await dialog.screenshot({ path: `/private/tmp/city-picker-${test.info().project.name}.png`, animations: 'disabled' })

  const search = page.getByRole('searchbox', { name: 'Найти город' })
  await search.fill('СПб')
  const petersburg = dialog.getByRole('option', { name: /Санкт-Петербург/ })
  await expect(petersburg).toBeEnabled()
  await expect(dialog.getByRole('option', { name: /Казань/ })).toHaveCount(0)
  await search.fill('')

  for (const city of ['Новосибирск', 'Екатеринбург', 'Казань', 'Нижний Новгород']) {
    await expect(dialog.getByRole('option', { name: new RegExp(city) })).toBeDisabled()
    await expect(dialog.getByRole('option', { name: new RegExp(city) })).toContainText('Скоро')
  }

  await dialog.getByRole('option', { name: /Санкт-Петербург/ }).click()
  await expect(page.getByRole('button', { name: 'Изменить город: Санкт-Петербург' })).toBeVisible()
  await expect.poll(() => backend.preferenceWrites).toHaveLength(1)
  expect(backend.preferenceWrites[0]).toEqual({
    city_id: petersburgId,
    interest_slugs: initialPreferences.interest_slugs,
    budget_max_minor: initialPreferences.budget_max_minor,
    usual_day_types: initialPreferences.usual_day_types,
    usual_time_slots: initialPreferences.usual_time_slots,
  })
  await expect.poll(() => backend.homeCityIds.includes(petersburgId)).toBe(true)

  await page.reload({ waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('button', { name: 'Изменить город: Санкт-Петербург' })).toBeVisible()
})

test('catalog city filter uses the same picker and updates search and count requests', async ({ page }) => {
  const backend = await stubCityBackend(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })

  await page.getByRole('button', { name: 'Фильтры' }).click()
  await page.getByRole('button', { name: 'Изменить город: Москва' }).click()
  const dialog = page.getByRole('dialog', { name: 'Выберите город' })
  await page.getByRole('searchbox', { name: 'Найти город' }).fill('питер')
  await dialog.getByRole('option', { name: /Санкт-Петербург/ }).click()

  await expect(page.getByRole('button', { name: 'Изменить город: Санкт-Петербург' })).toBeVisible()
  await expect.poll(() => backend.searchCityIds.filter((id) => id === petersburgId).length).toBeGreaterThan(0)
  await expect.poll(() => backend.countCityIds.filter((id) => id === petersburgId).length).toBeGreaterThan(0)
  expect(backend.savedPreferences.city_id).toBe(petersburgId)
})

test('failed city save keeps the previous city and picker open for retry', async ({ page }) => {
  const backend = await stubCityBackend(page, true)
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: 'Изменить город: Москва' }).click()
  const dialog = page.getByRole('dialog', { name: 'Выберите город' })

  await dialog.getByRole('option', { name: /Санкт-Петербург/ }).click()
  await expect(dialog.getByRole('alert')).toContainText('Не удалось сохранить город')
  await expect(dialog).toBeVisible()
  await expect(page.getByRole('button', { name: 'Изменить город: Москва' })).toBeVisible()

  await dialog.getByRole('option', { name: /Санкт-Петербург/ }).click()
  await expect(page.getByRole('button', { name: 'Изменить город: Санкт-Петербург' })).toBeVisible()
  await expect.poll(() => backend.preferenceWrites).toHaveLength(2)
})
