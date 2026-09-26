import { describe, expect, it } from 'vitest'
import { availableRelaxations, relaxedIntent } from '../../features/rooms/relaxation'
import { participantInitials, resolveSwipeIntent } from '../../features/rooms/animation'

const now = new Date(2026, 8, 21, 12) // Monday, 21 September 2026

describe('relaxedIntent', () => {
  it('removes past dates and adds the nearest unselected future date', () => {
    const result = relaxedIntent({
      dates: ['2026-09-20', '2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'date', now)

    expect(result.dates).toEqual(['2026-09-22', '2026-09-23'])
  })

  it('adds Sunday when only weekends are allowed and Saturday is already selected', () => {
    const result = relaxedIntent({
      dates: ['2026-09-22', '2026-09-26'], day_types: ['weekend'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'date', now)

    expect(result.dates).toEqual(['2026-09-22', '2026-09-26', '2026-09-27'])
  })

  it('keeps a valid date when every saved date is in the past', () => {
    const result = relaxedIntent({
      dates: ['2026-09-01'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-01T12:00:00Z',
    }, 'budget', now)

    expect(result.dates).toEqual(['2026-09-21'])
    expect(result.budget_max_minor).toBe(500_000)
  })

  it('replaces expired weekend dates with the next compatible weekend', () => {
    const result = relaxedIntent({
      dates: ['2026-09-20'], day_types: ['weekend'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'budget', now)

    expect(result.dates).toEqual(['2026-09-26'])
  })

  it('always clears saved location and radius when preparing a new round', () => {
    const result = relaxedIntent({
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 15_000,
      exclusion_slugs: [], location: { lat: 55.75, lng: 37.61 }, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'budget', now)

    expect(result.radius_m).toBeNull()
    expect(result.location).toBeNull()
  })

  it('offers only changes that alter current dates, time, or budget', () => {
    const suggestions = availableRelaxations({
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, now)

    expect(suggestions.map(({ id }) => id)).toEqual(['budget', 'date', 'time'])
    expect(suggestions[0].intent.budget_max_minor).toBe(350_000)
    expect(suggestions[1].intent.dates).toContain('2026-09-22')
    expect(suggestions[1].intent.dates.length).toBe(2)
    expect(suggestions[2].intent.time_slots).toEqual(['morning', 'day', 'evening', 'night'])
    expect(suggestions.every(({ intent }) => intent.radius_m === null && intent.location === null)).toBe(true)
  })

  it('does not offer budget or time changes when already unrestricted', () => {
    const suggestions = availableRelaxations({
      dates: Array.from({ length: 14 }, (_, i) => new Date(2026, 8, 22 + i).toLocaleDateString('sv-SE')), day_types: [], time_slots: [], category_slugs: ['concerts'], budget_max_minor: 400_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, now)

    expect(suggestions).toEqual([])
  })
})

describe('room motion helpers', () => {
  it('resolves intentional swipes by distance or velocity', () => {
    expect(resolveSwipeIntent(90)).toBe('like')
    expect(resolveSwipeIntent(-90)).toBe('dislike')
    expect(resolveSwipeIntent(12, 650)).toBe('like')
    expect(resolveSwipeIntent(-12, -650)).toBe('dislike')
    expect(resolveSwipeIntent(89, 649)).toBeNull()
  })

  it('creates stable participant initials for animated avatars', () => {
    expect(participantInitials('Анна Петрова')).toBe('АП')
    expect(participantInitials('')).toBe('?')
  })
})
