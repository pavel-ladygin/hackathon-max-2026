import { expect, test, type Page } from '@playwright/test'

declare const process: { env: Record<string, string | undefined> }

// The companion of the normal-match flow. It uses the same real HTTP/Postgres
// harness but exhausts all three rounds without manufacturing votes or round 4.
test('[empty] two independent clients exhaust exactly three empty rounds', async ({ browser, context }, testInfo) => {
  const { ROOMS_LIVE_API: api, ROOMS_LIVE_ROOM: room, ROOMS_LIVE_A: a, ROOMS_LIVE_B: b } = process.env
  if (!api || !room || !a || !b) throw new Error('Run via TestRoomRoundsBrowser with the isolated database harness')
  const city = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
  const peer = await browser.newContext({ viewport: testInfo.project.use.viewport, isMobile: testInfo.project.use.isMobile, hasTouch: testInfo.project.use.hasTouch, reducedMotion: 'reduce' })
  const pageA = await context.newPage(), pageB = await peer.newPage()
  let intentWrites = 0
  const snapshot = async (user: string) => {
    const response = await context.request.get(`${api}/api/v1/rooms/${room}`, { headers: { Authorization: `Bearer ${user}` } })
    expect(response.ok()).toBe(true)
    return response.json()
  }
  const connect = async (page: Page, user: string) => {
    await page.route('https://st.max.ru/**', route => route.fulfill({ contentType: 'text/javascript', body: '' }))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), url = new URL(request.url())
      if (url.pathname === '/api/v1/auth/max/bootstrap') return route.fulfill({ json: { access_token: user, token_type: 'Bearer', expires_in: 3600, user: { id: user, display_name: user === a ? 'Аня' : 'Дима', avatar_url: null, city_id: city, locale: 'ru' }, onboarding_state: 'complete', preferences: null, invite_context: null } })
      if (url.pathname.endsWith('/intent/me') && request.method() === 'PUT') intentWrites += 1
      if (url.pathname.startsWith('/api/v1/rooms/')) return route.fulfill({ response: await route.fetch({ url: `${api}${url.pathname}${url.search}`, headers: { ...request.headers(), authorization: `Bearer ${user}` } }) })
      if (url.pathname.includes('behavior')) return route.fulfill({ status: 202, json: { accepted: 1, duplicates: 0, rejected: 0 } })
      return route.fulfill({ json: { items: [], sections: [], next_cursor: null, total: 0 } })
    })
    await page.goto(`/rooms/${room}/recovery`)
    await expect(page.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
  }
  const screenshot = async (page: Page, name: string) => {
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: process.env.ROOMS_RENDER_DIR ? `${process.env.ROOMS_RENDER_DIR}/${testInfo.project.name}-empty-${name}.png` : testInfo.outputPath(`empty-${name}.png`), fullPage: true, animations: 'disabled' })
  }
  const selectAndReview = async (page: Page, round: number) => {
    if (round === 1) await page.getByRole('checkbox', { name: 'Разрешить любое время' }).check()
    else {
      await page.getByRole('checkbox', { name: 'Увеличить бюджет' }).check()
      await page.getByLabel('Новый лимит стоимости').selectOption('4000')
    }
    await page.getByRole('button', { name: 'Проверить изменения', exact: true }).click()
    await page.getByRole('button', { name: 'Подтвердить мои условия', exact: true }).click()
  }
  try {
    await connect(pageA, a); await connect(pageB, b)
    await screenshot(pageA, 'round-one')
    for (const round of [1, 2] as const) {
      await selectAndReview(pageB, round)
      await expect(pageB.getByText('Условия сохранены', { exact: true })).toBeVisible()
      expect(intentWrites).toBe(round * 2 - 1)
      expect((await snapshot(a)).round_no).toBe(round)
      await screenshot(pageB, `pending-${round}`)
      await expect(pageA.getByText('Друг предложил обновить подбор', { exact: true })).toBeVisible()
      await pageA.getByRole('button', { name: 'Продолжить', exact: true }).click()
      await pageA.getByRole('button', { name: 'Оставить мои условия', exact: true }).click()
      await expect.poll(async () => (await snapshot(a)).round_no).toBe(round + 1)
      expect(intentWrites).toBe(round * 2)
      if (round === 1) {
        await expect(pageA.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
        // B still displays its own saved preparation until it explicitly adopts
        // the started round; do not hide this transition with a reload.
        await expect(pageB.getByText('Подборка обновилась', { exact: true })).toBeVisible()
        await pageB.getByRole('button', { name: 'Продолжить', exact: true }).click()
        await expect(pageB.getByRole('heading', { name: 'Общих событий пока нет' })).toBeVisible()
      }
    }
    const terminal = await snapshot(a)
    expect(terminal).toMatchObject({ state: 'exhausted', round_no: 3, pool: { total: 0, voted_by_me: 0, room_exhausted: true } })
    expect(terminal.next_round ?? null).toBeNull()
    expect(intentWrites).toBe(4)
    const fourthIntent = { ...terminal.my_intent, expected_round_no: terminal.round_no, expected_room_version: terminal.version }
    delete fourthIntent.version; delete fourthIntent.round_no; delete fourthIntent.submitted_at
    const fourth = await context.request.put(`${api}/api/v1/rooms/${room}/intent/me`, { headers: { Authorization: `Bearer ${a}`, 'Idempotency-Key': 'empty-fourth-try' }, data: fourthIntent })
    expect(fourth.status()).toBe(409)
    expect((await snapshot(a)).round_no).toBe(3)
    expect(intentWrites).toBe(4)
    await expect(pageA.getByRole('heading', { name: 'Лимит подборок исчерпан' })).toBeVisible()
    await pageA.reload()
    await expect(pageA.getByRole('heading', { name: 'Лимит подборок исчерпан' })).toBeVisible()
    expect((await snapshot(a)).round_no).toBe(3)
    await screenshot(pageA, 'terminal')
  } finally { await peer.close() }
})
