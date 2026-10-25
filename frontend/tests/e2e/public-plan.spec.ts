import { expect, test, type Browser, type BrowserContextOptions, type Locator, type Page, type TestInfo } from '@playwright/test'

const creator = { id: 'creator-public-plan', display_name: 'Создатель', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const recipient = { id: 'recipient-public-plan', display_name: 'Участник', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru' }
const roomId = 'room-public-plan-e2e'
const inviteToken = 'invite-public-plan-e2e'
const plan = { dates: ['2099-11-20'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_025 }
const roomState: {
  created: boolean
  members: Set<string>
  intents: Record<string, Record<string, unknown>>
  createBody?: Record<string, unknown>
  joinCalls: number
  intentCalls: string[]
} = { created: false, members: new Set(), intents: {}, joinCalls: 0, intentCalls: [] }

function participant(user: typeof creator) {
  return { id: user.id, display_name: user.display_name, avatar_url: null, role: user.id === creator.id ? 'creator' : 'participant', intent_ready: Boolean(roomState.intents[user.id]) }
}

function roomFor(userId: string) {
  const readyCount = roomState.members.size > 0
    ? [...roomState.members].filter((id) => Boolean(roomState.intents[id])).length
    : 0
  return {
    id: roomId, name: 'Планы на ноябрь', city_id: creator.city_id, public_plan: plan,
    state: readyCount === 2 ? 'ranking' : 'collecting_intents', round_no: 1, version: 1,
    participants: [...roomState.members].map((id) => participant(id === creator.id ? creator : recipient)),
    my_intent: roomState.intents[userId] ?? null, pool: null, match: null,
    invite: { url: `http://127.0.0.1:4173/join/${inviteToken}`, max_deep_link: `https://max.ru/example?startapp=${inviteToken}`, expires_at: '2099-12-01T00:00:00Z' },
    allowed_actions: [roomState.intents[userId] ? 'wait' : 'edit_intent', ...(userId === creator.id && roomState.members.size < 2 ? ['invite'] : [])], created_at: '2099-01-01T00:00:00Z', expires_at: '2099-12-01T00:00:00Z',
  }
}

async function stubParticipant(browser: Browser, user: typeof creator, options: BrowserContextOptions) {
  const context = await browser.newContext({ ...options, baseURL: 'http://127.0.0.1:4173', reducedMotion: 'reduce' })
  const page = await context.newPage()
  // The fixture exercises the HTTP client and UI, not the remote MAX SDK.
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/auth/max/bootstrap', async (route) => {
    const alreadyJoined = roomState.members.has(user.id)
    const room = roomFor(user.id)
    const status = alreadyJoined || roomState.members.size < 2 && room.state === 'collecting_intents' ? 'joinable' : 'full'
    const bootstrap = user.id === creator.id ? {
      access_token: `access-${user.id}`, token_type: 'Bearer', expires_in: 3600,
      user, onboarding_state: 'complete', preferences: null, invite_context: null,
    } : {
      access_token: `access-${user.id}`, token_type: 'Bearer', expires_in: 3600,
      user, onboarding_state: 'complete', preferences: null,
      invite_context: {
        token: inviteToken, room_id: roomId, room_name: 'Планы на ноябрь', city_id: creator.city_id,
        public_plan: status === 'joinable' && room.state === 'collecting_intents' ? plan : null,
        inviter: participant(creator), expires_at: '2099-12-01T00:00:00Z', already_joined: alreadyJoined, status,
      },
    }
    await route.fulfill({ status: 200, contentType: 'application/json', json: bootstrap })
  })
  await page.route('**/api/v1/rooms', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    roomState.createBody = route.request().postDataJSON() as Record<string, unknown>
    roomState.created = true
    roomState.members.add(creator.id)
    const room = roomFor(creator.id)
    await route.fulfill({ status: 201, contentType: 'application/json', json: {
      room, invite: { token: inviteToken, url: room.invite.url, max_deep_link: room.invite.max_deep_link, expires_at: room.invite.expires_at },
    } })
  })
  await page.route(`**/api/v1/rooms/${roomId}`, async (route) => {
    if (route.request().method() !== 'GET') return route.fallback()
    await route.fulfill({ status: 200, contentType: 'application/json', json: roomFor(user.id) })
  })
  await page.route(`**/api/v1/room-invites/${inviteToken}/join`, async (route) => {
    roomState.joinCalls += 1
    roomState.members.add(recipient.id)
    await route.fulfill({ status: 200, contentType: 'application/json', json: roomFor(user.id) })
  })
  await page.route(`**/api/v1/rooms/${roomId}/intent/me`, async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    const body = route.request().postDataJSON() as Record<string, unknown>
    roomState.intentCalls.push(user.id)
    roomState.intents[user.id] = body
    await route.fulfill({ status: 200, contentType: 'application/json', json: roomFor(user.id) })
  })
  await page.route('**/api/v1/behavior/events:batch', async (route) => route.fulfill({ status: 202, contentType: 'application/json', json: { accepted: 1, duplicates: 0, rejected: 0 } }))
  return { context, page }
}

async function submitIntent(page: Page) {
  await page.getByRole('button', { name: 'Сохранить предпочтения' }).click()
}

async function activate(locator: Locator, touch: boolean) {
  if (touch) await locator.tap()
  else await locator.click()
}

async function expectFocusedFieldClearOfSaveBar(page: Page, selector: ReturnType<Page['getByLabel']>, screenshotPath: string) {
  const saveButton = page.getByRole('button', { name: 'Сохранить предпочтения' })
  await selector.scrollIntoViewIfNeeded()
  await selector.focus()
  await expect(selector).toBeFocused()
  const fieldBox = await selector.boundingBox()
  const saveBox = await saveButton.boundingBox()
  const viewport = page.viewportSize()
  expect(fieldBox).not.toBeNull()
  expect(saveBox).not.toBeNull()
  expect(fieldBox!.y).toBeGreaterThanOrEqual(0)
  expect(fieldBox!.y + fieldBox!.height).toBeLessThanOrEqual(viewport!.height)
  const overlaps = fieldBox!.x < saveBox!.x + saveBox!.width && fieldBox!.x + fieldBox!.width > saveBox!.x && fieldBox!.y < saveBox!.y + saveBox!.height && fieldBox!.y + fieldBox!.height > saveBox!.y
  expect(overlaps, 'sticky save button must not cover the focused field').toBe(false)
  await page.screenshot({ path: screenshotPath })
}

test('two participants preview, join, and separately confirm their own plan-derived preferences', async ({ browser, viewport, isMobile, hasTouch, userAgent, deviceScaleFactor }, testInfo: TestInfo) => {
  roomState.created = false
  roomState.members = new Set()
  roomState.intents = {}
  roomState.createBody = undefined
  roomState.joinCalls = 0
  roomState.intentCalls = []
  const options = { viewport, isMobile, hasTouch, userAgent, deviceScaleFactor }
  const creatorClient = await stubParticipant(browser, creator, options)
  const recipientClient = await stubParticipant(browser, recipient, options)

  try {
    const creatorPage = creatorClient.page
    await creatorPage.clock.install({ time: new Date('2099-11-20T12:00:00Z') })
    await recipientClient.page.clock.install({ time: new Date('2099-11-20T12:00:00Z') })
    await creatorPage.goto('/rooms/new', { waitUntil: 'domcontentloaded' })
    await expect(creatorPage.getByRole('heading', { name: 'Создать комнату' })).toBeVisible()
    await creatorPage.getByLabel('Название комнаты').fill('Куда идём в субботу?')
    const citySelect = creatorPage.getByRole('combobox', { name: 'Город комнаты' })
    await expect(citySelect).toContainText('Москва')
    await activate(citySelect, isMobile)
    await expect(creatorPage.getByRole('option', { name: 'Казань — город готовится' })).toBeDisabled()
    const cityMenu = creatorPage.getByRole('listbox', { name: 'Город комнаты' })
    await cityMenu.screenshot({ path: testInfo.outputPath('room-city-options.png') })
    const cityMenuBox = await cityMenu.boundingBox()
    const cityViewport = creatorPage.viewportSize()
    expect(cityMenuBox).not.toBeNull()
    expect(cityMenuBox!.x).toBeGreaterThanOrEqual(0)
    expect(cityMenuBox!.y).toBeGreaterThanOrEqual(0)
    expect(cityMenuBox!.x + cityMenuBox!.width).toBeLessThanOrEqual(cityViewport!.width)
    expect(cityMenuBox!.y + cityMenuBox!.height).toBeLessThanOrEqual(cityViewport!.height)
    if (isMobile) await activate(creatorPage.getByRole('option', { name: 'Москва', exact: true }), true)
    else {
      await citySelect.press('ArrowDown')
      await citySelect.press('ArrowUp')
      await citySelect.press('Enter')
    }
    await expect(citySelect).toContainText('Москва')
    await creatorPage.getByRole('checkbox', { name: /Добавить публичное предложение/ }).check()
    await activate(creatorPage.getByRole('button', { name: 'Выбор дат', exact: true }), isMobile)
    await activate(creatorPage.getByRole('gridcell', { name: /20 ноября 2099/ }), isMobile)
    await activate(creatorPage.getByRole('button', { name: 'Применить' }), isMobile)
    await creatorPage.getByRole('button', { name: 'Вечер', exact: true }).click()
    await creatorPage.getByRole('button', { name: 'Концерты', exact: true }).click()
    const anyTime = creatorPage.getByRole('button', { name: 'Любое время', exact: true })
    await anyTime.click()
    await expect(anyTime).toHaveAttribute('aria-pressed', 'true')
    await anyTime.click()
    await expect(anyTime).toHaveAttribute('aria-pressed', 'false')
    await creatorPage.getByRole('button', { name: 'Вечер', exact: true }).click()
    await creatorPage.getByLabel(/Бюджет на человека · максимум/).fill('2500.25')
    if (isMobile) {
      await creatorPage.setViewportSize({ width: 375, height: 812 })
      expect(await creatorPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await creatorPage.screenshot({ path: testInfo.outputPath('public-plan-create-375.png'), fullPage: true })
      await creatorPage.setViewportSize({ width: 812, height: 375 })
      expect(await creatorPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await creatorPage.screenshot({ path: testInfo.outputPath('public-plan-create-landscape.png'), fullPage: true })
      await creatorPage.setViewportSize(viewport ?? { width: 393, height: 851 })
    }
    await creatorPage.screenshot({ path: testInfo.outputPath('public-plan-create.png'), fullPage: true })
    await creatorPage.getByRole('button', { name: 'Выбор дат', exact: true }).click()
    await expect(creatorPage.getByRole('dialog', { name: 'Выбор дат' })).toBeVisible()
    const calendarBox = await creatorPage.getByRole('dialog', { name: 'Выбор дат' }).boundingBox()
    const viewportSize = creatorPage.viewportSize()
    expect(calendarBox).not.toBeNull()
    expect(calendarBox!.x).toBeGreaterThanOrEqual(0)
    expect(calendarBox!.y).toBeGreaterThanOrEqual(0)
    expect(calendarBox!.x + calendarBox!.width).toBeLessThanOrEqual(viewportSize!.width)
    expect(calendarBox!.y + calendarBox!.height).toBeLessThanOrEqual(viewportSize!.height)
    await creatorPage.screenshot({ path: testInfo.outputPath('public-plan-calendar.png'), fullPage: true })
    await creatorPage.keyboard.press('Escape')
    await expect(creatorPage.getByRole('button', { name: 'Выбор дат', exact: true })).toBeFocused()
    await creatorPage.getByRole('button', { name: 'Создать комнату' }).click()
    await expect(creatorPage).toHaveURL(new RegExp(`/rooms/${roomId}/invite$`))
    await expect(creatorPage.getByRole('region', { name: 'Публичное предложение комнаты' })).toContainText('20 ноября')
    await expect(creatorPage.getByRole('region', { name: 'Публичное предложение комнаты' })).toContainText('2 500,25')
    await creatorPage.getByRole('region', { name: 'Публичное предложение комнаты' }).screenshot({ path: testInfo.outputPath('public-plan-summary.png') })
    expect(roomState.createBody).toMatchObject({ city_id: creator.city_id, public_plan: plan })

    const recipientPage = recipientClient.page
    const initialRecipientBootstrap = recipientPage.waitForResponse((response) => new URL(response.url()).pathname.endsWith('/auth/max/bootstrap'))
    await recipientPage.goto(`/join/${inviteToken}`, { waitUntil: 'domcontentloaded' })
    expect((await (await initialRecipientBootstrap).json()).invite_context).toMatchObject({ already_joined: false, status: 'joinable' })
    await expect(recipientPage.getByRole('heading', { name: /Вас приглашает Участник|Вас приглашает Создатель/ })).toBeVisible()
    await expect(recipientPage.getByRole('region', { name: 'Публичное предложение комнаты' })).toContainText('20 ноября')
    await expect(recipientPage.getByRole('region', { name: 'Публичное предложение комнаты' })).toContainText('Концерты')
    expect(roomState.joinCalls).toBe(0)
    expect(roomState.members).toEqual(new Set([creator.id]))
    expect(roomState.intentCalls).toEqual([])

    await recipientPage.getByRole('button', { name: 'Присоединиться' }).click()
    await expect(recipientPage).toHaveURL(new RegExp(`/rooms/${roomId}/intent$`))
    await expect(recipientPage.getByText(/Поля заполнены из публичного предложения/)).toBeVisible()
    await expect(recipientPage.getByLabel(/Бюджет, ₽/)).toHaveValue('2500.25')
    expect(roomState.members.has(creator.id)).toBe(true)
    expect(roomState.members.has(recipient.id)).toBe(true)
    await recipientPage.screenshot({ path: testInfo.outputPath('private-room-preferences.png'), fullPage: true })
    await expectFocusedFieldClearOfSaveBar(recipientPage, recipientPage.getByLabel(/Бюджет, ₽/), testInfo.outputPath('private-budget-focused-viewport.png'))
    await expectFocusedFieldClearOfSaveBar(recipientPage, recipientPage.getByLabel('Дополнительное пожелание'), testInfo.outputPath('private-note-focused-viewport.png'))
    expect(roomState.joinCalls).toBe(1)
    expect(roomState.intentCalls).toEqual([])

    await creatorPage.goto(`/rooms/${roomId}/intent`, { waitUntil: 'domcontentloaded' })
    await expect(creatorPage.getByText(/Поля заполнены из публичного предложения/)).toBeVisible()
    await expect(creatorPage.getByLabel(/Бюджет, ₽/)).toHaveValue('2500.25')
    expect(roomState.intentCalls).toEqual([])

    await creatorPage.getByRole('button', { name: 'Концерты' }).click()
    await creatorPage.getByRole('button', { name: 'Кино' }).click()
    await creatorPage.getByLabel(/Бюджет, ₽/).fill('1500.25')
    await expect(recipientPage.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'true')
    await expect(recipientPage.getByLabel(/Бюджет, ₽/)).toHaveValue('2500.25')

    await submitIntent(creatorPage)
    await expect(creatorPage).toHaveURL(new RegExp(`/rooms/${roomId}/waiting$`))
    expect(roomState.intentCalls).toEqual([creator.id])
    expect(roomState.intents[creator.id]).toMatchObject({ dates: plan.dates, time_slots: plan.time_slots, category_slugs: ['cinema'], budget_max_minor: 150_025 })
    expect(roomState.intents[recipient.id]).toBeUndefined()
    expect([...roomState.members].map((id) => [id, Boolean(roomState.intents[id])])).toEqual([[creator.id, true], [recipient.id, false]])

    await recipientPage.getByLabel(/Бюджет, ₽/).fill('4000.50')
    await recipientPage.getByRole('button', { name: 'Выбор дат', exact: true }).click()
    await recipientPage.getByRole('gridcell', { name: '24 ноября 2099 г.', exact: true }).click()
    await recipientPage.getByRole('button', { name: 'Применить' }).click()
    expect(roomState.intents[creator.id]).toMatchObject({ category_slugs: ['cinema'], budget_max_minor: 150_025 })
    expect(roomState.intents[recipient.id]).toBeUndefined()

    await submitIntent(recipientPage)
    await expect(recipientPage).toHaveURL(new RegExp(`/rooms/${roomId}/waiting$`))
    expect(roomState.intentCalls).toEqual([creator.id, recipient.id])
    expect(roomState.intents[recipient.id]).toMatchObject({ dates: [...plan.dates, '2099-11-24'], time_slots: plan.time_slots, category_slugs: plan.category_slugs, budget_max_minor: 400_050 })
    expect(roomState.intents[creator.id]).toMatchObject({ dates: plan.dates, category_slugs: ['cinema'], budget_max_minor: 150_025 })

    const creatorBootstrap = creatorPage.waitForResponse((response) => new URL(response.url()).pathname.endsWith('/auth/max/bootstrap'))
    await creatorPage.reload({ waitUntil: 'domcontentloaded' })
    expect((await (await creatorBootstrap).json()).invite_context).toBeNull()
    const recipientBootstrap = recipientPage.waitForResponse((response) => new URL(response.url()).pathname.endsWith('/auth/max/bootstrap'))
    await recipientPage.reload({ waitUntil: 'domcontentloaded' })
    expect((await (await recipientBootstrap).json()).invite_context).toMatchObject({ already_joined: true, status: 'joinable', public_plan: null })
    await expect(creatorPage.getByRole('heading', { name: 'Формируем общий пул…' })).toBeVisible()
    await expect(recipientPage.getByRole('heading', { name: 'Формируем общий пул…' })).toBeVisible()
    expect(roomState.intents[creator.id]).toBeDefined()
    expect(roomState.intents[recipient.id]).toBeDefined()
    expect(roomState.createBody).toMatchObject({ public_plan: plan })
    expect(roomState.joinCalls).toBe(1)
  } finally {
    await recipientClient.context.close()
    await creatorClient.context.close()
  }
})
