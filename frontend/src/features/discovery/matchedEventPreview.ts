import type { EventCard } from '../../shared/api/types'

export type MatchedEventPreview = Pick<EventCard, 'id' | 'title' | 'starts_at' | 'timezone' | 'date_label' | 'venue_name'>

export function createMatchedEventPreview(event: MatchedEventPreview): MatchedEventPreview {
  const { id, title, starts_at, timezone, date_label, venue_name } = event
  return { id, title, starts_at, timezone, date_label, venue_name }
}

export function readMatchedEventPreview(state: unknown, eventId: string | undefined): MatchedEventPreview | undefined {
  if (!state || typeof state !== 'object' || !('matchedEvent' in state)) return undefined
  const value = state.matchedEvent
  if (!value || typeof value !== 'object') return undefined
  const event = value as Record<string, unknown>
  if (event.id !== eventId || !['id', 'title', 'starts_at', 'timezone', 'date_label', 'venue_name'].every((key) => typeof event[key] === 'string')) return undefined
  if (!Number.isFinite(Date.parse(event.starts_at as string))) return undefined
  return value as MatchedEventPreview
}

export function matchedEventHasStarted(event: MatchedEventPreview, now = Date.now()): boolean {
  return Date.parse(event.starts_at) <= now
}

export function matchedEventDateLabel(event: MatchedEventPreview): string {
  try {
    return new Intl.DateTimeFormat('ru-RU', {
      day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: event.timezone,
    }).format(new Date(event.starts_at))
  } catch {
    return event.date_label
  }
}
