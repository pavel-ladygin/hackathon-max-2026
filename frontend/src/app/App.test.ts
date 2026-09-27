import { describe, expect, it } from 'vitest'
import { eventIdFromStartParam, getOpenInMaxStartParam } from './sharedEventLink'

describe('event sharing deep links', () => {
  it('resolves only UUID event start parameters', () => {
    const eventId = '6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1'
    expect(eventIdFromStartParam(`event_${eventId}`)).toBe(eventId)
    expect(eventIdFromStartParam('AB24')).toBeNull()
    expect(eventIdFromStartParam('event_not-a-uuid')).toBeNull()
    expect(eventIdFromStartParam(null)).toBeNull()
  })

  it('preserves event links when opening the app from a browser', () => {
    const eventId = '6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1'
    expect(getOpenInMaxStartParam(`/events/${eventId}`)).toBe(`event_${eventId}`)
    expect(getOpenInMaxStartParam('/events')).toBeNull()
  })

  it('preserves room invitation start parameters', () => {
    expect(getOpenInMaxStartParam('/join/AB24')).toBe('AB24')
    expect(getOpenInMaxStartParam('/join/token%20with%20space')).toBe('token with space')
  })
})
