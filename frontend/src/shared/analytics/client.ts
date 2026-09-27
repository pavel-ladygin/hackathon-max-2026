import { apiClient } from '../api/client'
import type { AnalyticsEvent } from '../api/discovery'
import { maxPlatform } from '../platform/max/adapter'
import { ApiError } from '../api/errors'
import { CATEGORY_REGISTRY } from '../api/categories'

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
const queueKey = 'max.analytics.queue.v1'
const sessionTimeoutMs = 30 * 60 * 1000
const queueMax = 100
const queueBatch = 20
const queueTTL = 24 * 60 * 60 * 1000
const appVersion = import.meta.env.VITE_APP_VERSION ?? import.meta.env.MODE
const operations = new Set(['app_boot', 'feed_load', 'event_detail_load'])
const numericProperties = new Set(['result_count', 'zoom_level', 'pool_size', 'position', 'recommendation_rank', 'previous_active_filter_count', 'query_length', 'budget_max', 'duration_ms'])
const booleanProperties = new Set(['has_image', 'has_price'])
const boundedLabels = new Set(['feed', 'home', 'search', 'map', 'saved', 'room', 'direct', 'unknown', 'network_error', 'unauthorized', 'not_found', 'rate_limited', 'server_error', 'request_error'])
let queue: AnalyticsEvent[] = []
let activeUserId: string | null = null
let listenersAdded = false
let sending = false
let retryTimer: ReturnType<typeof setTimeout> | null = null
let retryDelayMs = 5_000

function safeProperties(input: Record<string, Property> = {}): Record<string, Property> {
  const result: Record<string, Property> = {}
  for (const [key, value] of Object.entries(input)) {
    if (numericProperties.has(key) && typeof value === 'number' && Number.isFinite(value)) result[key] = value
    else if (booleanProperties.has(key) && typeof value === 'boolean') result[key] = value
    else if ((key === 'date_from' || key === 'date_to') && typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value)) result[key] = value
    else if (key === 'category' && typeof value === 'string' && value in CATEGORY_REGISTRY) result[key] = value
    else if (key === 'categories' && Array.isArray(value)) result[key] = value.filter((item) => item in CATEGORY_REGISTRY)
    else if (key === 'operation' && typeof value === 'string' && operations.has(value)) result[key] = value
    else if ((key === 'source_screen' || key === 'list_type' || key === 'error_code') && typeof value === 'string' && boundedLabels.has(value)) result[key] = value
  }
  return result
}

function persistQueue() {
  if (!activeUserId || typeof window === 'undefined') return
  try {
    window.localStorage.setItem(queueKey, JSON.stringify({ userId: activeUserId, events: queue }))
  } catch { /* Continue in memory if WebView storage is unavailable. */ }
}

function loadQueue(userId: string) {
  if (typeof window === 'undefined') return []
  try {
    const stored = JSON.parse(window.localStorage.getItem(queueKey) ?? 'null') as { userId?: unknown; events?: unknown } | null
    if (stored?.userId !== userId || !Array.isArray(stored.events)) return []
    const oldest = Date.now() - queueTTL
    return stored.events.filter((event): event is AnalyticsEvent => {
      if (!event || typeof event !== 'object') return false
      const candidate = event as Partial<AnalyticsEvent>
      return typeof candidate.client_event_id === 'string' && typeof candidate.type === 'string' &&
        typeof candidate.occurred_at === 'string' && Date.parse(candidate.occurred_at) >= oldest &&
        typeof candidate.properties === 'object' && candidate.properties !== null
    }).slice(-queueMax).map((event) => ({ ...event, properties: safeProperties(event.properties as Record<string, Property>) }))
  } catch { return [] }
}

function scheduleRetry() {
  if (retryTimer || typeof window === 'undefined') return
  retryTimer = setTimeout(() => {
    retryTimer = null
    void flushQueue()
  }, retryDelayMs)
  retryDelayMs = Math.min(retryDelayMs * 2, 60_000)
}

async function flushQueue(keepalive = false) {
  if (!activeUserId || sending || queue.length === 0) return
  if (typeof navigator !== 'undefined' && navigator.onLine === false) return
  sending = true
  const batch = queue.slice(0, queueBatch)
  try {
    const result = await apiClient.recordBehavior(batch, keepalive)
    if (result.accepted + result.duplicates + result.rejected !== batch.length) throw new Error('Incomplete analytics batch')
    const delivered = new Set(batch.map((event) => event.client_event_id))
    queue = queue.filter((event) => !delivered.has(event.client_event_id))
    persistQueue()
    retryDelayMs = 5_000
    if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
  } catch {
    scheduleRetry()
  } finally {
    sending = false
  }
  if (queue.length && !retryTimer) void flushQueue()
}

export function activateAnalytics(userId: string) {
  if (!userId || typeof window === 'undefined') return
  if (activeUserId !== userId) {
    queue = [...loadQueue(userId), ...(activeUserId === null ? queue : [])].slice(-queueMax)
    activeUserId = userId
    persistQueue()
  }
  if (!listenersAdded) {
    window.addEventListener('online', () => void flushQueue())
    window.addEventListener('focus', () => void flushQueue())
    document.addEventListener('visibilitychange', () => { if (!document.hidden) void flushQueue() })
    window.addEventListener('pagehide', () => void flushQueue(true))
    listenersAdded = true
  }
  void flushQueue()
}

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
      properties: safeProperties(properties),
    })
    const events: AnalyticsEvent[] = []
    if (session.fresh) events.push(envelope('session_started'))
    events.push(envelope(eventName, input.properties))
    queue = [...queue, ...events].slice(-queueMax)
    persistQueue()
    void flushQueue()
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
