import { describe, expect, it } from 'vitest'
import { matchedEventDateLabel, matchedEventHasStarted, readMatchedEventPreview } from './matchedEventPreview'

const event = { id: 'event-1', title: 'Концерт', starts_at: '2026-10-25T19:00:00+03:00', timezone: 'Europe/Moscow', date_label: '25 октября', venue_name: 'Оранжерея' }

describe('matched event preview', () => {
  it('uses the absolute start time and includes the exact start boundary', () => {
    expect(matchedEventHasStarted(event, Date.parse('2026-10-25T15:59:59Z'))).toBe(false)
    expect(matchedEventHasStarted(event, Date.parse('2026-10-25T16:00:00Z'))).toBe(true)
    expect(matchedEventHasStarted(event, Date.parse('2026-10-26T16:00:00Z'))).toBe(true)
  })

  it('accepts metadata only for the event being opened', () => {
    expect(readMatchedEventPreview({ matchedEvent: event }, event.id)).toEqual(event)
    expect(readMatchedEventPreview({ matchedEvent: event }, 'another-event')).toBeUndefined()
    expect(readMatchedEventPreview(null, event.id)).toBeUndefined()
    expect(readMatchedEventPreview({ matchedEvent: { ...event, starts_at: 'invalid' } }, event.id)).toBeUndefined()
    expect(readMatchedEventPreview({ matchedEvent: { ...event, title: null } }, event.id)).toBeUndefined()
  })

  it('displays the year and time in the event timezone, with a safe label fallback', () => {
    expect(matchedEventDateLabel(event)).toContain('25 октября 2026')
    expect(matchedEventDateLabel(event)).toContain('19:00')
    expect(matchedEventDateLabel({ ...event, timezone: 'invalid/timezone' })).toBe(event.date_label)
  })
})
