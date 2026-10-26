import { expect, test, type Page } from '@playwright/test'

declare const process: { env: Record<string, string | undefined> }

// Invoked by TestRoomRoundsBrowser; room traffic is forwarded to actual Go
// handlers/PostgreSQL, including Home. MAX bootstrap is a fixture.
test('[match] two independent clients confirm rounds without consuming empty retries or losing recovery choices', async ({ browser, context }, testInfo) => {
  const { ROOMS_LIVE_API: api, ROOMS_LIVE_ROOM: room, ROOMS_LIVE_A: a, ROOMS_LIVE_B: b, ROOMS_LIVE_DATE: date, ROOMS_LIVE_EVENT_TITLE: title } = process.env
  if (!api || !room || !a || !b || !date || !title) throw new Error('Run via TestRoomRoundsBrowser with the isolated database harness')
  const city = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
  const otherContext = await browser.newContext({ viewport: testInfo.project.use.viewport, isMobile: testInfo.project.use.isMobile, hasTouch: testInfo.project.use.hasTouch, reducedMotion: 'reduce' })
  const pageA = await context.newPage()
  const pageB = await otherContext.newPage()
  const snapshot = async (user: string) => {
    const response = await context.request.get(`${api}/api/v1/rooms/${room}`, { headers: { Authorization: `Bearer ${user}` } })
    expect(response.ok()).toBe(true)
    return response.json()
  }
  async function connect(page: Page, user: string) {
    await page.route('https://st.max.ru/**', route => route.fulfill({ contentType: 'text/javascript', body: '' }))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), url = new URL(request.url())
      if (url.pathname === '/api/v1/auth/max/bootstrap') return route.fulfill({ json: { access_token: user, token_type: 'Bearer', expires_in: 3600, user: { id: user, display_name: user === a ? 'Аня' : 'Дима', avatar_url: null, city_id: city, locale: 'ru' }, onboarding_state: 'complete', preferences: null, invite_context: null } })
      if (url.pathname.startsWith('/api/v1/rooms/') || url.pathname.startsWith('/api/v1/events/') || url.pathname === '/api/v1/feed/home') {
        const response = await route.fetch({ url: `${api}${url.pathname}${url.search}`, headers: { ...request.headers(), authorization: `Bearer ${user}` } })
        return route.fulfill({ response })
      }
      if (url.pathname.includes('behavior')) return route.fulfill({ status: 202, json: { accepted: 1, duplicates: 0, rejected: 0 } })
      return route.fulfill({ json: { items: [], sections: [], next_cursor: null, total: 0 } })
    })
    await page.goto(`/rooms/${room}/recovery`)
    await expect(page.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
  }
  const shot = async (page: Page, name: string) => {
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    const filename = `${testInfo.project.name}-${name}.png`
    await page.screenshot({ path: process.env.ROOMS_RENDER_DIR ? `${process.env.ROOMS_RENDER_DIR}/${filename}` : testInfo.outputPath(filename), fullPage: true, animations: 'disabled' })
  }
  try {
    await connect(pageA, a); await connect(pageB, b)
    expect((await snapshot(a)).round_no).toBe(1)
    await shot(pageA, 'empty')
    // A reads a locally selected recovery review; B proposes a new pool.
    await pageA.getByRole('checkbox', { name: 'Разрешить любое время' }).check()
    await pageA.getByRole('button', { name: 'Проверить изменения', exact: true }).click()
    await expect(pageA.getByRole('heading', { name: 'Проверьте изменения' })).toBeVisible()
    await shot(pageA, 'review')
    await pageB.getByRole('checkbox', { name: 'Разрешить любое время' }).check()
    await pageB.getByRole('button', { name: 'Проверить изменения', exact: true }).click()
    await pageB.getByRole('button', { name: 'Подтвердить мои условия', exact: true }).click()
    await expect(pageB.getByText('Условия сохранены', { exact: true })).toBeVisible()
    const pending = await snapshot(a)
    expect(pending.round_no).toBe(1)
    expect(pending.next_round).toMatchObject({ round_no: 2, confirmed_count: 1, my_confirmed: false })
    await expect(pageA.getByText('Друг предложил обновить подбор', { exact: true })).toBeVisible()
    await expect(pageA.getByRole('heading', { name: 'Проверьте изменения' })).toBeVisible()
    await expect(pageA).toHaveURL(/\/recovery$/)
    await shot(pageA, 'notice')
    await pageA.getByRole('button', { name: 'Продолжить', exact: true }).click()
    await shot(pageA, 'choice')
    await pageA.getByRole('button', { name: 'Оставить мои условия', exact: true }).click()
    await expect.poll(async () => (await snapshot(a)).round_no).toBe(2)
    await expect(pageA.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
    // A's own saved conditions were kept, not the unsaved 'any time' review.
    expect((await snapshot(a)).my_intent.time_slots).toEqual(['evening'])
    await shot(pageA, 'round-two')
    await pageB.reload()
    await expect(pageB.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
    // Explicit date overlap produces a real non-empty recommendation in round3.
    await pageA.getByRole('checkbox', { name: 'Добавить дату' }).check()
    await pageA.getByLabel('Дополнительная дата').selectOption(date)
    await pageA.getByRole('checkbox', { name: 'Увеличить бюджет' }).check()
    await pageA.getByLabel('Новый лимит стоимости').selectOption('4000')
    await pageA.getByRole('checkbox', { name: 'Разрешить любое время' }).check()
    await shot(pageA, 'multi-selection')
    await pageA.getByRole('button', { name: 'Проверить изменения', exact: true }).click()
    await expect(pageA.getByRole('list', { name: 'Выбранные изменения' }).getByRole('listitem')).toHaveCount(3)
    await shot(pageA, 'multi-review')
    const combinedRequest = pageA.waitForRequest(r => r.method() === 'PUT' && r.url().endsWith('/intent/me'))
    await pageA.getByRole('button', { name: 'Подтвердить мои условия', exact: true }).click()
    const combinedBody = (await combinedRequest).postDataJSON()
    expect(combinedBody).toMatchObject({ budget_max_minor: 400000, time_slots: ['morning', 'day', 'evening', 'night'], expected_round_no: 2 })
    expect(combinedBody.dates).toContain(date)
    await expect(pageA.getByText('Условия сохранены', { exact: true })).toBeVisible()
    expect((await snapshot(a)).round_no).toBe(2)
    await shot(pageA, 'pending')
    await expect(pageB.getByText('Друг предложил обновить подбор', { exact: true })).toBeVisible()
    await pageB.getByRole('button', { name: 'Продолжить', exact: true }).click()
    await pageB.getByRole('button', { name: 'Оставить мои условия', exact: true }).click()
    await expect.poll(async () => (await snapshot(a)).state).toBe('voting')
    await expect(pageA.getByText('Подборка обновилась', { exact: true })).toBeVisible()
    await pageA.getByRole('button', { name: 'Продолжить', exact: true }).click()
    await expect(pageA.getByText(title, { exact: true }).first()).toBeVisible()
    await expect(pageB.getByText(title, { exact: true }).first()).toBeVisible()
    expect((await snapshot(a)).round_no).toBe(3)
    await shot(pageA, 'voting')
    await pageA.getByRole('button', { name: 'Хочу пойти', exact: true }).click()
    await expect.poll(async () => (await snapshot(a)).pool.my_pool_finished).toBe(true)
    // Warm a REAL active-room Home projection. Fixed Date keeps its production
    // 20s staleTime fresh while normal polling timers keep running.
    await pageA.clock.setFixedTime(new Date())
    await pageA.getByRole('button', { name: 'Назад', exact: true }).click()
    await expect(pageA).toHaveURL(/\/$/)
    await expect(pageA.getByRole('button', { name: 'Продолжить', exact: true })).toBeVisible()
    await shot(pageA, 'home-active')
    await pageA.getByRole('button', { name: 'Продолжить', exact: true }).click()
    await expect(pageA).toHaveURL(/\/waiting$/)
    await pageB.getByRole('button', { name: 'Хочу пойти', exact: true }).click()
    await expect.poll(async () => (await snapshot(a)).state).toBe('matched')
    await expect(pageA.getByText('Подборка завершена', { exact: true })).toBeVisible()
    await pageA.getByRole('button', { name: 'Продолжить', exact: true }).click()
    await expect(pageA).toHaveURL(/\/match$/)
    await expect(pageA.getByRole('heading', { name: 'Это мэтч!', exact: true })).toBeVisible()
    await expect(pageA.getByRole('button', { name: 'Открыть событие', exact: true })).toBeVisible()
    const openEvent = pageA.getByRole('button', { name: 'Открыть событие', exact: true })
    const sizes = testInfo.project.name === 'mobile-chrome' ? [{ width: 393, height: 720 }, { width: 393, height: 560 }] : [{ width: 1280, height: 720 }, { width: 1280, height: 480 }]
    for (const size of sizes) {
      await pageA.setViewportSize(size)
      await openEvent.scrollIntoViewIfNeeded()
      await expect(openEvent).toBeVisible()
      expect(await pageA.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      const actionBox = await openEvent.boundingBox()
      const titleBox = await pageA.getByRole('heading', { name: title, exact: true }).boundingBox()
      expect(actionBox).not.toBeNull(); expect(titleBox).not.toBeNull()
      expect(actionBox!.height).toBeGreaterThanOrEqual(44)
      expect(actionBox!.y).toBeGreaterThanOrEqual(titleBox!.y + titleBox!.height)
      // Browser scroll offsets are integer CSS pixels; glyph/layout boxes can
      // retain a fractional pixel after scrollIntoView.
      expect(actionBox!.y + actionBox!.height).toBeLessThanOrEqual(size.height + 1)
    }
    await pageA.setViewportSize(sizes[0])
    await shot(pageA, 'match')
    const refreshedHome = pageA.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/feed/home' && r.request().method() === 'GET')
    await pageA.getByRole('button', { name: 'Назад', exact: true }).click()
    await expect(pageA).toHaveURL(/\/$/)
    expect((await (await refreshedHome).json()).active_room ?? null).toBeNull()
    await expect(pageA.getByRole('button', { name: 'Создать комнату', exact: true })).toBeVisible()
    await expect(pageA.getByRole('button', { name: 'Продолжить', exact: true })).toHaveCount(0)
    // The live home feed is backed by the same non-empty isolated catalog.
    // A populated first section is normal and must not be treated as a defect.
    await expect(pageA.getByText(title, { exact: true }).first()).toBeVisible()
    await shot(pageA, 'home')
    await pageA.reload()
    await expect(pageA.getByRole('button', { name: 'Создать комнату', exact: true })).toBeVisible()
    expect((await snapshot(a)).round_no).toBe(3)
  } finally { await otherContext.close() }
})
