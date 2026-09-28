import { describe, expect, it, vi } from 'vitest'
import { createHttpDiscoveryApi } from './discovery'

describe('http discovery adapter', () => {
  it('maps feed and search responses', async () => {
    const event = { id: '1', title: 'Event', subtitle: null, category_slug: 'concerts', starts_at: '2026-01-01T00:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января', venue_name: 'Venue', distance_m: null, distance_label: null, price_from_minor: 0, currency: 'RUB', price_label: 'Бесплатно', image_url: null, saved: false, reasons: [] }
    const request = vi.fn(async (path: string) => path.startsWith('/feed') ? { feed_id: 'feed', generated_at: '', sections: [{ type: 'hero', title: 'Hero', items: [event] }], active_room: null } : { items: [event], applied_filters: {}, total_estimate: 1, next_cursor: null })
    const api = createHttpDiscoveryApi(request as never)
    expect((await api.getHomeFeed()).sections[0].items[0].id).toBe('1')
    expect((await api.searchEvents()).items).toHaveLength(1)
  })

  it('disables inline totals for search and requests the separate count with abort signals', async () => {
    const request = vi.fn(async (path: string) => path.startsWith('/events/search/count')
      ? { total: 42 }
      : { items: [], applied_filters: {}, total_estimate: null, next_cursor: null })
    const api = createHttpDiscoveryApi(request as never)
    const searchSignal = new AbortController().signal
    const countSignal = new AbortController().signal

    const page = await api.searchEvents({ q: 'jazz', limit: 24 }, searchSignal)
    const count = await api.getSearchCount({ q: 'jazz' }, countSignal)

    expect(page.totalEstimate).toBeNull()
    expect(count).toBe(42)
    expect(request).toHaveBeenNthCalledWith(1, '/events/search?q=jazz&limit=24&include_total=false', { signal: searchSignal })
    expect(request).toHaveBeenNthCalledWith(2, '/events/search/count?q=jazz', { signal: countSignal })
  })

  it('requests map bounds with integer zoom and maps wrapped singleton events', async () => {
    const event = { id: 'map-1', title: 'Map event', subtitle: null, category_slug: 'concerts', starts_at: '2026-01-01T00:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января', venue_name: 'Venue', distance_m: null, distance_label: null, price_from_minor: 0, currency: 'RUB', price_label: 'Бесплатно', image_url: null, saved: false, reasons: [], latitude: 55.75, longitude: 37.61 }
    const request = vi.fn(async (path: string) => path.startsWith('/events/map?') ? { items: [{ kind: 'event', id: 'map-1', latitude: 55.75, longitude: 37.61, event }] } : { items: [], applied_filters: {}, total_estimate: 0, next_cursor: null })
    const api = createHttpDiscoveryApi(request as never)
    const result = await api.getMapEvents({ west: 170, south: -10, east: -170, north: 10, zoom: 12, q: 'jazz' })
    expect(request).toHaveBeenCalledWith('/events/map?west=170&south=-10&east=-170&north=10&zoom=12&q=jazz')
    expect(result[0]).toMatchObject({ kind: 'event', id: 'map-1', event: { id: 'map-1', latitude: 55.75, longitude: 37.61 } })
  })

  it('includes the nearby radius and user coordinates in map requests', async () => {
    const request = vi.fn(async () => ({ items: [] }))
    const api = createHttpDiscoveryApi(request as never)

    await api.getMapEvents({ west: 37, south: 55, east: 38, north: 56, zoom: 11, lat: 55.75, lng: 37.61, distance_m: 10_000 })

    expect(request).toHaveBeenCalledWith('/events/map?west=37&south=55&east=38&north=56&zoom=11&lat=55.75&lng=37.61&distance_m=10000')
  })

  it('maps event cards nested inside a cluster', async () => {
    const event = { id: 'nested-1', title: 'Nested event', subtitle: null, category_slug: 'concerts', starts_at: '2026-01-01T00:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января', venue_name: 'Venue', distance_m: null, distance_label: null, price_from_minor: 0, currency: 'RUB', price_label: 'Бесплатно', image_url: null, saved: false, reasons: [], latitude: 55.75, longitude: 37.61 }
    const member = { kind: 'event', id: event.id, latitude: 55.75, longitude: 37.61, event }
    const request = vi.fn(async () => ({ items: [{ kind: 'cluster', id: 'cluster-1', latitude: 55.75, longitude: 37.61, west: 37.6, south: 55.7, east: 37.7, north: 55.8, count: 2, members: [member, { ...member, id: 'nested-2', event: { ...event, id: 'nested-2' } }] }] }))
    const api = createHttpDiscoveryApi(request as never)

    const result = await api.getMapEvents({ west: 37.6, south: 55.7, east: 37.7, north: 55.8, zoom: 11 })

    expect(result[0]).toMatchObject({ kind: 'cluster', count: 2, members: [{ event: { id: 'nested-1', imageUrl: null } }, { event: { id: 'nested-2', imageUrl: null } }] })
  })
})
