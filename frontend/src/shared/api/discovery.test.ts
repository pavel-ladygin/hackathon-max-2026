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

  it('requests map bounds with integer zoom and maps wrapped singleton events', async () => {
    const event = { id: 'map-1', title: 'Map event', subtitle: null, category_slug: 'concerts', starts_at: '2026-01-01T00:00:00Z', timezone: 'Europe/Moscow', date_label: '1 января', venue_name: 'Venue', distance_m: null, distance_label: null, price_from_minor: 0, currency: 'RUB', price_label: 'Бесплатно', image_url: null, saved: false, reasons: [], latitude: 55.75, longitude: 37.61 }
    const request = vi.fn(async (path: string) => path.startsWith('/events/map?') ? { items: [{ kind: 'event', id: 'map-1', latitude: 55.75, longitude: 37.61, event }] } : { items: [], applied_filters: {}, total_estimate: 0, next_cursor: null })
    const api = createHttpDiscoveryApi(request as never)
    const result = await api.getMapEvents({ west: 170, south: -10, east: -170, north: 10, zoom: 12, q: 'jazz' })
    expect(request).toHaveBeenCalledWith('/events/map?west=170&south=-10&east=-170&north=10&zoom=12&q=jazz')
    expect(result[0]).toMatchObject({ kind: 'event', id: 'map-1', event: { id: 'map-1', latitude: 55.75, longitude: 37.61 } })
  })
})
