import { mapDetail, mapEvent } from './mapper'
import type { EventDetailDto, EventMapItemDto, EventSearchResponseDto, HomeFeedResponseDto, MapBounds, SavedEventsResponseDto, SavedStateResponseDto } from './types'

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
  getHomeFeed(params?: Record<string, string | number | undefined>): Promise<{ feed_id: string; generated_at: string; sections: Array<Omit<HomeFeedResponseDto['sections'][number], 'items'> & { items: Event[] }>; activeRoom: HomeFeedResponseDto['active_room']; roomClosedNotice: HomeFeedResponseDto['room_closed_notice'] }>
  searchEvents(params?: DiscoverySearchParams): Promise<{ items: Event[]; appliedFilters: Record<string, unknown>; totalEstimate: number; nextCursor: string | null }>
  getMapEvents(params: MapBounds & { zoom: number } & Omit<DiscoverySearchParams, 'limit' | 'cursor'>): Promise<Array<{ kind: 'event'; id: string; longitude: number; latitude: number; event: Event } | (Omit<Extract<EventMapItemDto, { kind: 'cluster' }>, 'members'> & { members: Array<{ kind: 'event'; id: string; longitude: number; latitude: number; event: Event }> })>>
  getEvent(eventId: string): Promise<Detail>
  recordBehavior(events: AnalyticsEvent[], keepalive?: boolean): Promise<{ accepted: number; duplicates: number; rejected: number }>
  setSaved(eventId: string, saved: boolean): Promise<SavedStateResponseDto>
  getSaved(params?: { tab?: 'saved' | 'matches'; limit?: number; cursor?: string }): Promise<{ items: Array<{ event: Event; savedAt: string | null; match: SavedEventsResponseDto['items'][number]['match'] }>; nextCursor: string | null }>
  recordTicketClick(eventId: string, input: { source: string; room_id?: string | null }): Promise<{ external_url: string }>
}

export interface AnalyticsEvent {
  client_event_id: string
  type: string
  event_version: number
  occurred_at: string
  event_id?: string | null
  room_id?: string | null
  session_id: string | null
  platform: string
  app_version: string
  entry_point: string
  properties: Record<string, string | number | boolean | string[] | null>
}

export function createHttpDiscoveryApi(request: RequestFn): DiscoveryApi {
  return {
    async getHomeFeed(params = {}) {
      const query = new URLSearchParams(Object.entries(params).filter(([, value]) => value !== undefined).map(([key, value]) => [key, String(value)]))
      const response = await request<HomeFeedResponseDto>(`/feed/home${query.size ? `?${query}` : ''}`)
      return { ...response, sections: response.sections.map((section) => ({ ...section, items: section.items.map(mapEvent) })), activeRoom: response.active_room, roomClosedNotice: response.room_closed_notice }
    },
    async searchEvents(params = {}) {
      const query = new URLSearchParams()
      for (const [key, value] of Object.entries(params)) if (value !== undefined) query.set(key, Array.isArray(value) ? value.join(',') : String(value))
      const response = await request<EventSearchResponseDto>(`/events/search${query.size ? `?${query}` : ''}`)
      return { items: response.items.map(mapEvent), appliedFilters: response.applied_filters, totalEstimate: response.total_estimate, nextCursor: response.next_cursor }
    },
    async getMapEvents(params) {
      const query = new URLSearchParams()
      for (const [key, value] of Object.entries(params)) if (value !== undefined) query.set(key, Array.isArray(value) ? value.join(',') : String(value))
      const response = await request<{ items: EventMapItemDto[] }>(`/events/map?${query}`)
      return response.items.map((item) => item.kind === 'event'
        ? { ...item, event: mapEvent(item.event) }
        : { ...item, members: item.members.map((member) => ({ ...member, event: mapEvent(member.event) })) })
    },
    async getEvent(eventId) { return mapDetail(await request<EventDetailDto>(`/events/${eventId}`)) },
    async recordBehavior(events, keepalive = false) { return request('/behavior/events:batch', { method: 'POST', body: JSON.stringify({ events }), keepalive }) },
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
