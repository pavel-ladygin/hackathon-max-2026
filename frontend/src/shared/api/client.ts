import { mapPreferences, mapRoom, mapUser } from './mapper'
import type { BootstrapRequestDto, CreateRoomRequestDto, InviteContextDto, MapBounds, NotificationPreferencesDto, NotificationPreferencesRequestDto, Preferences, PreferencesRequestDto, RoomIntentRequestDto, RoomSnapshotDto, RoomEventsResponseDto, User, VoteResponseDto } from './types'
import { ApiError, normalizeApiError } from './errors'
import { createHttpDiscoveryApi, type AnalyticsEvent, type DiscoverySearchParams } from './discovery'

export interface ApiClientOptions { baseUrl?: string; fetchImpl?: typeof fetch; getToken?: () => string | null }
export type EventSubmissionInput = { title: string; description: string; city_id: string; venue_name: string; venue_address: string; starts_at_local: string; ends_at_local?: string; price_mode: 'free' | 'paid' | 'unknown'; price_from_minor?: number | null; price_to_minor?: number | null; ticket_url?: string; category_slug: string; image_url?: string; age_rating?: string }
export type EventSubmissionRow = { id: string; author_id: string; author_name?: string; city_id: string; starts_at: string; timezone: string; payload: EventSubmissionInput; status: 'pending' | 'approved' | 'rejected'; created_at: string; decided_at: string | null; decided_by: string | null; rejection_reason: string | null; event_id: string | null }
let inMemoryAccessToken: string | null = null
export class ApiClient {
  private readonly baseUrl: string; private readonly fetchImpl: typeof fetch; private readonly getToken: () => string | null; private readonly discovery: ReturnType<typeof createHttpDiscoveryApi>
  constructor(options: ApiClientOptions = {}) { this.baseUrl = options.baseUrl ?? import.meta.env.VITE_API_BASE_URL ?? '/api/v1'; this.fetchImpl = options.fetchImpl ?? globalThis.fetch.bind(globalThis); this.getToken = options.getToken ?? (() => inMemoryAccessToken); this.discovery = createHttpDiscoveryApi(this.request.bind(this)) }
  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers); headers.set('Accept', 'application/json'); if (init.body) headers.set('Content-Type', 'application/json')
    const token = this.getToken(); if (token) headers.set('Authorization', `Bearer ${token}`)
    let response: Response
    try { response = await this.fetchImpl(`${this.baseUrl}${path}`, { ...init, headers }) } catch (error) { throw normalizeApiError(error) }
    const body = await response.json().catch(() => null)
    if (!response.ok) throw new ApiError(response.status, body ?? {}, response.headers.get('Retry-After')); return body as T
  }
  async health(): Promise<{ status: 'ready'; database: 'ready'; migrations: 'current' }> { return this.request('/health/ready') }
  async bootstrap(input: BootstrapRequestDto): Promise<{ accessToken: string; user: User; onboardingState: 'new' | 'complete'; preferences: Preferences | null; dailyNotificationsEnabled: boolean; inviteContext: InviteContextDto | null; sharedEventId: string | null }> { const x = await this.request<any>('/auth/max/bootstrap', { method: 'POST', body: JSON.stringify(input) }); inMemoryAccessToken = x.access_token; return { accessToken: x.access_token, user: mapUser(x.user), onboardingState: x.onboarding_state, preferences: x.preferences ? mapPreferences(x.preferences) : null, dailyNotificationsEnabled: x.daily_notifications_enabled, inviteContext: x.invite_context, sharedEventId: x.shared_event_id ?? null } }
  async replacePreferences(input: PreferencesRequestDto): Promise<Preferences> { return mapPreferences(await this.request('/me/preferences', { method: 'PUT', body: JSON.stringify(input) })) }
  async updateNotificationPreferences(input: NotificationPreferencesRequestDto): Promise<NotificationPreferencesDto> { return this.request('/me/notification-preferences', { method: 'PATCH', body: JSON.stringify(input) }) }
  async getEventSubmissionOptions(): Promise<{ cities: Array<{ id: string; name: string; timezone: string }>; categories: Array<{ slug: string; label: string }> }> { return this.request('/event-submissions/options') }
  async createEventSubmission(input: EventSubmissionInput, idempotencyKey: string): Promise<{ id: string; status: 'pending'; created_at: string }> { return this.request('/event-submissions', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(input) }) }
  async getMyEventSubmissions(params: { limit?: number; offset?: number } = {}): Promise<{ items: EventSubmissionRow[]; total: number }> { const q = new URLSearchParams(); if (params.limit !== undefined) q.set('limit', String(params.limit)); if (params.offset !== undefined) q.set('offset', String(params.offset)); return this.request(`/me/event-submissions${q.size ? `?${q}` : ''}`) }
  async getHomeFeed(params: Record<string, string | number | undefined> = {}) { return this.discovery.getHomeFeed(params) }
  async searchEvents(params: Record<string, string | number | boolean | string[] | undefined> = {}, signal?: AbortSignal) { return this.discovery.searchEvents(params, signal) }
  async getSearchCount(params: Record<string, string | number | boolean | string[] | undefined> = {}, signal?: AbortSignal) { return this.discovery.getSearchCount(params, signal) }
  async getMapEvents(params: MapBounds & { zoom: number } & Omit<DiscoverySearchParams, 'limit' | 'cursor'>) { return this.discovery.getMapEvents(params) }
  async getEvent(eventId: string) { return this.discovery.getEvent(eventId) }
  async recordBehavior(events: AnalyticsEvent[], keepalive = false) { return this.discovery.recordBehavior(events, keepalive) }
  async setSaved(eventId: string, saved: boolean) { return this.discovery.setSaved(eventId, saved) }
  async getSaved(params: { tab?: 'saved' | 'matches'; limit?: number; cursor?: string } = {}) { return this.discovery.getSaved(params) }
  async createRoom(input: CreateRoomRequestDto, idempotencyKey = crypto.randomUUID()): Promise<{ room: ReturnType<typeof mapRoom>; invite: { token: string; url: string; max_deep_link: string; expires_at: string } }> {
    const x = await this.request<{ room?: RoomSnapshotDto; invite?: { token: string; url: string; max_deep_link: string; expires_at: string } }>('/rooms', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(input) })
    const room = x?.room
    const invite = x?.invite
    if (
      !room || typeof room.id !== 'string' || !room.id.trim() || typeof room.name !== 'string' || !room.name.trim() ||
      typeof room.city_id !== 'string' || !room.city_id.trim() || !Array.isArray(room.participants) ||
      !invite || typeof invite.url !== 'string' || !invite.url.trim() || typeof invite.max_deep_link !== 'string' || !invite.max_deep_link.trim() ||
      typeof invite.expires_at !== 'string' || !invite.expires_at.trim()
    ) throw new Error('Invalid create room response')
    return { room: mapRoom(room), invite }
  }
  async joinRoom(token: string, idempotencyKey = crypto.randomUUID()): Promise<ReturnType<typeof mapRoom>> { return mapRoom(await this.request<RoomSnapshotDto>(`/room-invites/${encodeURIComponent(token)}/join`, { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey } })) }
  async getRoom(roomId: string): Promise<ReturnType<typeof mapRoom>> { return mapRoom(await this.request<RoomSnapshotDto>(`/rooms/${roomId}`)) }
  async closeRoom(roomId: string): Promise<ReturnType<typeof mapRoom>> { return mapRoom(await this.request<RoomSnapshotDto>(`/rooms/${roomId}/close`, { method: 'POST' })) }
  async acknowledgeRoomClosedNotice(roomId: string): Promise<void> { await this.request(`/rooms/${roomId}/close-notice/ack`, { method: 'POST' }) }
  async replaceMyIntent(roomId: string, input: RoomIntentRequestDto): Promise<ReturnType<typeof mapRoom>> { return mapRoom(await this.request<RoomSnapshotDto>(`/rooms/${roomId}/intent/me`, { method: 'PUT', body: JSON.stringify(input) })) }
  async getRoomEvents(roomId: string, params: { limit?: number; cursor?: string } = {}): Promise<RoomEventsResponseDto> { const q = new URLSearchParams(); if (params.limit) q.set('limit', String(params.limit)); if (params.cursor) q.set('cursor', params.cursor); return this.request(`/rooms/${roomId}/events${q.size ? `?${q}` : ''}`) }
  async vote(roomId: string, eventId: string, input: { pool_version: number; vote: 'like' | 'dislike' }): Promise<VoteResponseDto> { return this.request(`/rooms/${roomId}/events/${eventId}/vote`, { method: 'PUT', body: JSON.stringify(input) }) }
  async recordTicketClick(eventId: string, input: { source: string; room_id?: string | null }): Promise<{ external_url: string }> { return this.discovery.recordTicketClick(eventId, input) }
}
export const apiClient = new ApiClient()
