import { expect, test, type Page } from '@playwright/test'

async function stubCatalog(page: Page) {
  const searches: URLSearchParams[] = []
  const counts: URLSearchParams[] = []
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({ json: {
    access_token: 'catalog-dates-token', token_type: 'Bearer', expires_in: 3600,
    user: { id: 'catalog-dates-user', display_name: 'Павел', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' },
    onboarding_state: 'complete', preferences: null, invite_context: null,
  } }))
  await page.route(/\/api\/v1\/events\/search(?:\?.*)?$/, (route) => {
    searches.push(new URL(route.request().url()).searchParams)
    return route.fulfill({ json: { items: [], applied_filters: {}, total_estimate: null, next_cursor: null } })
  })
  await page.route('**/api/v1/events/search/count**', (route) => {
    counts.push(new URL(route.request().url()).searchParams)
    return route.fulfill({ json: { total: 0 } })
  })
  await page.route('**/api/v1/behavior/events:batch', (route) => route.fulfill({ json: { accepted: 1, duplicates: 0, rejected: 0 } }))
  return { searches, counts }
}

async function pickDate(page: Page, field: 'С' | 'По', day: number) {
  await page.getByRole('button', { name: field, exact: true }).click()
  await page.getByRole('gridcell', { name: `${day} октября 2026 г.` }).click()
  await page.getByRole('button', { name: 'Применить', exact: true }).click()
}

test('catalog blocks past city dates and sends a valid range to search and count', async ({ page }) => {
  // It is still October 24 in UTC, but already October 25 in Moscow.
  await page.clock.install({ time: new Date('2026-10-24T21:30:00Z') })
  const backend = await stubCatalog(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: 'Фильтры', exact: true }).click()
  for (const field of ['С', 'По']) {
    await page.getByRole('button', { name: field, exact: true }).click()
    await expect(page.getByRole('gridcell', { name: '24 октября 2026 г.' })).toBeDisabled()
    await expect(page.getByRole('gridcell', { name: '25 октября 2026 г.' })).toBeEnabled()
    await page.getByRole('button', { name: 'Закрыть календарь' }).click()
  }
  await pickDate(page, 'С', 25)
  await pickDate(page, 'По', 26)
  for (const requests of [backend.searches, backend.counts]) {
    await expect.poll(() => requests.some((params) => params.get('date_from') === '2026-10-25' && params.get('date_to') === '2026-10-26')).toBe(true)
    expect(requests.every((params) => !['date_from', 'date_to'].some((key) => { const value = params.get(key); return value !== null && value < '2026-10-25' }))).toBe(true)
  }
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('catalog catches a stale calendar draft at midnight and resumes only after correction', async ({ page }, testInfo) => {
  await page.clock.install({ time: new Date('2026-10-25T20:59:00Z') })
  const backend = await stubCatalog(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: 'Фильтры', exact: true }).click()
  await page.getByRole('button', { name: 'С', exact: true }).click()
  await page.getByRole('gridcell', { name: '25 октября 2026 г.' }).click()
  await page.clock.fastForward(65_000)
  await expect(page.getByRole('gridcell', { name: '25 октября 2026 г.' })).toBeDisabled()
  await page.getByRole('button', { name: 'Применить', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Дата начала уже прошла — выберите сегодня или более поздний день')
  await expect(page.getByRole('button', { name: 'С', exact: true })).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByRole('heading', { name: 'Ничего не нашли' })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('expired-catalog-date.png'), fullPage: true })
  await page.getByRole('tab', { name: 'Карта', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'События на карте' })).toHaveCount(0)
  expect([...backend.searches, ...backend.counts].every((params) => !params.has('date_from'))).toBe(true)
  await page.getByRole('tab', { name: 'Список', exact: true }).click()
  await pickDate(page, 'С', 26)
  await expect(page.getByRole('alert')).toHaveCount(0)
  await expect.poll(() => backend.searches.some((params) => params.get('date_from') === '2026-10-26')).toBe(true)
})
