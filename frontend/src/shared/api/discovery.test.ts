import { describe, expect, it, beforeEach } from 'vitest'
import { EVENTS } from '../../mocks/fixtures'
import { createMockDiscoveryApi } from './discovery'

describe('mock discovery adapter', () => {
  beforeEach(() => localStorage.clear())

  it('serves feed, search and detail through the same mapped contract', async () => {
    const api = createMockDiscoveryApi()
    const feed = await api.getHomeFeed()
    expect(feed.activeRoom).toBeNull()
    expect(feed.sections[0].items[0]).toHaveProperty('imageUrl')
    expect((await api.searchEvents({ q: 'Егор' })).items[0].title).toBe('Егор Крид')
    expect((await api.getEvent(EVENTS[0].id)).dataProvenance.is_demo).toBe(true)
  })

  it('keeps saved state local and supports pagination', async () => {
    const api = createMockDiscoveryApi()
    await api.setSaved(EVENTS[0].id, true)
    expect((await api.getSaved()).items.map(({ event }) => event.id)).toEqual([EVENTS[0].id])
    await api.setSaved(EVENTS[0].id, false)
    expect((await api.getSaved()).items).toHaveLength(0)
  })

  it('deduplicates behavior events and returns safe ticket links', async () => {
    const api = createMockDiscoveryApi()
    const event = { client_event_id: 'client-1', type: 'open' as const, occurred_at: new Date().toISOString() }
    expect(await api.recordBehavior([event, event])).toMatchObject({ accepted: 1, duplicates: 1 })
    expect(await api.recordBehavior([event])).toMatchObject({ accepted: 0, duplicates: 1 })
    expect(await api.recordTicketClick(EVENTS[0].id, { source: 'demo' })).toEqual({ external_url: `https://tickets.example.invalid/events/${EVENTS[0].id}` })
  })
})
