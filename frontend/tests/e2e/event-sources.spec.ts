import { expect, test } from '@playwright/test'

const api = '/api/v1/internal/event-sources'

test('internal event sources: create, preview, edit, sync, and disable', async ({ page }) => {
  const recorded: { method: string; path: string; body?: Record<string, unknown>; headers: Record<string, string> }[] = []
  const builtIn = { kind: 'built_in', source_key: 'kudago', name: 'KudaGo', enabled: true, event_count: 485, last_sync_state: 'succeeded', last_sync_at: '2026-09-27T10:00:00Z' }
  let source: Record<string, unknown> | null = null
  let syncPoll = 0

  await page.route(`**${api}**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const method = request.method()
    const body = method === 'POST' || method === 'PATCH' ? request.postDataJSON() as Record<string, unknown> : undefined
    recorded.push({ method, path, body, headers: request.headers() })

    if (path === api && method === 'GET') {
      const current = source ? [{ kind: 'generic', ...source, ...(syncPoll > 0 && source.enabled === true ? { last_sync_state: syncPoll === 1 ? 'running' : 'succeeded', last_sync_at: syncPoll === 1 ? '2026-09-28T10:01:00Z' : '2026-09-28T10:02:00Z' } : {}) }] : []
      if (syncPoll > 0) syncPoll += 1
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ sources: [builtIn, ...current] }) })
      return
    }
    if (path === `${api}/test` && method === 'POST') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ connection_ok: true, http_status: 200, received: 1, valid: 1, invalid: 0, complete: true, errors: [], preview: [{ title: 'Autumn jazz', starts_at: '2026-10-03T19:00:00+03:00', venue: 'Greenhouse', price_from_minor: 150000 }] }) })
      return
    }
    if (path === api && method === 'POST') {
      source = { ...body, id: 'source-1', secret_configured: Boolean(body?.auth_secret), mapping_locked: false, created_at: '2026-09-28T10:00:00Z', updated_at: '2026-09-28T10:00:00Z', last_sync_state: null, last_sync_at: null }
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ source }) })
      return
    }
    if (path === `${api}/source-1` && method === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ source }) })
      return
    }
    if (path === `${api}/source-1` && method === 'PATCH') {
      source = { ...source, ...body, secret_configured: Boolean(source?.secret_configured || body?.auth_secret), updated_at: '2026-09-28T10:03:00Z' }
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ source }) })
      return
    }
    if (path === `${api}/source-1/sync` && method === 'POST') {
      syncPoll = 1
      await route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ status: 'started' }) })
      return
    }
    await route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: { message: 'not mocked' } }) })
  })

  await page.goto('/internal/event-sources')
  await expect(page.getByRole('heading', { name: 'Источники событий' }).first()).toBeVisible()
  await expect(page.getByRole('heading', { name: 'KudaGo' })).toBeVisible()

  await page.getByRole('button', { name: '＋ Добавить источник' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Название', { exact: true }).fill('City Events')
  await dialog.getByLabel('Endpoint URL', { exact: true }).fill('https://events.example.com/api')
  await dialog.getByRole('combobox').nth(0).selectOption('api_key_header')
  await dialog.getByLabel('Имя заголовка', { exact: true }).fill('X-Events-Key')
  await dialog.getByLabel('Секрет', { exact: true }).fill('secret-value')
  await dialog.getByLabel('External ID path', { exact: true }).fill('event.id')
  await dialog.getByLabel('Title path', { exact: true }).fill('event.title')
  await dialog.getByLabel('Start date path', { exact: true }).fill('event.start')
  await dialog.getByLabel('Venue name path', { exact: true }).fill('venue.name')
  await dialog.getByRole('button', { name: 'Проверить' }).click()
  await expect(dialog.getByRole('heading', { name: 'Результат проверки' })).toBeVisible()
  await expect(dialog.getByText('Autumn jazz')).toBeVisible()
  const testRequest = recorded.find((item) => item.path === `${api}/test`)
  expect(testRequest?.headers['x-admin-request']).toBe('1')
  expect(testRequest?.headers['origin']).toBe(new URL(page.url()).origin)
  expect(testRequest?.body?.source_key).toBe('generic:city-events')
  expect((testRequest?.body?.mapping as Record<string, unknown>).price_from).toBeDefined()

  await dialog.getByRole('button', { name: 'Сохранить' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText('City Events')).toBeVisible()
  const createRequest = recorded.find((item) => item.path === api && item.method === 'POST')
  expect(createRequest?.body?.auth_secret).toBe('secret-value')

  await page.getByRole('button', { name: 'Редактировать' }).click()
  const editDialog = page.getByRole('dialog')
  await expect(editDialog.getByLabel('Секрет', { exact: true })).toHaveValue('')
  expect(await editDialog.getByLabel('Секрет', { exact: true }).getAttribute('placeholder')).toContain('Секрет сохранён')
  await editDialog.getByRole('button', { name: 'Сохранить' }).click()
  await expect(editDialog).toBeHidden()
  const patchRequest = recorded.find((item) => item.path === `${api}/source-1` && item.method === 'PATCH')
  expect(patchRequest?.body?.auth_secret).toBe('')
  for (const responseOnlyField of ['id', 'secret_configured', 'mapping_locked', 'created_at', 'updated_at', 'last_sync_state', 'last_sync_at']) {
    expect(patchRequest?.body).not.toHaveProperty(responseOnlyField)
  }

  await page.getByRole('button', { name: 'Синхронизировать' }).click()
  await expect(page.getByRole('status')).toContainText('Синхронизация завершена: succeeded.', { timeout: 15_000 })
  expect(recorded.find((item) => item.path === `${api}/source-1/sync`)?.headers['x-admin-request']).toBe('1')

  await page.getByRole('button', { name: 'Выключить' }).click()
  await expect(page.getByRole('status')).toContainText('выключен')
  const disableRequest = recorded.filter((item) => item.path === `${api}/source-1` && item.method === 'PATCH').at(-1)
  expect(disableRequest?.body?.enabled).toBe(false)
  expect(disableRequest?.body).not.toHaveProperty('id')
  expect(disableRequest?.headers['x-admin-request']).toBe('1')
})
