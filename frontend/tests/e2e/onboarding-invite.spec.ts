import { expect, test, type Page } from '@playwright/test'

const cityId = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
async function stubNewUser(page: Page, initialInvite: boolean) {
  let complete = false
  let preferences: Record<string, unknown> | null = null
  let saves = 0
  let joins = 0
  let saveGate: Promise<void> | null = null
  await page.addInitScript((invited) => {
    window.WebApp = { initData: invited ? 'signed-invite-a' : 'signed-no-invite', initDataUnsafe: invited ? { start_param: 'invite-a' } : {}, platform: 'web', ready: () => {} }
  }, initialInvite)
  await page.route('https://st.max.ru/js/max-web-app.js', route => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/max/bootstrap')) {
      const input = route.request().postDataJSON() as { init_data: string }
      const token = input.init_data === 'signed-invite-a' ? 'invite-a' : input.init_data === 'signed-invite-b' ? 'invite-b' : null
      return route.fulfill({ json: {
        access_token: 'local-test-session', user: { id: 'new-user', display_name: 'Анна', avatar_url: null, city_id: cityId, locale: 'ru' },
        onboarding_state: complete ? 'complete' : 'new', preferences, daily_notifications_enabled: false, shared_event_id: null,
        invite_context: token ? { token, room_id: `room-${token}`, room_name: 'Вечер вместе', city_id: cityId, public_plan: null, inviter: { id: 'creator', display_name: token === 'invite-a' ? 'Павел' : 'Сергей', avatar_url: null, role: 'creator', intent_ready: true }, expires_at: '2099-01-01T00:00:00Z', already_joined: false, status: 'joinable' } : null,
      } })
    }
    if (path.endsWith('/me/preferences') && route.request().method() === 'PUT') {
      saves++
      if (saveGate) await saveGate
      complete = true
      preferences = { ...route.request().postDataJSON(), version: 1, updated_at: '2026-10-25T12:00:00Z' }
      return route.fulfill({ json: preferences })
    }
    if (path.includes('/room-invites/') && path.endsWith('/join')) { joins++; return route.fulfill({ status: 409, json: { error: { code: 'ROOM_FULL', message: 'Unexpected automatic join' } } }) }
    if (path.endsWith('/feed/home')) return route.fulfill({ json: { feed_id: 'local-feed', generated_at: '2026-10-25T12:00:00Z', sections: [], active_room: null } })
    if (path.endsWith('/events/search')) return route.fulfill({ json: { items: [], total_estimate: 0, applied_filters: {}, next_cursor: null } })
    if (path.endsWith('/events/search/count')) return route.fulfill({ json: { total: 0 } })
    if (path.endsWith('/behavior/events:batch')) return route.fulfill({ status: 202, json: { accepted: 1, duplicates: 0, rejected: 0 } })
    return route.fulfill({ status: 404, json: { error: { code: 'NOT_FOUND', message: 'No local fixture' } } })
  })
  return { get saves() { return saves }, get joins() { return joins }, deferSave() { let release!: () => void; saveGate = new Promise<void>(resolve => { release = resolve }); return release } }
}

async function openProfile(page: Page) {
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Что вам интересно?' })).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Настроим подборку' })).toBeVisible()
}

for (const invited of [true, false]) {
  test(`BUG-02 onboarding ${invited ? 'preserves its original invitation' : 'without invitation finishes at home'}`, async ({ page }) => {
    const session = await stubNewUser(page, invited)
    await openProfile(page)
    await page.getByRole('button', { name: 'Сформировать подборку' }).click()
    await expect.poll(() => session.saves).toBe(1)
    await page.waitForLoadState('networkidle')
    await expect(page).toHaveURL(invited ? /\/join\/invite-a$/ : /\/$/)
    if (invited) await expect(page.getByRole('heading', { name: 'Вас приглашает Павел' })).toBeVisible()
    expect(session.joins).toBe(0)
  })
}

test('BUG-02 the latest invitation survives an older pending onboarding save', async ({ page }) => {
  const session = await stubNewUser(page, true)
  await openProfile(page)
  const release = session.deferSave()
  await page.getByRole('button', { name: 'Сформировать подборку' }).click()
  await expect.poll(() => session.saves).toBe(1)
  const newBootstrap = page.waitForResponse(response => response.url().endsWith('/auth/max/bootstrap') && response.request().postDataJSON().init_data === 'signed-invite-b')
  await page.evaluate(() => {
    Object.defineProperty(window.WebApp!, 'initData', { configurable: true, value: 'signed-invite-b' })
    Object.defineProperty(window.WebApp!, 'initDataUnsafe', { configurable: true, value: { start_param: 'invite-b' } })
    window.dispatchEvent(new Event('focus'))
  })
  await (await newBootstrap).finished()
  const saved = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/me/preferences'))
  release()
  await (await saved).finished()
  await page.waitForLoadState('networkidle')
  await expect(page).toHaveURL(/\/join\/invite-b$/)
  await expect(page.getByRole('heading', { name: 'Вас приглашает Сергей' })).toBeVisible()
  expect(session.saves).toBe(1)
  expect(session.joins).toBe(0)
})
