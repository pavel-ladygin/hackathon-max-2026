import { expect, test, type Page } from '@playwright/test'

const user = { id: 'ui-review-user', display_name: 'Павел', avatar_url: null, city_id: 'ui-moscow', locale: 'ru' }
const payload = { title: 'Прогулка на катере', description: 'Вечерняя прогулка по реке.', city_id: user.city_id, venue_name: 'Яхт-клуб', venue_address: 'Набережная, 10', starts_at_local: '2099-06-01T16:40', category_slug: 'walks', price_mode: 'free', ticket_url: '' }
const submission = { id: 'ui-submission', author_id: user.id, author_name: user.display_name, city_id: user.city_id, starts_at: '2099-06-01T13:40:00Z', timezone: 'Europe/Moscow', payload, status: 'pending', created_at: '2099-05-31T13:40:00Z', decided_at: null, decided_by: null, rejection_reason: null, event_id: null }

async function stubBackend(page: Page) {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/max/bootstrap')) return route.fulfill({ json: { access_token: 'ui-test-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences: null, daily_notifications_enabled: false, invite_context: null } })
    if (path.endsWith('/event-submissions/options')) return route.fulfill({ json: { cities: [{ id: user.city_id, name: 'Москва', timezone: 'Europe/Moscow' }], categories: [{ slug: 'walks', label: 'Прогулки' }] } })
    if (path.endsWith('/events/search/count')) return route.fulfill({ json: { total: 0 } })
    if (path.endsWith('/events/search')) return route.fulfill({ json: { items: [], applied_filters: {}, total_estimate: 0, next_cursor: null } })
    if (path.endsWith('/events/map')) return route.fulfill({ json: { items: [] } })
    if (path.endsWith('/internal/event-submissions/domains')) return route.fulfill({ json: { domains: [] } })
    if (path.endsWith('/internal/event-submissions/ui-submission')) return route.fulfill({ json: { submission, duplicates: [], resource_domains: [] } })
    if (path.endsWith('/internal/event-submissions')) return route.fulfill({ json: { items: [submission], total: 1, counts: { pending: 1, approved: 0, rejected: 0 } } })
    if (path.endsWith('/internal/event-sources')) return route.fulfill({ json: { sources: [] } })
    if (path.endsWith('/internal/analytics/dashboard')) return route.fulfill({ status: 503, json: { error: { code: 'UNAVAILABLE' } } })
    return route.fulfill({ json: { accepted: 0, duplicates: 0, rejected: 0 } })
  })
}

for (const width of [320, 375, 390]) {
  test.describe(`UI consistency at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('poster icon is keyboard operable and form fields share typography', async ({ page }, testInfo) => {
      await stubBackend(page)
      await page.goto('/events/suggest')
      const choose = page.getByRole('button', { name: 'Выбрать файл афиши' })
      await expect(choose).toBeVisible()
      await expect(page.getByRole('button', { name: 'Выбрать файл', exact: true })).toHaveCount(0)
      await choose.focus()
      const chooserPromise = page.waitForEvent('filechooser')
      await page.keyboard.press('Enter')
      await (await chooserPromise).setFiles([])
      const typography = await page.locator('input[name="title"], #event-start-date, #event-start-time, [role="combobox"]').evaluateAll((elements) => elements.map((element) => {
        const style = getComputedStyle(element)
        return { family: style.fontFamily, size: style.fontSize }
      }))
      expect(typography.length).toBeGreaterThanOrEqual(4)
      expect(new Set(typography.map((style) => style.family)).size).toBe(1)
      expect(typography.every((style) => style.size === '16px')).toBe(true)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      await page.getByRole('group', { name: 'Зона загрузки афиши' }).screenshot({ path: testInfo.outputPath('poster-icon-button.png') })
    })

    test('opaque map message passes pointer gestures through to the map', async ({ page }, testInfo) => {
      await stubBackend(page)
      await page.addInitScript(() => {
        class Layer {}
        class MapStub {
          constructor(container: HTMLElement) {
            container.style.background = '#e8eee5'
            container.dataset.pointerStarts = '0'
            container.addEventListener('pointerdown', () => { container.dataset.pointerStarts = String(Number(container.dataset.pointerStarts) + 1) })
          }
          addChild() { return this }
          removeChild() { return this }
          destroy() {}
        }
        Object.assign(window, { ymaps3: { ready: Promise.resolve(), YMap: MapStub, YMapDefaultSchemeLayer: Layer, YMapDefaultFeaturesLayer: Layer, YMapMarker: Layer, YMapListener: Layer } })
      })
      await page.goto('/events')
      await page.getByRole('tab', { name: 'Карта', exact: true }).click()
      const title = page.getByRole('heading', { name: 'В этой области событий не найдено' })
      await expect(title).toBeVisible()
      const card = title.locator('..')
      expect(await card.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe('rgb(255, 255, 255)')
      const map = page.getByRole('region', { name: 'Яндекс Карта событий' })
      await title.scrollIntoViewIfNeeded()
      const box = await title.boundingBox()
      expect(box).not.toBeNull()
      await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2)
      await page.mouse.down()
      await page.mouse.move(box!.x + box!.width / 2 + 20, box!.y + box!.height / 2 + 20)
      await page.mouse.up()
      await expect(map).toHaveAttribute('data-pointer-starts', '1')
      await page.getByLabel('Карта событий', { exact: true }).screenshot({ path: testInfo.outputPath('map-empty-card.png') })
    })
  })
}

test('application and all internal pages use the same system font', async ({ page }, testInfo) => {
  await stubBackend(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  const families: string[] = []
  for (const path of ['/preferences', '/events/suggest', '/internal/event-moderation', '/internal/event-sources', '/internal/analytics']) {
    await page.goto(path)
    const heading = page.getByRole('heading', { level: 1 }).first()
    await expect(heading).toBeVisible()
    families.push(await heading.evaluate((element) => getComputedStyle(element).fontFamily))
    if (path === '/internal/event-moderation') {
      await expect(page.getByRole('button', { name: /Прогулка на катере/ })).toBeVisible()
      await page.screenshot({ path: testInfo.outputPath('moderation-desktop.png'), fullPage: true })
      await page.getByRole('button', { name: /Прогулка на катере/ }).click()
      await expect(page.getByRole('heading', { name: 'Прогулка на катере' })).toBeVisible()
      await page.screenshot({ path: testInfo.outputPath('moderation-selected.png'), fullPage: true })
    }
  }
  expect(new Set(families).size).toBe(1)
})
