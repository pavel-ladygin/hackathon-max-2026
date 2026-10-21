import { expect, test, type Page, type TestInfo } from '@playwright/test'

const viewports = [
  { name: '320px', width: 320, height: 720 },
  { name: '375px', width: 375, height: 812 },
  { name: '390px', width: 390, height: 844 },
  { name: '430px', width: 430, height: 932 },
  { name: 'landscape', width: 844, height: 390 },
] as const

const user = {
  id: 'layout-e2e-user',
  display_name: 'Е2Е пользователь',
  avatar_url: null,
  city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24',
  locale: 'ru',
}

const preferences = {
  city_id: user.city_id,
  interest_slugs: ['concerts'],
  budget_max_minor: 350_000,
  usual_day_types: ['weekend'],
  usual_time_slots: ['evening'],
  version: 1,
  updated_at: '2026-01-01T00:00:00.000Z',
}

const event = {
  id: 'layout-event',
  title: 'Джазовый вечер',
  subtitle: 'Живой концерт',
  category_slug: 'concerts',
  starts_at: '2026-01-01T19:00:00Z',
  timezone: 'Europe/Moscow',
  date_label: '1 января, 19:00',
  venue_name: 'Оранжерея',
  distance_m: 1200,
  distance_label: '1,2 км',
  price_from_minor: 150000,
  currency: 'RUB',
  price_label: 'от 1 500 ₽',
  image_url: null,
  saved: false,
  reasons: [],
}

const room = {
  id: 'layout-room',
  name: 'Куда идём?',
  city_id: user.city_id,
  state: 'collecting_intents',
  round_no: 1,
  version: 1,
  participants: [{ id: user.id, display_name: user.display_name, avatar_url: null, role: 'creator', intent_ready: false }],
  my_intent: null,
  pool: null,
  match: null,
  invite: null,
  allowed_actions: ['edit_intent'],
  created_at: '2026-01-01T00:00:00Z',
  expires_at: '2030-01-01T00:00:00Z',
}

async function stubCatalogBackend(page: Page) {
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ access_token: 'layout-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
  }))

  let searchCount = 0
  await page.route('**/api/v1/events/search/count**', (route) => route.fulfill({ json: { total: 1 } }))
  await page.route(/\/api\/v1\/events\/search(?:\?.*)?$/, async (route) => {
    searchCount += 1
    // Hold the first changed-filter response long enough to observe the page geometry.
    if (searchCount > 1) await new Promise((resolve) => setTimeout(resolve, 900))
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [{ ...event }], applied_filters: {}, total_estimate: null, next_cursor: null }),
    })
  })
}

async function stubRoomBackend(page: Page) {
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ access_token: 'layout-room-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
  }))
  await page.route(`**/api/v1/rooms/${room.id}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(room) }))
  await page.route(`**/api/v1/rooms/${room.id}/intent/me`, async (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...room, my_intent: null }) }))
}

async function stubPreferencesBackend(page: Page) {
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ access_token: 'layout-preferences-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
  }))
}

async function assertPreferenceActions(page: Page, viewportWidth: number, testInfo: TestInfo) {
  await stubPreferencesBackend(page)
  await page.goto('/preferences')
  const section = page.getByRole('region', { name: 'Предложенные мероприятия' })
  await expect(section).toBeVisible()
  const actions = section.getByRole('button', { name: /^(Предложить|Открыть)$/ })
  await expect(actions).toHaveCount(2)

  const metrics = await actions.evaluateAll((buttons) => buttons.map((button) => {
    const rect = button.getBoundingClientRect()
    const style = getComputedStyle(button)
    return { width: rect.width, height: rect.height, fontFamily: style.fontFamily, fontSize: style.fontSize, fontWeight: style.fontWeight, background: style.backgroundColor }
  }))
  expect(metrics[0].width).toBe(metrics[1].width)
  expect(metrics[0].height).toBe(metrics[1].height)
  expect(metrics[0].fontFamily).toBe(metrics[1].fontFamily)
  expect(metrics[0].fontSize).toBe(metrics[1].fontSize)
  expect(metrics[0].fontWeight).toBe(metrics[1].fontWeight)
  expect(metrics[0].background).toBe(metrics[1].background)
  expect(metrics[0].height).toBe(48)
  expect(metrics[0].width).toBe(viewportWidth < 360 ? 104 : 116)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewportWidth)

  for (const name of ['Обычно удобно', 'Время']) {
    const gap = await page.getByRole('group', { name, exact: true }).evaluate((fieldset) => {
      const legend = fieldset.querySelector('legend')!
      return legend.nextElementSibling!.getBoundingClientRect().top - legend.getBoundingClientRect().bottom
    })
    expect(gap).toBeCloseTo(13, 0)
  }

  await page.screenshot({ path: testInfo.outputPath(`preferences-actions-${viewportWidth}px.png`), fullPage: true })
}

for (const viewport of viewports) {
  test.describe(`layout regressions at ${viewport.name}`, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } })

    test('filter changes keep horizontal position and scrollY while search is pending', async ({ page }) => {
      await stubCatalogBackend(page)
      await page.goto('/events')
      await expect(page.getByRole('heading', { name: 'События' })).toBeVisible()

      await page.getByRole('button', { name: 'Фильтры' }).click()
      await expect(page.getByRole('button', { name: 'Концерты' })).toBeVisible()
      await page.waitForTimeout(250)
      await page.evaluate(() => window.scrollTo(0, Math.min(160, document.documentElement.scrollHeight)))

      const before = await page.evaluate(() => ({ x: window.scrollX, y: window.scrollY, width: document.documentElement.scrollWidth }))
      await page.getByRole('button', { name: 'Концерты' }).evaluate((button: HTMLButtonElement) => button.click())
      await page.waitForTimeout(120)
      const during = await page.evaluate(() => ({ x: window.scrollX, y: window.scrollY, width: document.documentElement.scrollWidth }))

      expect(during.x).toBe(before.x)
      expect(Math.abs(during.y - before.y)).toBeLessThanOrEqual(1)
      expect(during.width).toBeLessThanOrEqual(viewport.width)
      await expect(page.getByRole('button', { name: /Фильтры · 1/ })).toBeVisible()
    })

    test('date fields open an accessible bottom-sheet calendar within the viewport', async ({ page }) => {
      await stubCatalogBackend(page)
      await page.goto('/events')
      await page.getByRole('button', { name: 'Фильтры' }).click()

      // The date picker replaces the browser-native date control with labelled buttons.
      const startDate = page.getByRole('button', { name: /Дата начала|^С$/ }).first()
      await expect(startDate).toBeVisible()
      await startDate.focus()
      await page.keyboard.press('Enter')

      const dialog = page.getByRole('dialog', { name: /Выбор даты|Календарь/ })
      await expect(dialog).toBeVisible()
      await expect(page.locator('input[type="date"]')).toHaveCount(0)
      const box = await dialog.boundingBox()
      const browserViewport = await page.evaluate(() => ({ width: window.innerWidth, height: window.innerHeight }))
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.y).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(browserViewport.width + 1)
      expect(box!.y + box!.height).toBeLessThanOrEqual(browserViewport.height + 1)

      await page.keyboard.press('Tab')
      await expect.poll(() => page.evaluate(() => document.activeElement?.closest('[role="dialog"]') !== null)).toBe(true)
      await page.keyboard.press('Escape')
      await expect(dialog).toBeHidden()
      await expect(startDate).toBeFocused()
    })

    test('room intent choices wrap without overlap and save action stays reachable', async ({ page }) => {
      await stubRoomBackend(page)
      await page.goto(`/rooms/${room.id}/intent`)
      await expect(page.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()

      const choiceFields = page.locator('form fieldset').filter({ has: page.locator('[class*="choiceOptions"]') })
      await expect(choiceFields.first()).toBeVisible()
      const gaps = await choiceFields.evaluateAll((fields) => fields.map((field) => {
        const legend = field.querySelector('legend')
        const options = field.querySelector('[class*="choiceOptions"]')
        if (!legend || !options) return Number.NaN
        return options.getBoundingClientRect().top - legend.getBoundingClientRect().bottom
      }))
      expect(gaps.length).toBeGreaterThan(0)
      for (const gap of gaps) expect(gap).toBeCloseTo(13, 0)

      const form = page.locator('form').first()
      const boxes = await form.locator('fieldset, label, .footer').evaluateAll((nodes) => nodes
        .map((node) => node.getBoundingClientRect())
        .filter((rect) => rect.width > 0 && rect.height > 0)
        .sort((a, b) => a.top - b.top))
      for (let index = 1; index < boxes.length; index += 1) {
        expect(boxes[index].top).toBeGreaterThanOrEqual(boxes[index - 1].bottom - 1)
      }

      const save = page.getByRole('button', { name: 'Сохранить предпочтения' })
      await expect(save).toBeVisible()
      await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
      const saveBox = await save.boundingBox()
      expect(saveBox).not.toBeNull()
      expect(saveBox!.x).toBeGreaterThanOrEqual(0)
      expect(saveBox!.x + saveBox!.width).toBeLessThanOrEqual(viewport.width + 1)
      expect(saveBox!.y + saveBox!.height).toBeLessThanOrEqual(viewport.height + 1)
    })

    test('preference actions use a consistent button style without viewport overflow', async ({ page }, testInfo) => {
      await assertPreferenceActions(page, viewport.width, testInfo)
    })

    test('preference action labels remain separated at 200% theme text size', async ({ page }, testInfo) => {
      await stubPreferencesBackend(page)
      await page.goto('/preferences')
      const theme = page.locator('.max-app-theme')
      await expect(theme).toBeVisible()
      await page.evaluate(() => { document.documentElement.style.fontSize = '200%' })
      const section = page.getByRole('region', { name: 'Предложенные мероприятия' })
      const actions = section.getByRole('button', { name: /^(Предложить|Открыть)$/ })
      await expect(actions.first()).toBeVisible()
      await expect(actions).toHaveCount(2)
      const rows = await section.locator(':scope > div').evaluateAll((elements) => elements.map((row) => {
        const text = row.querySelector('span')?.getBoundingClientRect()
        const button = row.querySelector('button')?.getBoundingClientRect()
        if (!text || !button) return false
        return (text.right <= button.left + 1 || text.bottom <= button.top - 8) && text.bottom > row.getBoundingClientRect().top
      }))
      expect(rows.length).toBe(2)
      expect(rows.every(Boolean)).toBe(true)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width)
      await page.screenshot({ path: testInfo.outputPath(`preferences-actions-${viewport.width}px-200-percent.png`), fullPage: true })
    })
  })
}

test.describe('desktop full-bleed layout', () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test('page shell reaches viewport edges without desktop frame styling', async ({ page }) => {
    await stubCatalogBackend(page)
    await page.goto('/events')
    await expect(page.getByRole('heading', { name: 'События' })).toBeVisible()

    const layout = await page.locator('main').first().evaluate((element) => {
      const rect = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      return {
        left: rect.left,
        top: rect.top,
        width: rect.width,
        availableWidth: document.body.getBoundingClientRect().width,
        borderRadius: style.borderRadius,
        boxShadow: style.boxShadow,
      }
    })

    expect(layout.left).toBe(0)
    expect(layout.top).toBe(0)
    expect(layout.width).toBe(layout.availableWidth)
    expect(layout.borderRadius).toBe('0px')
    expect(layout.boxShadow).toBe('none')
  })

  test('desktop loading skeleton also reaches viewport edges', async ({ page }) => {
    let releaseBootstrap!: () => void
    const bootstrapGate = new Promise<void>((resolve) => { releaseBootstrap = resolve })
    await page.route('**/api/v1/auth/max/bootstrap', async (route) => {
      await bootstrapGate
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ access_token: 'layout-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
      })
    })
    await page.goto('/events')
    const skeleton = page.getByRole('status', { name: 'Знакомимся с вами…' })
    try {
      await expect(skeleton).toBeVisible()

      const geometry = await skeleton.evaluate((element) => {
        const rect = element.getBoundingClientRect()
        return { left: rect.left, width: rect.width, availableWidth: document.body.getBoundingClientRect().width }
      })
      expect(geometry.left).toBe(0)
      expect(geometry.width).toBe(geometry.availableWidth)
    } finally {
      releaseBootstrap()
    }
  })

  test('home hero uses the available desktop content width', async ({ page }) => {
    await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ access_token: 'layout-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
    }))
    await page.route('**/api/v1/feed/home**', (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ feed_id: 'layout-feed', generated_at: '2026-01-01T00:00:00Z', sections: [{ type: 'for_you', title: 'Для вас', items: [{ ...event }] }], active_room: null }),
    }))

    await page.goto('/')
    const hero = page.getByRole('button', { name: /Джазовый вечер/ })
    await expect(hero).toBeVisible()

    const widths = await hero.evaluate((element) => {
      const content = element.parentElement!
      const style = getComputedStyle(content)
      return {
        hero: element.getBoundingClientRect().width,
        content: content.getBoundingClientRect().width,
        paddingLeft: Number.parseFloat(style.paddingLeft),
        paddingRight: Number.parseFloat(style.paddingRight),
      }
    })
    expect(widths.hero).toBe(widths.content - widths.paddingLeft - widths.paddingRight)
  })
})
