import { expect, test, type Page } from '@playwright/test'

const cityId = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
const user = { id: 'compact-card-user', display_name: 'Тест карточек', avatar_url: null, city_id: cityId, locale: 'ru' }
const preferences = { city_id: cityId, interest_slugs: ['concerts', 'exhibitions'], budget_max_minor: 350_000, usual_day_types: ['weekend'], usual_time_slots: ['evening'], version: 1, updated_at: '2026-01-01T00:00:00.000Z' }

const problemEvents = [
  {
    id: 'compact-card-long-title',
    title: 'Обзорная экскурсия по выставке «Триумф сюрреализма. Империя Дали» — расширенная программа с посещением всех залов',
    venue_name: 'Сад им. Н. Э. Баумана, улица Старая Басманная, дом 15А, строение 4, встреча у центрального входа',
    date_label: '25 октября, 13:15',
    price_label: 'от 850 ₽',
  },
  {
    id: 'compact-card-long-place',
    title: 'Интимные тайны Москвы (18+)',
    venue_name: 'Метро Сухаревская, выход из метро № 2, сразу на выходе из метро вас встретит гид с красной табличкой «Любимый город»',
    date_label: '25 октября, 13:30',
    price_label: 'от 1 700 ₽',
  },
  {
    id: 'compact-card-long-everything',
    title: 'Застава границы времени и пространства: большая историческая прогулка по старинным улицам и переулкам города',
    venue_name: 'Встречаемся у выхода № 3 на станции метро «Площадь Ильича» или «Римская». Экскурсия завершится у станции метро «Таганская» кольцевой линии.',
    date_label: '25 октября, 14:00',
    price_label: 'от 1 500 ₽',
  },
].map((event) => ({
  ...event,
  subtitle: null,
  category_slug: 'walks',
  starts_at: '2099-10-25T10:15:00Z',
  timezone: 'Europe/Moscow',
  distance_m: null,
  distance_label: null,
  price_from_minor: 85000,
  currency: 'RUB' as const,
  image_url: null,
  saved: false,
  reasons: [],
}))

const heroEvent = {
  ...problemEvents[0],
  id: 'compact-card-home-hero',
  title: 'Главное событие афиши',
  venue_name: 'Центр Москвы',
  date_label: '25 октября, 12:00',
}

async function stubBackend(page: Page) {
  const savedEventIds = new Set<string>()
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ access_token: 'compact-card-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
  }))
  await page.route('**/api/v1/feed/home**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ feed_id: 'compact-card-feed', generated_at: '2098-01-01T00:00:00Z', sections: [{ type: 'for_you', title: 'Для вас', items: [heroEvent, ...problemEvents] }], active_room: null }),
  }))
  await page.route(/\/api\/v1\/events\/search(?:\?.*)?$/, (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: problemEvents.map((event) => ({ ...event, saved: savedEventIds.has(event.id) })), applied_filters: {}, total_estimate: problemEvents.length, next_cursor: null }),
  }))
  await page.route('**/api/v1/events/search/count**', (route) => route.fulfill({ json: { total: problemEvents.length } }))
  await page.route(/\/api\/v1\/me\/saved-events(?:\?.*)?$/, (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: problemEvents.map((event) => ({ event: { ...event, saved: true }, saved_at: '2098-01-01T00:00:00Z', match: null })), next_cursor: null }),
  }))
  await page.route(/\/api\/v1\/me\/saved-events\/([^/?]+)$/, async (route) => {
    const eventId = new URL(route.request().url()).pathname.split('/').at(-1)!
    const { saved } = route.request().postDataJSON() as { saved: boolean }
    if (saved) savedEventIds.add(eventId)
    else savedEventIds.delete(eventId)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ event_id: eventId, saved, saved_at: saved ? '2098-01-01T00:00:00Z' : null }),
    })
  })
}

const fullTitle = problemEvents[0].title

async function cardMetrics(page: Page, title: string) {
  const card = page.locator('[class*="eventCard"]').filter({ has: page.locator('strong', { hasText: title }) }).first()
  await expect(card).toBeVisible()
  return card.evaluate((element) => {
    const titleElement = element.querySelector('strong')!
    const place = element.querySelector('[class*="eventPlace"]')!
    const price = element.querySelector('[class*="eventPrice"]')!
    const eyebrow = element.querySelector('[class*="eyebrow"]')!
    const image = element.querySelector('[class*="eventImage"]')!
    const favorite = element.parentElement?.querySelector('[aria-label*="Сохранить"], [aria-label*="Убрать"]')
    const rect = (node: Element) => {
      const box = node.getBoundingClientRect()
      return { top: box.top, bottom: box.bottom, left: box.left, right: box.right, centerY: box.top + box.height / 2, height: box.height }
    }
    const titleStyle = getComputedStyle(titleElement)
    const placeStyle = getComputedStyle(place)
    const eyebrowStyle = getComputedStyle(eyebrow)
    const priceStyle = getComputedStyle(price)
    const cardRect = rect(element)
    return {
      fullTitle: titleElement.textContent,
      fullPlace: place.textContent,
      fullPrice: price.textContent,
      titleLineClamp: titleStyle.webkitLineClamp,
      titleLineHeight: Number.parseFloat(titleStyle.lineHeight),
      titleHeight: rect(titleElement).height,
      placeLineClamp: placeStyle.webkitLineClamp,
      placeLineHeight: Number.parseFloat(placeStyle.lineHeight),
      placeHeight: rect(place).height,
      eyebrowLineClamp: eyebrowStyle.webkitLineClamp,
      eyebrowLineHeight: Number.parseFloat(eyebrowStyle.lineHeight),
      eyebrowHeight: rect(eyebrow).height,
      priceClamp: priceStyle.webkitLineClamp,
      image: rect(image),
      favorite: favorite ? rect(favorite) : null,
      card: cardRect,
      priceBounds: rect(price),
      imageBounds: rect(image),
      hasSeparatePrice: price.parentElement === place.parentElement,
    }
  })
}

async function assertNoHorizontalOverflow(page: Page, width: number) {
  const sizes = await page.evaluate(() => ({ viewport: document.documentElement.clientWidth, document: document.documentElement.scrollWidth, body: document.body.scrollWidth }))
  expect(sizes.viewport).toBe(width)
  expect(sizes.document).toBeLessThanOrEqual(width)
  expect(sizes.body).toBeLessThanOrEqual(width)
}

test.describe('compact event cards', () => {
  test('home, catalog, and saved cards clamp long text, keep price visible, and center image and favorite', async ({ page }) => {
    await stubBackend(page)
    await page.setViewportSize({ width: 390, height: 844 })

    for (const path of ['/', '/events', '/saved']) {
      await page.goto(path)
      for (const event of problemEvents) {
        const card = page.locator('[class*="eventCard"]').filter({ has: page.locator('strong', { hasText: event.title }) }).first()
        await expect(card).toBeVisible()
        await expect(card).toContainText(event.title)
        await expect(card).toContainText(event.venue_name)
        await expect(card).toContainText(event.price_label)
        const metrics = await cardMetrics(page, event.title)

        expect(metrics.fullTitle).toBe(event.title)
        expect(metrics.fullPlace).toBe(event.venue_name)
        expect(metrics.fullPrice).toBe(event.price_label)
        expect(metrics.titleLineClamp).toBe('2')
        expect(metrics.titleHeight).toBeLessThanOrEqual(metrics.titleLineHeight * 2 + 1)
        expect(metrics.placeLineClamp).toBe('1')
        expect(metrics.placeHeight).toBeLessThanOrEqual(metrics.placeLineHeight + 1)
        expect(metrics.eyebrowLineClamp).toBe('2')
        expect(metrics.eyebrowHeight).toBeLessThanOrEqual(metrics.eyebrowLineHeight * 2 + 1)
        expect(metrics.priceClamp).toBe('none')
        expect(metrics.hasSeparatePrice).toBe(true)
        expect(metrics.image.centerY).toBeCloseTo(metrics.card.centerY, 0)
        expect(metrics.priceBounds.left).toBeGreaterThanOrEqual(metrics.card.left)
        expect(metrics.priceBounds.right).toBeLessThanOrEqual(metrics.card.right)

        if (path !== '/') {
          expect(metrics.favorite).not.toBeNull()
          expect(metrics.favorite!.centerY).toBeCloseTo(metrics.card.centerY, 0)
          expect(metrics.favorite!.left).toBeGreaterThanOrEqual(metrics.card.left)
          expect(metrics.favorite!.left).toBeGreaterThanOrEqual(metrics.priceBounds.right - 1)
          await expect(card).toHaveAccessibleName(new RegExp(event.title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
        }
      }
    }

    // The main promotional card keeps its existing content and presentation.
    await page.goto('/')
    const hero = page.getByRole('button', { name: /Главное событие афиши/ })
    await expect(hero).toBeVisible()
    await expect(hero).toContainText('Главное событие афиши')
  })

  test('favorite action stays separate from event navigation', async ({ page }) => {
    await stubBackend(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/events')
    const title = problemEvents[0].title
    const favorite = page.getByRole('button', { name: `Сохранить «${title}»` })
    await expect(favorite).toHaveAttribute('aria-pressed', 'false')
    await favorite.click()
    await expect(page).toHaveURL(/\/events(?:\?.*)?$/)
    const savedFavorite = page.getByRole('button', { name: `Убрать «${title}» из сохранённых` })
    await expect(savedFavorite).toHaveAttribute('aria-pressed', 'true')
    await savedFavorite.click()
    await expect(favorite).toHaveAttribute('aria-pressed', 'false')
    await expect(page).toHaveURL(/\/events(?:\?.*)?$/)
  })

  test('cards reflow without horizontal overflow at narrow widths and increased text sizes', async ({ page }) => {
    await stubBackend(page)
    for (const width of [320, 390, 430, 1440]) {
      await page.setViewportSize({ width, height: 844 })
      for (const [scale, rootSize] of [['90%', '14.4px'], ['115%', '18.4px'], ['200%', '32px']] as const) {
        await page.goto('/events')
        await page.evaluate((fontSize) => { document.documentElement.style.fontSize = fontSize }, scale)
        await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe(rootSize)
        const title = problemEvents[2].title
        const metrics = await cardMetrics(page, title)
        expect(metrics.titleHeight).toBeLessThanOrEqual(metrics.titleLineHeight * 2 + 1)
        expect(metrics.placeHeight).toBeLessThanOrEqual(metrics.placeLineHeight + 1)
        expect(metrics.favorite).not.toBeNull()
        expect(metrics.image.centerY).toBeCloseTo(metrics.card.centerY, 0)
        expect(metrics.favorite!.centerY).toBeCloseTo(metrics.card.centerY, 0)
        expect(metrics.priceBounds.right).toBeLessThanOrEqual(metrics.card.right)
        await assertNoHorizontalOverflow(page, width)
      }
    }
  })

  test('long card title remains part of its accessible event control name', async ({ page }) => {
    await stubBackend(page)
    await page.setViewportSize({ width: 320, height: 844 })
    await page.goto('/events')
    const card = page.locator('[class*="eventCard"]').filter({ has: page.locator('strong', { hasText: fullTitle }) }).first()
    await expect(card).toBeVisible()
    await expect(card).toHaveAccessibleName(new RegExp(fullTitle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    await expect(card).toContainText(problemEvents[0].venue_name)
  })
})
