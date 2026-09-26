import { apiClient } from '../api/client'
import type { AnalyticsEvent } from '../api/discovery'
import { maxPlatform } from '../platform/max/adapter'
import { ApiError } from '../api/errors'

export const analyticsEvents = [
  'app_opened', 'session_started', 'onboarding_started', 'onboarding_completed',
  'feed_opened', 'event_impression', 'event_opened', 'event_saved', 'event_unsaved',
  'search_performed', 'filters_opened', 'filters_applied', 'filters_reset', 'map_opened',
  'map_marker_opened', 'room_creation_started', 'room_opened', 'invite_opened',
  'invite_link_opened', 'room_join_started', 'swipe_session_started',
  'event_swipe_impression', 'match_shown',
  'invite_shared', 'match_opened', 'swipe_pool_exhausted', 'client_error', 'client_performance',
  'room_creation_failed', 'room_join_failed', 'invite_share_failed', 'ticket_redirect_failed',
] as const

export type ClientAnalyticsEvent = typeof analyticsEvents[number]
type Property = string | number | boolean | string[] | null
type AnalyticsInput = { eventId?: string; roomId?: string; properties?: Record<string, Property>; entryPoint?: string }

const allowedEvents = new Set<string>(analyticsEvents)
const sessionKey = 'max.analytics.session.v1'
const sessionTimeoutMs = 30 * 60 * 1000
const appVersion = import.meta.env.VITE_APP_VERSION ?? import.meta.env.MODE
const operations = new Set(['app_boot', 'feed_load', 'event_detail_load'])

function currentSession() {
  if (typeof window === 'undefined') return { id: null, fresh: false }
  try {
    const previous = JSON.parse(window.sessionStorage.getItem(sessionKey) ?? 'null') as { id?: unknown; lastSeen?: unknown } | null
    const fresh = !previous || typeof previous.id !== 'string' || typeof previous.lastSeen !== 'number' || Date.now() - previous.lastSeen > sessionTimeoutMs
    const id = fresh ? crypto.randomUUID() : previous.id as string
    window.sessionStorage.setItem(sessionKey, JSON.stringify({ id, lastSeen: Date.now() }))
    return { id, fresh }
  } catch {
    return { id: null, fresh: false }
  }
}

function platformName() {
  if (!maxPlatform.isMax) return 'browser'
  const platform = maxPlatform.getPlatform()?.toLowerCase()
  return platform && ['ios', 'android', 'web', 'desktop'].includes(platform) ? `max_${platform}` : 'unknown'
}

function safeEntryPoint(value?: string) {
  const allowed = ['feed', 'home', 'search', 'map', 'saved', 'room_invite', 'room', 'deep_link', 'unknown']
  return value && allowed.includes(value) ? value : 'unknown'
}

export function track(eventName: ClientAnalyticsEvent | string, input: AnalyticsInput = {}) {
  if (!allowedEvents.has(eventName)) return
  try {
    const session = currentSession()
    const envelope = (type: string, properties: Record<string, Property> = {}): AnalyticsEvent => ({
      client_event_id: crypto.randomUUID(),
      type,
      event_version: 1,
      occurred_at: new Date().toISOString(),
      event_id: input.eventId ?? null,
      room_id: input.roomId ?? null,
      session_id: session.id,
      platform: platformName(),
      app_version: appVersion,
      entry_point: safeEntryPoint(input.entryPoint),
      properties,
    })
    const events: AnalyticsEvent[] = []
    if (session.fresh) events.push(envelope('session_started'))
    events.push(envelope(eventName, input.properties))
    const request = apiClient.recordBehavior(events)
    void Promise.resolve(request).catch(() => reportAnalyticsFailure(eventName))
  } catch {
    reportAnalyticsFailure(eventName)
  }
}

function reportAnalyticsFailure(eventName: string) {
  try { console.warn('Analytics event could not be recorded', { event: eventName }) } catch { /* best effort */ }
}

export function trackAppOpen(entryPoint: string) {
  track('app_opened', { entryPoint })
}

export function trackPerformance(operation: string, durationMs: number) {
  if (!operations.has(operation) || !Number.isFinite(durationMs)) return
  track('client_performance', { properties: { operation, duration_ms: Math.min(600_000, Math.max(0, Math.round(durationMs))) } })
}

export function trackClientError(operation: string, error: unknown) {
  if (!operations.has(operation)) return
  track('client_error', { properties: { operation, error_code: normalizedErrorCode(error) } })
}

export function trackFailure(eventName: 'room_creation_failed' | 'room_join_failed' | 'invite_share_failed' | 'ticket_redirect_failed', error: unknown, input: Pick<AnalyticsInput, 'roomId' | 'eventId'> = {}) {
  track(eventName, { ...input, properties: { error_code: normalizedErrorCode(error) } })
}

function normalizedErrorCode(error: unknown) {
  let errorCode = 'unknown'
  if (error instanceof ApiError) {
    if (error.status === 0) errorCode = 'network_error'
    else if (error.status === 401 || error.status === 403) errorCode = 'unauthorized'
    else if (error.status === 404) errorCode = 'not_found'
    else if (error.status === 429) errorCode = 'rate_limited'
    else if (error.status >= 500) errorCode = 'server_error'
    else if (error.status >= 400) errorCode = 'request_error'
  } else if (error instanceof TypeError) {
    errorCode = 'network_error'
  }
  return errorCode
}
