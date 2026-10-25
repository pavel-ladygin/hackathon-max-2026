import { expect, test, type Page } from '@playwright/test'
import type { RoomSnapshotDto } from '../../src/shared/api/types'

const cityId = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
const user = { id: 'invite-recipient', display_name: 'Анна', avatar_url: null, city_id: cityId, locale: 'ru' }
type BootstrapInput = { init_data: string; start_param: string | null }

async function stubSession(page: Page, status: 'joinable' | 'full' | 'expired' = 'joinable', options: { alreadyJoined?: boolean; roomState?: 'collecting_intents' | 'ranking'; coldInvite?: boolean } = {}) {
  const bootstraps: BootstrapInput[] = []
  let joins = 0
  let joinGate: Promise<void> | null = null
  let bootstrapGate: Promise<void> | null = null
  const ranking = options.roomState === 'ranking'
  const joinedRoom: RoomSnapshotDto = { id: 'room-invite-a', name: 'Вечер с Павлом', city_id: cityId, state: options.roomState ?? 'collecting_intents', round_no: 1, version: 2, participants: [{ id: 'creator', display_name: 'Павел', avatar_url: null, role: 'creator', intent_ready: true }, { ...user, role: 'participant', intent_ready: ranking }], my_intent: ranking ? { dates: ['2099-10-26'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 200_000, radius_m: null, exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2099-10-25T12:00:00Z' } : null, pool: null, match: null, invite: null, allowed_actions: ranking ? ['wait'] : ['edit_intent'], created_at: '2026-10-25T12:00:00Z', expires_at: '2099-01-01T00:00:00Z' }
  await page.addInitScript((coldInvite) => {
    window.WebApp = { initData: coldInvite ? 'signed-invite-a' : 'signed-initial', initDataUnsafe: coldInvite ? { start_param: 'invite-a' } : {}, platform: 'web', ready: () => {} }
  }, options.coldInvite ?? false)
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/max/bootstrap')) {
      const body = route.request().postDataJSON() as BootstrapInput
      bootstraps.push(body)
      const token = body.init_data === 'signed-invite-a' ? 'invite-a' : body.init_data === 'signed-invite-b' ? 'invite-b' : null
      if (body.init_data === 'signed-invite-a' && bootstrapGate) await bootstrapGate
      if (body.init_data === 'invalid-signed-data') return route.fulfill({ status: 401, json: { error: { code: 'UNAUTHORIZED', message: 'Invalid init data' } } })
      return route.fulfill({ json: {
        access_token: 'local-test-session', user, onboarding_state: 'complete', preferences: null,
        daily_notifications_enabled: false, shared_event_id: null,
        invite_context: token ? { token, room_id: `room-${token}`, room_name: token === 'invite-a' ? 'Вечер с Павлом' : 'Вечер с Сергеем', city_id: cityId, public_plan: null, inviter: { id: 'creator', display_name: token === 'invite-a' ? 'Павел' : 'Сергей', avatar_url: null, role: 'creator', intent_ready: true }, expires_at: '2099-01-01T00:00:00Z', already_joined: options.alreadyJoined ?? false, status } : null,
      } })
    }
    if (/\/room-invites\/[^/]+\/join$/.test(path)) { joins += 1; if (joinGate) await joinGate; return route.fulfill({ json: joinedRoom }) }
    if (path.endsWith('/rooms/room-invite-a')) return route.fulfill({ json: joinedRoom })
    if (path.endsWith('/feed/home')) return route.fulfill({ json: { feed_id: 'local-feed', generated_at: '2026-10-25T12:00:00Z', sections: [], active_room: null } })
    if (path.endsWith('/events/search/count')) return route.fulfill({ json: { total: 0 } })
    if (path.endsWith('/events/search')) return route.fulfill({ json: { items: [], total_estimate: 0, next_cursor: null, applied_filters: {} } })
    if (path.endsWith('/behavior/events:batch')) return route.fulfill({ status: 202, json: { accepted: 1, duplicates: 0, rejected: 0 } })
    return route.fulfill({ status: 404, json: { error: { code: 'NOT_FOUND', message: 'No local fixture' } } })
  })
  return { bootstraps, get joins() { return joins }, deferNextJoin() { let release!: () => void; joinGate = new Promise<void>((resolve) => { release = resolve }); return release }, deferFirstInviteBootstrap() { let release!: () => void; bootstrapGate = new Promise<void>(resolve => { release = resolve }); return release } }
}

async function bridgeLaunch(page: Page, token: 'a' | 'b') {
  await page.evaluate((value) => {
    Object.defineProperty(window.WebApp!, 'initData', { configurable: true, value: `signed-invite-${value}` })
    Object.defineProperty(window.WebApp!, 'initDataUnsafe', { configurable: true, value: { start_param: `invite-${value}` } })
    window.dispatchEvent(new Event('focus'))
  }, token)
}

for (const transport of ['bridge', 'hash'] as const) {
  test(`BUG-02 opens a warm invitation from catalog via ${transport}, without joining or redirect loops`, async ({ page }, testInfo) => {
    const session = await stubSession(page)
    await page.goto('/events', { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
    if (transport === 'bridge') await bridgeLaunch(page, 'a')
    else await page.evaluate(() => { location.hash = new URLSearchParams({ WebAppData: 'signed-invite-a' }).toString() })
    await expect(page).toHaveURL(/\/join\/invite-a$/)
    await expect(page.getByRole('heading', { name: 'Вас приглашает Павел' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Присоединиться', exact: true })).toBeEnabled()
    expect(session.joins).toBe(0)
    expect(session.bootstraps.filter((body) => body.init_data === 'signed-invite-a')).toEqual([{ init_data: 'signed-invite-a', start_param: transport === 'bridge' ? 'invite-a' : null }])
    const bootstrapCount = session.bootstraps.length
    await page.screenshot({ path: testInfo.outputPath(`bug02-warm-invite-${transport}.png`), fullPage: true })

    await page.getByRole('button', { name: 'На главную', exact: true }).click()
    await expect(page).toHaveURL(/\/$/)
    await page.getByRole('button', { name: 'Афиша', exact: true }).click()
    await expect(page).toHaveURL(/\/events$/)
    await page.evaluate(() => { window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('pageshow')) })
    await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
    expect(session.bootstraps).toHaveLength(bootstrapCount)
    expect(session.joins).toBe(0)
  })
}

test('BUG-02 a second warm invitation replaces the first on its open page', async ({ page }) => {
  const session = await stubSession(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
  await bridgeLaunch(page, 'a')
  await expect(page.getByRole('heading', { name: 'Вас приглашает Павел' })).toBeVisible()
  await bridgeLaunch(page, 'b')
  await expect(page).toHaveURL(/\/join\/invite-b$/)
  await expect(page.getByRole('heading', { name: 'Вас приглашает Сергей' })).toBeVisible()
  expect(session.bootstraps.filter((body) => body.init_data === 'signed-invite-a')).toHaveLength(1)
  expect(session.bootstraps.filter((body) => body.init_data === 'signed-invite-b')).toHaveLength(1)
  expect(session.joins).toBe(0)
})

for (const roomState of ['collecting_intents', 'ranking'] as const) {
  test(`BUG-02 an existing member returns to the server ${roomState} screen without joining again`, async ({ page }) => {
    const session = await stubSession(page, 'joinable', { alreadyJoined: true, roomState })
    await page.goto('/events', { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('searchbox', { name: 'Найти событие' })).toBeVisible()
    await bridgeLaunch(page, 'a')
    await expect(page).toHaveURL(roomState === 'ranking' ? /\/rooms\/room-invite-a\/waiting$/ : /\/rooms\/room-invite-a\/intent$/)
    await expect(page.getByRole('heading', { name: roomState === 'ranking' ? 'Формируем общий пул…' : 'Мои предпочтения' })).toBeVisible()
    await expect(page.getByText('Комната уже заполнена.', { exact: true })).toHaveCount(0)
    expect(session.joins).toBe(0)
    const bootstrapCount = session.bootstraps.length
    await page.getByRole('button', { name: 'Назад', exact: true }).click()
    await expect(page).toHaveURL(/\/$/)
    await page.evaluate(() => { window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('pageshow')) })
    await expect(page).toHaveURL(/\/$/)
    expect(session.bootstraps).toHaveLength(bootstrapCount)
  })
}

test('BUG-02 a cold reload preserves an already joined room route', async ({ page }) => {
  const session = await stubSession(page, 'joinable', { alreadyJoined: true, roomState: 'ranking', coldInvite: true })
  await page.goto('/rooms/room-invite-a/waiting', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Формируем общий пул…' })).toBeVisible()
  await page.reload({ waitUntil: 'domcontentloaded' })
  await expect(page).toHaveURL(/\/rooms\/room-invite-a\/waiting$/)
  await expect(page.getByRole('heading', { name: 'Формируем общий пул…' })).toBeVisible()
  expect(session.joins).toBe(0)
})

test('BUG-02 a slow old bootstrap cannot replace the latest invitation', async ({ page }) => {
  const session = await stubSession(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('searchbox', { name: 'Найти событие' })).toBeVisible()
  const release = session.deferFirstInviteBootstrap()
  await bridgeLaunch(page, 'a')
  await expect.poll(() => session.bootstraps.some(input => input.init_data === 'signed-invite-a')).toBe(true)
  await bridgeLaunch(page, 'b')
  await expect(page.getByRole('heading', { name: 'Вас приглашает Сергей' })).toBeVisible()
  release()
  await page.waitForLoadState('networkidle')
  await expect(page).toHaveURL(/\/join\/invite-b$/)
  await expect(page.getByRole('heading', { name: 'Вас приглашает Сергей' })).toBeVisible()
  expect(session.joins).toBe(0)
})

test('BUG-02 picks up a delayed bridge update without another focus event', async ({ page }) => {
  const session = await stubSession(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await page.evaluate(() => {
    Object.defineProperty(window.WebApp!, 'initData', { configurable: true, value: 'signed-invite-a' })
    Object.defineProperty(window.WebApp!, 'initDataUnsafe', { configurable: true, value: { start_param: 'invite-a' } })
  })
  await expect(page.getByRole('heading', { name: 'Вас приглашает Павел' })).toBeVisible()
  expect(session.bootstraps.filter((body) => body.init_data === 'signed-invite-a')).toHaveLength(1)
  expect(session.joins).toBe(0)
})

test('BUG-02 an older pending join cannot navigate over the latest invitation', async ({ page }) => {
  const session = await stubSession(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
  await bridgeLaunch(page, 'a')
  await expect(page.getByRole('heading', { name: 'Вас приглашает Павел' })).toBeVisible()
  const releaseJoin = session.deferNextJoin()
  await page.getByRole('button', { name: 'Присоединиться', exact: true }).click()
  await expect.poll(() => session.joins).toBe(1)
  await bridgeLaunch(page, 'b')
  await expect(page.getByRole('heading', { name: 'Вас приглашает Сергей' })).toBeVisible()
  const oldJoinResponse = page.waitForResponse((response) => new URL(response.url()).pathname.endsWith('/room-invites/invite-a/join'))
  releaseJoin()
  await (await oldJoinResponse).finished()
  await page.waitForLoadState('networkidle')
  await expect(page).toHaveURL(/\/join\/invite-b$/)
  await expect(page.getByRole('heading', { name: 'Вас приглашает Сергей' })).toBeVisible()
})

for (const status of ['full', 'expired'] as const) {
  test(`BUG-02 respects server ${status} status for a new invitation`, async ({ page }) => {
    const session = await stubSession(page, status)
    await page.goto('/events', { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
    await bridgeLaunch(page, 'a')
    await expect(page.getByRole('heading', { name: 'Вас приглашает Павел' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Присоединиться', exact: true })).toBeDisabled()
    await expect(page.getByText(status === 'full' ? 'Комната уже заполнена.' : 'Срок приглашения истёк.', { exact: true })).toBeVisible()
    expect(session.joins).toBe(0)
  })
}

test('BUG-02 rejects tampered launch data before displaying an invitation', async ({ page }) => {
  const session = await stubSession(page)
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
  await page.evaluate(() => { location.hash = new URLSearchParams({ WebAppData: 'invalid-signed-data' }).toString() })
  await expect(page.getByRole('heading', { name: 'Не удалось открыть приложение' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Присоединиться', exact: true })).toHaveCount(0)
  expect(session.joins).toBe(0)
})
