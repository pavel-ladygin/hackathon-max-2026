import { describe, expect, it } from 'vitest'
import { collectMapEvents, shouldFetchMapPage } from './mapResults'
import { radiusForViewport } from './yandexMaps'

const event = (id: string) => ({ id }) as { id: string }

describe('map result loading', () => {
  it('collects distinct results across pages and caps them at 150', () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => event(`event-${index}`))
    const secondPage = [event('event-99'), ...Array.from({ length: 100 }, (_, index) => event(`event-${index + 100}`))]
    const found = collectMapEvents([{ items: firstPage }, { items: secondPage }], 150)

    expect(found).toHaveLength(150)
    expect(new Set(found.map((item) => item.id)).size).toBe(150)
    expect(found.at(-1)?.id).toBe('event-149')
  })

  it('requests another page until the cap, then stops during active fetches', () => {
    expect(shouldFetchMapPage(50, 150, true, false)).toBe(true)
    expect(shouldFetchMapPage(150, 150, true, false)).toBe(false)
    expect(shouldFetchMapPage(50, 150, true, true)).toBe(false)
    expect(shouldFetchMapPage(50, 150, false, false)).toBe(false)
  })
})

describe('radiusForViewport', () => {
  it('keeps searches inside the backend radius cap and grows as the map zooms out', () => {
    const close = radiusForViewport(15, 700, 500)
    const far = radiusForViewport(8, 700, 500)
    expect(close).toBeGreaterThanOrEqual(1_000)
    expect(far).toBeGreaterThan(close)
    expect(radiusForViewport(1, 700, 500)).toBe(50_000)
  })
})
