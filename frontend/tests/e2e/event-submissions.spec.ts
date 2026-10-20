import { expect, test, type Page, type TestInfo } from '@playwright/test'

const user = { id: 'user-event-test', display_name: 'Е2Е пользователь', avatar_url: null, city_id: 'city-moscow', locale: 'ru' }
const city = { id: 'city-moscow', name: 'Москва', timezone: 'Europe/Moscow' }
const submissionId = 'submission-e2e-1'
const eventId = 'published-e2e-1'
const categories = ['concerts', 'cinema', 'theatre', 'standup', 'exhibitions', 'sports', 'food', 'parties', 'festivals', 'walks', 'other'].map((slug) => ({ slug, label: slug }))

async function setupBackend(page: Page) {
  let state: 'pending' | 'approved' | 'rejected' = 'pending'
  let submittedPayload: Record<string, unknown> | null = null
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({ json: { access_token: 'e2e-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences: null, daily_notifications_enabled: false, invite_context: null } }))
  await page.route('**/api/v1/event-submissions/options', (route) => route.fulfill({ json: { cities: [city], categories } }))
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
  await page.route(`**/api/v1/internal/event-submissions/${submissionId}/approve`, async (route) => { state = 'approved'; await route.fulfill({ json: { id: submissionId, status: state, event_id: eventId } }) })
  return { get payload() { return submittedPayload } }
}

test('submit an event, approve it in moderation, and see it in the user history', async ({ page }, testInfo: TestInfo) => {
  const backend = await setupBackend(page)
  let externalPosterRequests = 0
  await page.route('https://images.example/**', async (route) => { externalPosterRequests += 1; await route.abort() })
  await page.goto('/events/suggest')
  await page.getByLabel('Название').fill('Вечер живого джаза')
  await page.getByLabel('Описание').fill('Камерный концерт с живым джазом в центре города.')
  await page.getByLabel('Город').selectOption(city.id)
  await page.getByLabel('Место').fill('Клуб Сцена')
  await page.getByLabel('Адрес').fill('Москва, улица Тверская, 10')
  await page.getByLabel('Начало').fill('2099-06-01T10:00')
  await page.getByLabel('Категория').selectOption('concerts')
  await page.getByLabel('Ссылка на афишу · необязательно').fill('https://images.example/poster.jpg')
  await page.getByRole('button', { name: 'Проверить перед отправкой' }).click()
  await expect(page.getByText(/01\.06\.2099, 10:00/)).toBeVisible()
  await expect(page.getByText('Фото появится после модерации')).toBeVisible()
  await page.screenshot({ path: `/private/tmp/event-submission-form-${testInfo.project.name}.png`, fullPage: true })
  expect(externalPosterRequests).toBe(0)
  await page.getByRole('button', { name: 'Отправить на модерацию' }).click()
  await expect(page.getByRole('heading', { name: 'Отправлено на модерацию' })).toBeVisible()
  expect(backend.payload).toMatchObject({ starts_at_local: '2099-06-01T10:00', category_slug: 'concerts' })

  page.on('dialog', (dialog) => dialog.accept())
  await page.goto('/internal/event-moderation')
  await expect(page.getByRole('heading', { name: 'Очередь модерации' })).toBeVisible()
  await page.getByRole('button', { name: /Вечер живого джаза/ }).click()
  await expect(page.getByRole('heading', { name: 'Вечер живого джаза' })).toBeVisible()
  await expect(page.getByText('Возможные дубликаты')).toHaveCount(0)
  await page.screenshot({ path: `/private/tmp/event-submission-moderation-${testInfo.project.name}.png`, fullPage: true })
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
