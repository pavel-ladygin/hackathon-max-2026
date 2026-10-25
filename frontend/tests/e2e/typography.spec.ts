import { expect, test, type Page } from '@playwright/test'

const user = {
  id: 'typography-e2e-user',
  display_name: 'Тест типографики',
  avatar_url: null,
  city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24',
  locale: 'ru',
}

const preferences = {
  city_id: user.city_id,
  interest_slugs: ['concerts', 'exhibitions', 'food'],
  budget_max_minor: 350_000,
  usual_day_types: ['weekend'],
  usual_time_slots: ['evening'],
  version: 1,
  updated_at: '2026-01-01T00:00:00.000Z',
}

const event = {
  id: 'typography-event',
  title: 'Джазовый вечер в Оранжерее',
  subtitle: 'Живой концерт',
  category_slug: 'concerts',
  starts_at: '2099-01-01T19:00:00Z',
  timezone: 'Europe/Moscow',
  date_label: '1 января 2099, 19:00',
  venue_name: 'Оранжерея на Большой Никитской',
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
  id: 'typography-room',
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
  created_at: '2098-01-01T00:00:00Z',
  expires_at: '2100-01-01T00:00:00Z',
}

async function stubBackend(page: Page) {
  await page.route('https://st.max.ru/js/max-web-app.js', (route) => route.fulfill({ contentType: 'text/javascript', body: '' }))
  await page.route('**/api/v1/auth/max/bootstrap', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ access_token: 'typography-test-token', token_type: 'Bearer', expires_in: 3600, user, onboarding_state: 'complete', preferences, invite_context: null }),
  }))
  await page.route('**/api/v1/feed/home**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ feed_id: 'typography-feed', generated_at: '2098-01-01T00:00:00Z', sections: [{ type: 'for_you', title: 'Для вас', items: [event] }], active_room: null }),
  }))
  await page.route('**/api/v1/event-submissions/options', (route) => route.fulfill({
    json: { cities: [{ id: user.city_id, name: 'Москва', timezone: 'Europe/Moscow' }], categories: [{ slug: 'concerts', label: 'Концерты' }] },
  }))
  await page.route(/\/api\/v1\/events\/search(?:\?.*)?$/, (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ items: [event], applied_filters: {}, total_estimate: 1, next_cursor: null }),
  }))
  await page.route('**/api/v1/events/search/count**', (route) => route.fulfill({ json: { total: 1 } }))
  await page.route(`**/api/v1/events/${event.id}`, (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      ...event,
      title: 'Джазовый вечер в Оранжерее: длинное название для проверки переноса на узком экране',
      description: 'Живой вечер музыки в исторической оранжерее.\n\nВ программе — известные мелодии и новые аранжировки.',
      ends_at: '2099-01-01T21:00:00Z',
      venue: { id: 'typography-venue', name: event.venue_name, address: 'Москва, ул. Большая Никитская, 24', latitude: 55.75, longitude: 37.61, metro: 'Арбатская', district: 'ЦАО' },
      images: [],
      ticket_available: true,
      status: 'published',
      age_rating: '16+',
      data_provenance: { source: 'demo', source_updated_at: null, is_demo: true },
    }),
  }))
  await page.route(`**/api/v1/rooms/${room.id}`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(room) }))
  await page.route(`**/api/v1/rooms/${room.id}/intent/me`, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...room, my_intent: null }) }))
}

async function assertNoHorizontalOverflow(page: Page, width: number) {
  const dimensions = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    document: document.documentElement.scrollWidth,
    body: document.body.scrollWidth,
  }))
  expect(dimensions.viewport).toBe(width)
  expect(dimensions.document).toBeLessThanOrEqual(width)
  expect(dimensions.body).toBeLessThanOrEqual(width)
}

async function apply200PercentText(page: Page) {
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%' })
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe('32px')
}

test.describe('Mini App typography roles', () => {
  test('home body and section heading use app roles despite MAX UI defaults', async ({ page }) => {
    await stubBackend(page)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Для вас', exact: true })).toBeVisible()

    const metrics = await page.evaluate(() => {
      const heading = [...document.querySelectorAll('h2')].find((element) => element.textContent?.trim() === 'Для вас')!
      const body = getComputedStyle(document.body)
      const section = getComputedStyle(heading)
      const maxTheme = document.querySelector('.max-app-theme')!
      return {
        typographyMode: document.documentElement.dataset.appTypography,
        bodySize: body.fontSize,
        bodyFamily: body.fontFamily,
        sectionSize: section.fontSize,
        sectionWeight: section.fontWeight,
        sectionLineHeight: section.lineHeight,
        maxThemeSize: getComputedStyle(maxTheme).fontSize,
      }
    })

    expect(metrics.typographyMode).toBe('mini-app')
    expect(metrics.bodySize).toBe('16px')
    expect(metrics.sectionSize).toBe('20px')
    expect(metrics.sectionWeight).toBe('600')
    expect(metrics.sectionLineHeight).toBe('26px')
    expect(metrics.bodyFamily).toMatch(/-apple-system|BlinkMacSystemFont|Segoe UI/)
    expect(metrics.maxThemeSize).toBe('16px')
  })

  test('event title, body copy, and detail metadata have distinct readable roles', async ({ page }) => {
    await stubBackend(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(`/events/${event.id}`)
    const title = page.getByRole('heading', { name: /длинное название для проверки переноса/ })
    await expect(title).toBeVisible()
    const copy = page.locator('[class*="bodyCopy"]').first()
    await expect(copy).toContainText('В программе')

    const metrics = await page.evaluate((titleText) => {
      const titleElement = [...document.querySelectorAll('h1')].find((element) => element.textContent?.includes(titleText))!
      const bodyCopy = document.querySelector('[class*="bodyCopy"]')!
      const metaLabel = document.querySelector('dt')!
      const style = (element: Element) => {
        const computed = getComputedStyle(element)
        return { size: computed.fontSize, weight: computed.fontWeight, lineHeight: computed.lineHeight }
      }
      return { title: style(titleElement), copy: style(bodyCopy), meta: style(metaLabel), titleScrollWidth: titleElement.scrollWidth, titleClientWidth: titleElement.clientWidth }
    }, 'длинное название для проверки переноса')

    expect(metrics.title.size).toBe('28px')
    expect(metrics.title.weight).toBe('600')
    expect(metrics.title.lineHeight).toBe('35px')
    expect(metrics.copy.size).toBe('16px')
    expect(metrics.copy.lineHeight).toBe('24px')
    expect(metrics.meta.size).toBe('12px')
    expect(metrics.titleScrollWidth).toBeLessThanOrEqual(metrics.titleClientWidth)
  })

  test('room intent controls use consistent chip, helper, and input roles', async ({ page }) => {
    await stubBackend(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(`/rooms/${room.id}/intent`)
    await expect(page.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()

    const metrics = await page.evaluate(() => {
      const chip = document.querySelector('[class*="choiceOptions"] button')!
      const helper = document.querySelector('[id$="-help"]')!
      const textarea = document.querySelector('textarea')!
      const placeholder = getComputedStyle(textarea, '::placeholder')
      const styles = [getComputedStyle(chip), getComputedStyle(helper), getComputedStyle(textarea)]
      return {
        chip: { size: styles[0].fontSize, weight: styles[0].fontWeight, lineHeight: styles[0].lineHeight },
        helper: { size: styles[1].fontSize, lineHeight: styles[1].lineHeight },
        inputSize: styles[2].fontSize,
        placeholderSize: placeholder.fontSize,
        pageHeadingSize: getComputedStyle(document.querySelector('h1')!).fontSize,
      }
    })

    expect(metrics.chip).toEqual({ size: '14px', weight: '500', lineHeight: '21px' })
    expect(metrics.helper).toEqual({ size: '14px', lineHeight: '21px' })
    expect(metrics.inputSize).toBe('16px')
    expect(metrics.placeholderSize).toBe('16px')
    expect(metrics.pageHeadingSize).toBe('28px')
  })

  test('the text-size control scales app text, persists the choice, and preserves input readability', async ({ page }) => {
    await stubBackend(page)
    await page.goto('/preferences')
    const options = page.getByRole('group', { name: 'Размер текста' })

    for (const [value, rootSize, inputSize] of [
      ['compact', '14.4px', '16px'],
      ['normal', '16px', '16px'],
      ['large', '18.4px', '18.4px'],
    ] as const) {
      await options.getByRole('radio', { name: new RegExp(value === 'compact' ? 'Компактный' : value === 'normal' ? 'Обычный' : 'Крупный') }).check()
      await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe(rootSize)
      await page.goto(`/rooms/${room.id}/intent`)
      await expect(page.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()
      await expect.poll(() => page.evaluate(() => getComputedStyle(document.querySelector('textarea')!).fontSize)).toBe(inputSize)
      await page.reload()
      await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe(rootSize)
      await page.goto('/preferences')
      await expect(page.getByRole('group', { name: 'Размер текста' }).getByRole('radio', { name: new RegExp(value === 'compact' ? 'Компактный' : value === 'normal' ? 'Обычный' : 'Крупный') })).toBeChecked()
    }

    await expect(page.getByRole('heading', { name: 'Настройте афишу' })).toBeVisible()
  })

  test('large text remains readable at 200% on home, event, submission, and room screens', async ({ page }) => {
    await stubBackend(page)

    for (const width of [320, 375, 390, 430, 1440]) {
      await page.setViewportSize({ width, height: 844 })
      await page.goto('/')
      await apply200PercentText(page)
      await expect(page.getByRole('heading', { name: 'Для вас', exact: true })).toBeVisible()
      await assertNoHorizontalOverflow(page, width)

      await page.goto(`/events/${event.id}`)
      await apply200PercentText(page)
      const eventTitle = page.getByRole('heading', { name: /длинное название для проверки переноса/ })
      await expect(eventTitle).toBeVisible()
      const eventTitleBox = await eventTitle.boundingBox()
      const eventTitleWidth = await eventTitle.evaluate((element) => ({ scroll: element.scrollWidth, client: element.clientWidth }))
      expect(eventTitleBox!.height).toBeGreaterThan(35)
      expect(eventTitleWidth.scroll).toBeLessThanOrEqual(eventTitleWidth.client)
      await assertNoHorizontalOverflow(page, width)

      await page.goto('/events/suggest')
      await apply200PercentText(page)
      const submissionTitle = page.getByRole('heading', { name: 'Добавьте событие в афишу' })
      await expect(submissionTitle).toBeVisible()
      const titleInput = page.locator('input[name="title"]')
      const titleInputMetrics = await titleInput.evaluate((input) => {
        const style = getComputedStyle(input)
        const rect = input.getBoundingClientRect()
        const parent = input.parentElement!.getBoundingClientRect()
        const overflowing = [...document.querySelectorAll<HTMLElement>('body *')]
          .map((element) => ({ tag: element.tagName, className: typeof element.className === 'string' ? element.className : '', left: element.getBoundingClientRect().left, right: element.getBoundingClientRect().right, width: element.getBoundingClientRect().width }))
          .filter((element) => element.right > document.documentElement.clientWidth + 1)
          .sort((a, b) => b.right - a.right)
          .slice(0, 5)
        return { size: style.fontSize, left: rect.left, right: rect.right, width: input.clientWidth, scrollWidth: input.scrollWidth, rootSize: getComputedStyle(document.documentElement).fontSize, inlineRootSize: document.documentElement.style.fontSize, token: getComputedStyle(input).getPropertyValue('--app-type-input-size'), parentLeft: parent.left, parentRight: parent.right, documentWidth: document.documentElement.scrollWidth, viewportWidth: document.documentElement.clientWidth, overflowing }
      })
      expect(Number.parseFloat(titleInputMetrics.size), JSON.stringify(titleInputMetrics)).toBeGreaterThanOrEqual(32)
      expect(titleInputMetrics.left).toBeGreaterThanOrEqual(0)
      expect(titleInputMetrics.right, JSON.stringify(titleInputMetrics)).toBeLessThanOrEqual(width + 1)
      expect(titleInputMetrics.scrollWidth).toBeLessThanOrEqual(titleInputMetrics.width)
      await assertNoHorizontalOverflow(page, width)

      await page.goto(`/rooms/${room.id}/intent`)
      await apply200PercentText(page)
      const title = page.getByRole('heading', { name: 'Мои предпочтения' })
      await expect(title).toBeVisible()
      const titleBox = await title.boundingBox()
      const firstGroup = await page.locator('form fieldset').first().boundingBox()
      const save = await page.getByRole('button', { name: 'Сохранить предпочтения' }).boundingBox()
      expect(titleBox).not.toBeNull()
      expect(firstGroup).not.toBeNull()
      expect(save).not.toBeNull()
      expect(titleBox!.height).toBeGreaterThan(35)
      expect(firstGroup!.y).toBeGreaterThan(titleBox!.y + titleBox!.height)
      expect(save!.x).toBeGreaterThanOrEqual(0)
      expect(save!.x + save!.width).toBeLessThanOrEqual(width + 1)
      await assertNoHorizontalOverflow(page, width)
    }
  })
})
