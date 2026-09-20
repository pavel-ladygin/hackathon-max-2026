import { EVENTS, eventCards, now } from '../../mocks/fixtures'
import { mapDetail, mapEvent } from './mapper'
import { ApiError } from './errors'
import type { EventDetailDto, EventSearchResponseDto, HomeFeedResponseDto, SavedEventsResponseDto, SavedStateResponseDto } from './types'

type RequestFn = <T>(path: string, init?: RequestInit) => Promise<T>
type Event = ReturnType<typeof mapEvent>
type Detail = ReturnType<typeof mapDetail>

export interface DiscoverySearchParams {
  q?: string
  city_id?: string
  date_from?: string
  date_to?: string
  day_types?: string[]
  time_slots?: string[]
  category_slugs?: string[]
  price_max_minor?: number
  distance_m?: number
  lat?: number
  lng?: number
  free_only?: boolean
  limit?: number
  cursor?: string
}

export interface DiscoveryApi {
  getHomeFeed(params?: Record<string, string | number | undefined>): Promise<{ feed_id: string; generated_at: string; sections: Array<Omit<HomeFeedResponseDto['sections'][number], 'items'> & { items: Event[] }>; activeRoom: HomeFeedResponseDto['active_room'] }>
  searchEvents(params?: DiscoverySearchParams): Promise<{ items: Event[]; appliedFilters: Record<string, unknown>; totalEstimate: number; nextCursor: string | null }>
  getEvent(eventId: string): Promise<Detail>
  recordBehavior(events: Array<{ client_event_id: string; type: 'impression' | 'open' | 'share'; occurred_at: string; event_id?: string | null; room_id?: string | null; metadata?: Record<string, unknown> }>): Promise<{ accepted: number; duplicates: number; rejected: number }>
  setSaved(eventId: string, saved: boolean): Promise<SavedStateResponseDto>
  getSaved(params?: { tab?: 'saved' | 'matches'; limit?: number; cursor?: string }): Promise<{ items: Array<{ event: Event; savedAt: string | null; match: SavedEventsResponseDto['items'][number]['match'] }>; nextCursor: string | null }>
  recordTicketClick(eventId: string, input: { source: string; room_id?: string | null }): Promise<{ external_url: string }>
}

export function createHttpDiscoveryApi(request: RequestFn): DiscoveryApi {
  return {
    async getHomeFeed(params = {}) {
      const query = new URLSearchParams(Object.entries(params).filter(([, value]) => value !== undefined).map(([key, value]) => [key, String(value)]))
      const response = await request<HomeFeedResponseDto>(`/feed/home${query.size ? `?${query}` : ''}`)
      return { ...response, sections: response.sections.map((section) => ({ ...section, items: section.items.map(mapEvent) })), activeRoom: response.active_room }
    },
    async searchEvents(params = {}) {
      const query = new URLSearchParams()
      for (const [key, value] of Object.entries(params)) if (value !== undefined) query.set(key, Array.isArray(value) ? value.join(',') : String(value))
      const response = await request<EventSearchResponseDto>(`/events/search${query.size ? `?${query}` : ''}`)
      return { items: response.items.map(mapEvent), appliedFilters: response.applied_filters, totalEstimate: response.total_estimate, nextCursor: response.next_cursor }
    },
    async getEvent(eventId) { return mapDetail(await request<EventDetailDto>(`/events/${eventId}`)) },
    async recordBehavior(events) { return request('/behavior/events:batch', { method: 'POST', body: JSON.stringify({ events }) }) },
    async setSaved(eventId, saved) { return request(`/me/saved-events/${encodeURIComponent(eventId)}`, { method: 'PUT', body: JSON.stringify({ saved }) }) },
    async getSaved(params = {}) {
      const query = new URLSearchParams()
      if (params.tab) query.set('tab', params.tab)
      if (params.limit) query.set('limit', String(params.limit))
      if (params.cursor) query.set('cursor', params.cursor)
      const response = await request<SavedEventsResponseDto>(`/me/saved-events${query.size ? `?${query}` : ''}`)
      return { items: response.items.map((item) => ({ event: mapEvent(item.event), savedAt: item.saved_at, match: item.match })), nextCursor: response.next_cursor }
    },
    async recordTicketClick(eventId, input) { return request(`/events/${eventId}/ticket-click`, { method: 'POST', body: JSON.stringify(input) }) },
  }
}

const SAVED_KEY = 'max-together-discovery-saved-v1'
const BEHAVIOR_KEY = 'max-together-discovery-behavior-v1'
const readJson = <T>(key: string, fallback: T): T => {
  try { return JSON.parse(globalThis.localStorage?.getItem(key) ?? '') as T } catch { return fallback }
}
const writeJson = (key: string, value: unknown) => { try { globalThis.localStorage?.setItem(key, JSON.stringify(value)) } catch { /* Storage is optional in restricted webviews. */ } }

export function createMockDiscoveryApi(): DiscoveryApi {
  const saved = () => readJson<Record<string, string>>(SAVED_KEY, {})
  const cards = () => eventCards().map((event) => ({ ...event, saved: Boolean(saved()[event.id]) }))
  return {
    async getHomeFeed() {
      const items = cards().map(mapEvent)
      return { feed_id: 'feed-demo', generated_at: now(), sections: [{ type: 'hero', title: 'Для вас', items }, { type: 'popular', title: 'Популярное', items: [...items].reverse() }], activeRoom: null }
    },
    async searchEvents(params = {}) {
      const query = params.q?.toLowerCase()
      const categories = params.category_slugs
      const price = params.price_max_minor
      const distance = params.distance_m
      const all = cards().filter((event) => (!query || `${event.title} ${event.venue_name} ${event.subtitle ?? ''}`.toLowerCase().includes(query)) && (!categories?.length || categories.includes(event.category_slug)) && (!params.free_only || event.price_from_minor === 0) && (price === undefined || event.price_from_minor === null || event.price_from_minor <= price) && (distance === undefined || event.distance_m === null || event.distance_m <= distance) && (!params.date_from || event.starts_at.slice(0, 10) >= params.date_from) && (!params.date_to || event.starts_at.slice(0, 10) <= params.date_to))
      const limit = Math.min(50, Math.max(1, params.limit ?? 20))
      const start = Math.max(0, Number(params.cursor ?? 0))
      return { items: all.slice(start, start + limit).map(mapEvent), appliedFilters: Object.fromEntries(Object.entries(params).filter(([, value]) => value !== undefined)), totalEstimate: all.length, nextCursor: start + limit < all.length ? String(start + limit) : null }
    },
    async getEvent(eventId) {
      const event = EVENTS.find((item) => item.id === eventId)
      if (!event) throw new ApiError(404, { error: { code: 'NOT_FOUND', message: 'Событие не найдено', request_id: 'mock-discovery' } })
      return mapDetail({ ...event, saved: Boolean(saved()[eventId]) })
    },
    async recordBehavior(events) {
      const known = readJson<string[]>(BEHAVIOR_KEY, [])
      const seen = new Set(known)
      const fresh = events.filter((event) => { if (seen.has(event.client_event_id)) return false; seen.add(event.client_event_id); return true })
      writeJson(BEHAVIOR_KEY, [...known, ...fresh.map((event) => event.client_event_id)])
      return { accepted: fresh.length, duplicates: events.length - fresh.length, rejected: 0 }
    },
    async setSaved(eventId, isSaved) {
      const state = saved()
      if (isSaved) state[eventId] = now(); else delete state[eventId]
      writeJson(SAVED_KEY, state)
      return { event_id: eventId, saved: isSaved, saved_at: isSaved ? state[eventId] : null }
    },
    async getSaved(params = {}) {
      if (params.tab === 'matches') return { items: [], nextCursor: null }
      const state = saved()
      const ids = Object.keys(state).sort((a, b) => state[b].localeCompare(state[a]))
      const start = Math.max(0, Number(params.cursor ?? 0)); const limit = Math.min(50, Math.max(1, params.limit ?? 20))
      const byId = new Map(cards().map((event) => [event.id, event]))
      const items = ids.slice(start, start + limit).flatMap((id) => { const event = byId.get(id); return event ? [{ event: mapEvent(event), savedAt: state[id], match: null }] : [] })
      return { items, nextCursor: start + limit < ids.length ? String(start + limit) : null }
    },
    async recordTicketClick(eventId) {
      if (!EVENTS.some((event) => event.id === eventId)) throw new ApiError(404, { error: { code: 'NOT_FOUND', message: 'Событие не найдено', request_id: 'mock-discovery' } })
      return { external_url: `https://tickets.example.invalid/events/${encodeURIComponent(eventId)}` }
    },
  }
}

export const discoverySource = (import.meta.env.VITE_DISCOVERY_SOURCE ?? 'mock') === 'http' ? 'http' : 'mock'
