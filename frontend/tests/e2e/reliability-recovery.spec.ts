import { expect, test, type Page, type TestInfo } from '@playwright/test'

const user = { id: 'recovery-user', display_name: 'Участник', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const roomId = 'room-reliability-recovery'
const privateMarker = 'PRIVATE-INTENT-MUST-STAY-PRIVATE'
const ownIntent = {
  dates: ['2099-11-20'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'],
  budget_max_minor: 350_000, radius_m: 5_000, exclusion_slugs: [], location: null, free_text: privateMarker,
  version: 1, round_no: 2, submitted_at: '2099-11-01T10:00:00Z',
}

type RecoveryKind = 'zero-pool' | 'positive-nonmatch' | 'third-round'

function roomFor(kind: RecoveryKind) {
  const thirdRound = kind === 'third-round'
  const zeroPool = kind === 'zero-pool'
  return {
    id: roomId, name: 'Планы на ноябрь', city_id: user.city_id, state: 'exhausted',
    round_no: thirdRound ? 3 : 2, version: 3,
    participants: [
      { id: user.id, display_name: user.display_name, avatar_url: null, role: 'creator', intent_ready: true },
      { id: 'other-user', display_name: 'Другой участник', avatar_url: null, role: 'participant', intent_ready: true },
    ],
    my_intent: { ...ownIntent, round_no: thirdRound ? 3 : 2 },
    pool: {
      version: 3, round_no: thirdRound ? 3 : 2, state: 'exhausted', total: zeroPool ? 0 : 4,
      voted_by_me: zeroPool ? 0 : 4, my_pool_finished: true, room_exhausted: true,
      retry_after_seconds: null, exhaustion_reasons: [],
    },
    match: null, invite: null,
    allowed_actions: thirdRound ? [] : ['restart_with_new_intent'],
    created_at: '2099-11-01T00:00:00Z', expires_at: '2099-12-01T00:00:00Z',
  }
}

async function stubSession(page: Page, kind: RecoveryKind) {
  const gets: string[] = []
  let currentKind = kind
  let failedGetsRemaining = 0
  let createRoomPosts = 0
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({ status: 200, contentType: 'application/json', json: {
    access_token: 'e2e-access-only', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences: null, invite_context: null,
  } }))
  await page.route(`**/api/v1/rooms/${roomId}`, async (route) => {
    if (route.request().method() !== 'GET') return route.fallback()
    gets.push(route.request().url())
    if (failedGetsRemaining > 0) {
      failedGetsRemaining -= 1
      await route.abort('internetdisconnected')
      return
    }
    await route.fulfill({ status: 200, contentType: 'application/json', json: roomFor(currentKind) })
  })
  await page.route('**/api/v1/rooms', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    createRoomPosts += 1
    await route.fulfill({ status: 500, contentType: 'application/json', json: { error: 'unexpected room creation' } })
  })
  await page.route('**/api/v1/behavior/events:batch', (route) => route.fulfill({ status: 202, contentType: 'application/json', json: { accepted: 1, duplicates: 0, rejected: 0 } }))
  return {
    gets,
    get createRoomPosts() { return createRoomPosts },
    failNextGet() { failedGetsRemaining = 2 },
    setKind(next: RecoveryKind) { currentKind = next },
  }
}

async function assertViewportFits(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
}

test('recovery separates zero pool, nonmatch and exhausted rounds across viewport and keyboard states', async ({ page, isMobile }, testInfo: TestInfo) => {
  const backend = await stubSession(page, 'zero-pool')
  await page.goto(`/rooms/${roomId}/recovery`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Добавить категорию' })).toHaveCount(0)
  await expect(page.getByText(/Изменения не гарантируют больше событий/)).toBeVisible()
  await expect(page.locator('body')).not.toContainText(privateMarker)

  if (isMobile) {
    await page.setViewportSize({ width: 375, height: 812 })
    await assertViewportFits(page)
    await page.screenshot({ path: testInfo.outputPath('recovery-zero-375.png'), fullPage: true })
    await page.setViewportSize({ width: 812, height: 375 })
    await assertViewportFits(page)
    const explanation = page.getByText('Вы меняете только свои пожелания. Каждый участник самостоятельно пересматривает и подтверждает свои условия.')
    const actions = page.getByRole('button', { name: 'Создать новую комнату' }).locator('..')
    const explanationBox = await explanation.boundingBox()
    const actionsBox = await actions.boundingBox()
    expect(explanationBox).not.toBeNull()
    expect(actionsBox).not.toBeNull()
    expect(actionsBox!.y).toBeGreaterThanOrEqual(explanationBox!.y + explanationBox!.height - 1)
    await page.screenshot({ path: testInfo.outputPath('recovery-zero-landscape.png') })
    const createRoom = page.getByRole('button', { name: 'Создать новую комнату' })
    await createRoom.focus()
    await expect(createRoom).toBeFocused()
    const focusedBox = await createRoom.boundingBox()
    expect(focusedBox!.y).toBeGreaterThanOrEqual(0)
    expect(focusedBox!.y + focusedBox!.height).toBeLessThanOrEqual(375)
  } else {
    await assertViewportFits(page)
    await page.screenshot({ path: testInfo.outputPath('recovery-zero-desktop.png'), fullPage: true })
  }
  expect(backend.gets.length).toBeGreaterThan(0)

  const navigation = page.getByRole('button', { name: 'На главную' })
  await navigation.focus()
  await expect(navigation).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/$/)

  backend.setKind('positive-nonmatch')
  await page.goto(`/rooms/${roomId}/recovery?case=positive`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: /События закончились, общего лайка нет/ })).toBeVisible()
})

test('positive pool explains category changes, while a third round only navigates to explicit room creation', async ({ page, isMobile }, testInfo: TestInfo) => {
  const backend = await stubSession(page, 'positive-nonmatch')
  await page.goto(`/rooms/${roomId}/recovery`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'События закончились, общего лайка нет' })).toBeVisible()
  const category = page.getByRole('button', { name: 'Добавить категорию' })
  await expect(category).toBeVisible()
  await expect(category).toContainText('Изменит порядок рекомендаций, но не гарантирует больше событий')
  expect((await category.boundingBox())!.height).toBeGreaterThanOrEqual(44)
  await expect(page.locator('body')).not.toContainText(privateMarker)
  await assertViewportFits(page)
  await page.screenshot({ path: testInfo.outputPath(isMobile ? 'recovery-nonmatch-mobile.png' : 'recovery-nonmatch-desktop.png'), fullPage: true })

  backend.setKind('third-round')
  await page.goto(`/rooms/${roomId}/recovery?case=third`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Лимит подборок исчерпан' })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('terminal-round-three.png'), fullPage: true })
  await expect(page.getByRole('button', { name: 'Создать новую комнату' })).toBeVisible()
  await page.getByRole('button', { name: 'Создать новую комнату' }).click()
  await expect(page).toHaveURL(/\/rooms\/new$/)
  expect(backend.createRoomPosts).toBe(0)
})

test('room read retry repeats the same GET after offline failure', async ({ page }) => {
  const backend = await stubSession(page, 'zero-pool')
  backend.failNextGet()
  await page.goto(`/rooms/${roomId}/recovery`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Комната недоступна' })).toBeVisible()
  expect(backend.gets).toHaveLength(2)
  const failedUrl = backend.gets[0]
  await page.getByRole('button', { name: 'Повторить' }).click()
  await expect(page.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
  expect(backend.gets).toEqual([failedUrl, failedUrl, failedUrl])
})
