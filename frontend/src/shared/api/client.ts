import { mapPreferences, mapRoom, mapUser } from './mapper'
import type { BootstrapRequestDto, InviteContextDto, MapBounds, NotificationPreferencesDto, NotificationPreferencesRequestDto, Preferences, PreferencesRequestDto, RoomIntentRequestDto, RoomSnapshotDto, RoomEventsResponseDto, User, VoteResponseDto } from './types'
import { ApiError, normalizeApiError } from './errors'
import { createHttpDiscoveryApi, type AnalyticsEvent, type DiscoverySearchParams } from './discovery'

export interface ApiClientOptions { baseUrl?: string; fetchImpl?: typeof fetch; getToken?: () => string | null }
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
  async getHomeFeed(params: Record<string, string | number | undefined> = {}) { return this.discovery.getHomeFeed(params) }
  async searchEvents(params: Record<string, string | number | boolean | string[] | undefined> = {}) { return this.discovery.searchEvents(params) }
  async getMapEvents(params: MapBounds & { zoom: number } & Omit<DiscoverySearchParams, 'limit' | 'cursor'>) { return this.discovery.getMapEvents(params) }
  async getEvent(eventId: string) { return this.discovery.getEvent(eventId) }
  async recordBehavior(events: AnalyticsEvent[], keepalive = false) { return this.discovery.recordBehavior(events, keepalive) }
  async setSaved(eventId: string, saved: boolean) { return this.discovery.setSaved(eventId, saved) }
  async getSaved(params: { tab?: 'saved' | 'matches'; limit?: number; cursor?: string } = {}) { return this.discovery.getSaved(params) }
  async createRoom(input: { name: string; city_id: string }, idempotencyKey = crypto.randomUUID()): Promise<{ room: ReturnType<typeof mapRoom>; invite: any }> { const x = await this.request<any>('/rooms', { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(input) }); return { room: mapRoom(x.room), invite: x.invite } }
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
