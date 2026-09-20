import { expect, test, type Page } from '@playwright/test'

const user = {
  id: 'e2e-user',
  display_name: 'Е2Е пользователь',
  avatar_url: null,
  city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24',
  locale: 'ru',
}

const preferences = {
  city_id: user.city_id,
  interest_slugs: ['concerts', 'exhibitions', 'food'],
  budget_max_minor: 350_000,
  usual_day_types: ['weekend'],
  usual_time_slots: ['evening'],
  version: 1,
  updated_at: '2026-01-01T00:00:00.000Z',
}

async function stubBackend(page: Page, initialState: 'new' | 'complete' = 'new') {
  let onboardingState = initialState
  let storedPreferences = initialState === 'complete' ? preferences : null
  let preferencesWrites = 0

  await page.route('**/api/v1/auth/max/bootstrap', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ access_token: 'e2e-access-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: onboardingState, preferences: storedPreferences, invite_context: null }) })
  })

  await page.route('**/api/v1/me/preferences', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    const body = route.request().postDataJSON() as typeof preferences
    preferencesWrites += 1
    storedPreferences = { ...preferences, ...body, version: 1, updated_at: '2026-01-01T00:00:00.000Z' }
    onboardingState = 'complete'
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(storedPreferences) })
  })

  return { get preferencesWrites() { return preferencesWrites } }
}

async function rejectRoomRequests(page: Page) {
  await page.route('**/api/v1/rooms**', async (route) => { throw new Error(`Room API must not be called: ${route.request().method()} ${route.request().url()}`) })
  await page.route('**/api/v1/room-invites**', async (route) => { throw new Error(`Room invite API must not be called: ${route.request().method()} ${route.request().url()}`) })
}

test('real bootstrap and preferences work with mock discovery', async ({ page }) => {
  const backend = await stubBackend(page)
  await rejectRoomRequests(page)

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Что вам интересно?' })).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByRole('heading', { name: 'Настроим подборку' })).toBeVisible()
  await page.getByRole('button', { name: 'Сформировать подборку' }).click()

  await expect.poll(() => backend.preferencesWrites).toBe(1)
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText('Для вас')).toBeVisible()
})

test('catalog uses local discovery and remains usable without room API', async ({ page }) => {
  await stubBackend(page, 'complete')
  await rejectRoomRequests(page)

  await page.goto('/events')
  await expect(page.getByRole('heading', { name: 'События' })).toBeVisible()
  await page.getByRole('searchbox', { name: 'Найти событие' }).fill('джаз')
  await expect(page.getByText('Джазовый вечер в Оранжерее')).toBeVisible()
})

test('room routes explain that the scenario is unavailable without making API calls', async ({ page }) => {
  await stubBackend(page, 'complete')
  await rejectRoomRequests(page)

  await page.goto('/rooms/new')
  await expect(page.getByText('Сценарий комнат временно недоступен')).toBeVisible()
})
