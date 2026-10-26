import { expect, test, type Page } from '@playwright/test'
import type { RoomSnapshotDto } from '../../src/shared/api/types'

const cityId = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
const roomId = 'bug04-room'
const user = { id: 'bug04-user', display_name: 'Участник', avatar_url: null, city_id: cityId, locale: 'ru' }

type RoomKind = 'active' | 'zero-pool' | 'third-round' | 'error'
type Counters = { joins: number; writes: string[]; bootstraps: number; homeReads: number }

function roomSnapshot(kind: Exclude<RoomKind, 'error'>): RoomSnapshotDto {
  const exhausted = kind !== 'active'
  const thirdRound = kind === 'third-round'
  return {
    id: roomId, name: 'Планы на ноябрь', city_id: cityId,
    state: exhausted ? 'exhausted' : 'collecting_intents', round_no: thirdRound ? 3 : exhausted ? 2 : 1,
    version: thirdRound ? 7 : exhausted ? 5 : 4,
    participants: [
      { id: user.id, display_name: user.display_name, avatar_url: null, role: 'creator', intent_ready: exhausted },
      { id: 'bug04-peer', display_name: 'Собеседник', avatar_url: null, role: 'participant', intent_ready: exhausted },
    ],
    my_intent: exhausted ? { dates: ['2099-11-20'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 350_000, radius_m: 5_000, exclusion_slugs: [], location: null, free_text: null, version: 3, round_no: thirdRound ? 3 : 2, submitted_at: '2099-11-01T10:00:00Z' } : null,
    pool: exhausted ? { version: 5, round_no: thirdRound ? 3 : 2, state: 'exhausted', total: kind === 'zero-pool' ? 0 : 4, voted_by_me: kind === 'zero-pool' ? 0 : 4, my_pool_finished: true, room_exhausted: true, retry_after_seconds: null, exhaustion_reasons: [] } : null,
    match: null, invite: null, allowed_actions: exhausted ? thirdRound ? [] : ['restart_with_new_intent'] : ['edit_intent'],
    created_at: '2099-11-01T00:00:00Z', expires_at: '2099-12-01T00:00:00Z',
  }
}

async function fixture(page: Page, options: { room?: RoomKind; inviteStatus?: 'full' | 'expired' | 'joinable'; inviteAlreadyJoined?: boolean; initialInvite?: boolean } = {}) {
  const counters: Counters = { joins: 0, writes: [], bootstraps: 0, homeReads: 0 }
  let roomKind = options.room ?? 'active'
  let releaseJoin: (() => void) | undefined
  let releaseSave: (() => void) | undefined
  let joinGate: Promise<void> | undefined
  let saveGate: Promise<void> | undefined
  let persistedRoom = options.room && options.room !== 'error' ? roomSnapshot(options.room) : roomSnapshot('active')
  let savedIntent: Record<string, unknown> | null = null
  let joined = false
  await page.addInitScript((initialInvite) => { window.WebApp = { initData: initialInvite ? 'signed-a' : 'bug04-initial', initDataUnsafe: initialInvite ? { start_param: 'bug04-invite' } : {}, platform: 'web', ready: () => {} } }, Boolean(options.initialInvite))
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path.endsWith('/auth/max/bootstrap')) {
      counters.bootstraps += 1
      const input = request.postDataJSON() as { init_data?: string }
      const includeInvite = Boolean(options.inviteStatus) && (options.initialInvite || input.init_data === 'signed-a' || input.init_data === 'signed-b')
      return route.fulfill({ json: { access_token: 'bug04-token', user, onboarding_state: 'complete', preferences: null, invite_context: includeInvite ? { token: 'bug04-invite', room_id: roomId, room_name: 'Планы на ноябрь', city_id: cityId, public_plan: null, inviter: { id: 'creator', display_name: 'Создатель', avatar_url: null, role: 'creator', intent_ready: true }, expires_at: '2099-12-01T00:00:00Z', already_joined: joined || (options.inviteAlreadyJoined ?? false), status: options.inviteStatus! } : null } })
    }
    if (/\/room-invites\/[^/]+\/join$/.test(path)) {
      counters.joins += 1
      if (joinGate) await joinGate
      joined = true
      return route.fulfill({ json: roomSnapshot('active') })
    }
    if (path.endsWith(`/rooms/${roomId}/intent/me`)) {
      counters.writes.push(`${request.method()} ${path}`)
      if (saveGate) await saveGate
      savedIntent = request.postDataJSON() as Record<string, unknown>
      // The first proposal keeps exhausted round N public while preparing N+1.
      // It must not fabricate a new pool/ranking state before the peer confirms.
      const ownPrepared = { ...savedIntent }; delete ownPrepared.expected_round_no; delete ownPrepared.expected_room_version
      persistedRoom = { ...roomSnapshot('zero-pool'), version: 6, next_round: { round_no: 3, confirmed_count: 1, my_confirmed: true, my_intent: { ...ownPrepared, version: 4, round_no: 3, submitted_at: '2099-11-02T10:00:00Z' } as NonNullable<RoomSnapshotDto['my_intent']> } }
      return route.fulfill({ json: persistedRoom })
    }
    if (path.startsWith('/api/v1/rooms') && request.method() !== 'GET') {
      counters.writes.push(`${request.method()} ${path}`)
      return route.fulfill({ status: 500, json: { error: { code: 'UNEXPECTED_WRITE', message: 'Unexpected room mutation' } } })
    }
    if (path.includes(`/rooms/${roomId}`)) {
      if (roomKind === 'error') return route.fulfill({ status: 500, json: { error: { code: 'ROOM_READ_FAILED', message: 'Room unavailable' } } })
      return route.fulfill({ json: persistedRoom })
    }
    if (path.endsWith('/feed/home')) {
      counters.homeReads += 1
      // The actual feed deliberately excludes exhausted rooms; do not make a
      // continuation button appear from a fixture-only active_room projection.
      const hasActiveRoom = (joined || options.room !== undefined) && ['collecting_intents', 'ranking', 'voting'].includes(persistedRoom.state)
      return route.fulfill({ json: { feed_id: 'bug04-feed', generated_at: '2099-11-01T00:00:00Z', sections: [{ type: 'popular', title: 'Популярное', items: [] }], active_room: hasActiveRoom ? { id: roomId, name: 'Планы на ноябрь', city_id: cityId, state: persistedRoom.state } : null } })
    }
    if (path.endsWith('/events/search/count')) return route.fulfill({ json: { total: 0 } })
    if (path.endsWith('/events/search')) return route.fulfill({ json: { items: [], total_estimate: 0, next_cursor: null, applied_filters: {} } })
    if (path.endsWith('/behavior/events:batch')) return route.fulfill({ status: 202, json: { accepted: 1, duplicates: 0, rejected: 0 } })
    return route.fulfill({ status: 404, json: { error: { code: 'NOT_FOUND', message: 'No fixture' } } })
  })
  return {
    counters,
    setRoom(next: RoomKind) { roomKind = next; if (next !== 'error') persistedRoom = roomSnapshot(next) },
    get persistedRoom() { return persistedRoom },
    get savedIntent() { return savedIntent },
    deferJoin() { joinGate = new Promise<void>((resolve) => { releaseJoin = resolve }); return () => releaseJoin?.() },
    deferSave() { saveGate = new Promise<void>((resolve) => { releaseSave = resolve }); return () => releaseSave?.() },
  }
}

test('BUG-04 null invitation context is a neutral screen with an explicit Home exit', async ({ page }, testInfo) => {
  const backend = await fixture(page)
  await page.goto('/join/bug04-invite', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Не удалось подтвердить приглашение' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('bug04-null-invite-before-home.png'), fullPage: true })
  await page.getByRole('button', { name: 'На главную', exact: true }).click()
  await assertHomeStable(page)
  await page.screenshot({ path: testInfo.outputPath('bug04-null-invite-home-after-error.png'), fullPage: true })
  expect(backend.counters.joins).toBe(0)
  expect(backend.counters.writes).toEqual([])
})

async function assertHomeStable(page: Page) {
  await expect(page).toHaveURL(/\/$/)
  await page.evaluate(() => { window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('pageshow')) })
  await expect(page).toHaveURL(/\/$/)
  await expect.poll(() => page.getByRole('button', { name: /^(Создать комнату|Продолжить)$/ }).count()).toBe(1)
  await expect(page.getByRole('button', { name: 'Главная', exact: true })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByRole('button', { name: /^(Создать комнату|Продолжить)$/ })).toBeVisible()
}

for (const inviteStatus of ['full', 'expired'] as const) {
  test(`BUG-04 ${inviteStatus} invite Home exit survives lifecycle events without room writes`, async ({ page }, testInfo) => {
    const backend = await fixture(page, { inviteStatus, initialInvite: true })
    await page.goto('/join/bug04-invite', { waitUntil: 'domcontentloaded' })
    await expect(page.getByText(inviteStatus === 'full' ? 'Комната уже заполнена.' : 'Срок приглашения истёк.', { exact: true })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`bug04-${inviteStatus}-before-home.png`), fullPage: true })
    await page.getByRole('button', { name: 'На главную', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Создать комнату', exact: true })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`bug04-${inviteStatus}-home-after-error.png`), fullPage: true })
    await assertHomeStable(page)
    expect(backend.counters.joins).toBe(0)
    expect(backend.counters.writes).toEqual([])
  })
}

for (const room of ['zero-pool', 'third-round', 'error'] as const) {
  test(`BUG-04 ${room} recovery/error Home exit preserves the route and never resets room state`, async ({ page }, testInfo) => {
    const backend = await fixture(page, { room })
    await page.goto(`/rooms/${roomId}/recovery`, { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('button', { name: 'На главную', exact: true })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`bug04-${room}-before-home.png`), fullPage: true })
    if (room === 'error') {
      backend.setRoom('active')
    }
    await page.getByRole('button', { name: 'На главную', exact: true }).click()
    await assertHomeStable(page)
    await page.screenshot({ path: testInfo.outputPath(`bug04-${room}-home-after-error.png`), fullPage: true })
    if (room === 'error') {
      await page.getByRole('button', { name: 'Продолжить', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/rooms/${roomId}/intent$`))
      await expect(page.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()
    } else {
      await expect(page.getByRole('button', { name: 'Создать комнату', exact: true })).toBeVisible()
      await expect(page.getByRole('button', { name: 'Продолжить', exact: true })).toHaveCount(0)
    }
    expect(backend.counters.joins).toBe(0)
    expect(backend.counters.writes).toEqual([])
  })
}

test('BUG-04 pending join completion cannot navigate over explicit Home exit', async ({ page }, testInfo) => {
  const backend = await fixture(page, { inviteStatus: 'joinable', initialInvite: true })
  await page.goto('/join/bug04-invite', { waitUntil: 'domcontentloaded' })
  const release = backend.deferJoin()
  const joinedResponse = page.waitForResponse((response) => new URL(response.url()).pathname.endsWith(`/room-invites/bug04-invite/join`))
  await page.getByRole('button', { name: 'Присоединиться', exact: true }).click()
  await expect.poll(() => backend.counters.joins).toBe(1)
  await page.getByRole('button', { name: 'На главную', exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  const bootstrapCount = backend.counters.bootstraps
  release()
  await (await joinedResponse).finished()
  await expect.poll(() => backend.counters.bootstraps).toBeGreaterThan(bootstrapCount)
  await assertHomeStable(page)
  await expect(page.getByRole('button', { name: 'Продолжить', exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('bug04-home-after-pending-join.png'), fullPage: true })
  await expect(page).toHaveURL(/\/$/)
  expect(backend.counters.writes).toEqual([])
  expect(backend.counters.joins).toBe(1)
})

test('BUG-04 pending recovery save completes data invalidation but cannot leave Home; Continue keeps snapshot', async ({ page }, testInfo) => {
  const backend = await fixture(page, { room: 'zero-pool' })
  await page.goto(`/rooms/${roomId}/recovery`, { waitUntil: 'domcontentloaded' })
  await page.getByRole('checkbox', { name: /Разрешить любое время/ }).check()
  await page.getByRole('button', { name: 'Проверить изменения', exact: true }).click()
  const release = backend.deferSave()
  const savedResponse = page.waitForResponse((response) => new URL(response.url()).pathname.endsWith(`/rooms/${roomId}/intent/me`))
  await page.getByRole('button', { name: 'Подтвердить мои условия', exact: true }).click()
  await expect.poll(() => backend.counters.writes.length).toBe(1)
  await page.getByRole('button', { name: 'На главную', exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  release()
  await (await savedResponse).finished()
  await assertHomeStable(page)
  await page.screenshot({ path: testInfo.outputPath('bug04-home-after-pending-save.png'), fullPage: true })
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('button', { name: 'Создать комнату', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Продолжить', exact: true })).toHaveCount(0)
  expect(backend.persistedRoom.version).toBe(6)
  expect(backend.savedIntent).not.toBeNull()
  expect(backend.persistedRoom.next_round).toMatchObject({ round_no: 3, confirmed_count: 1, my_confirmed: true, my_intent: { time_slots: ['morning', 'day', 'evening', 'night'] } })
  expect(backend.counters.writes).toEqual([`PUT /api/v1/rooms/${roomId}/intent/me`])
})

test('BUG-04 reopening the same invite after a new signed launch is allowed and can exit again', async ({ page }, testInfo) => {
  const backend = await fixture(page, { inviteStatus: 'joinable' })
  await page.goto('/events', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('searchbox', { name: 'Найти событие', exact: true })).toBeVisible()
  await page.evaluate(() => {
    Object.defineProperty(window.WebApp!, 'initData', { configurable: true, value: 'signed-a' })
    Object.defineProperty(window.WebApp!, 'initDataUnsafe', { configurable: true, value: { start_param: 'bug04-invite' } })
    window.dispatchEvent(new Event('focus'))
  })
  await expect(page).toHaveURL(/\/join\/bug04-invite$/)
  await expect(page.getByRole('heading', { name: 'Вас приглашает Создатель' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('bug04-repeat-invite-before-home.png'), fullPage: true })
  await page.getByRole('button', { name: 'На главную', exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  await page.evaluate(() => {
    Object.defineProperty(window.WebApp!, 'initData', { configurable: true, value: 'signed-b' })
    Object.defineProperty(window.WebApp!, 'initDataUnsafe', { configurable: true, value: { start_param: 'bug04-invite' } })
    window.dispatchEvent(new Event('focus'))
  })
  await expect(page).toHaveURL(/\/join\/bug04-invite$/)
  await page.getByRole('button', { name: 'На главную', exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  expect(backend.counters.joins).toBe(0)
})
