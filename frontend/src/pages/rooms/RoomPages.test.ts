import { describe, expect, it } from 'vitest'
import { availableDates, availableRelaxations, RELAXATION_CATEGORIES, relaxedIntent } from '../../features/rooms/relaxation'
import { participantInitials, resolveSwipeIntent } from '../../features/rooms/animation'
import type { RoomSnapshot } from '../../shared/api/types'

const now = new Date(2026, 8, 21, 12) // Monday, 21 September 2026

describe('relaxedIntent', () => {
  it('adds only the concrete date selected by the user', () => {
    const result = relaxedIntent({
      dates: ['2026-09-20', '2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'date', now, '2026-09-23')

    expect(result.dates).toEqual(['2026-09-22', '2026-09-23'])
  })

  it('adds only the explicitly selected category and preserves existing categories', () => {
    const current: NonNullable<RoomSnapshot['myIntent']> = {
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts', 'sports'], budget_max_minor: 500_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }
    const choices = availableRelaxations(current, now).find(({ id }) => id === 'category')?.categories

    expect(choices).toEqual(RELAXATION_CATEGORIES.filter(({ slug }) => slug !== 'concerts'))
    expect(relaxedIntent(current, 'category', now, 'food').category_slugs).toEqual(['concerts', 'sports', 'food'])
    expect(() => relaxedIntent(current, 'category', now, 'concerts')).toThrow(/available category/)
  })

  it('requires an explicitly selected higher budget in 500 ₽ steps', () => {
    const current: NonNullable<RoomSnapshot['myIntent']> = {
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 320_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }
    const choices = availableRelaxations(current, now).find(({ id }) => id === 'budget')?.budgets

    expect(choices).toEqual(Array.from({ length: 14 }, (_, index) => 3_500 + index * 500))
    expect(relaxedIntent(current, 'budget', now, 4_000).budget_max_minor).toBe(400_000)
    expect(() => relaxedIntent(current, 'budget', now)).toThrow(/higher budget/)
    expect(() => relaxedIntent(current, 'budget', now, 3_000)).toThrow(/higher budget/)
    expect(() => relaxedIntent(current, 'budget', now, 10_500)).toThrow(/higher budget/)
  })

  it('adds Sunday when only weekends are allowed and Saturday is already selected', () => {
    const result = relaxedIntent({
      dates: ['2026-09-22', '2026-09-26'], day_types: ['weekend'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'date', now, '2026-09-27')

    expect(result.dates).toEqual(['2026-09-22', '2026-09-26', '2026-09-27'])
  })

  it('offers only explicit date selection when every saved date has expired', () => {
    const expiredIntent: NonNullable<RoomSnapshot['myIntent']> = {
      dates: ['2026-09-01'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-01T12:00:00Z',
    }
    const suggestions = availableRelaxations(expiredIntent, now)

    expect(suggestions.map(({ id }) => id)).toEqual(['date'])
    expect(suggestions[0].dates).toContain('2026-09-22')
    expect(relaxedIntent(expiredIntent, 'date', now, '2026-09-22').dates).toEqual(['2026-09-22'])
  })

  it('does not silently add a replacement for expired dates', () => {
    const expiredWeekendIntent: NonNullable<RoomSnapshot['myIntent']> = {
      dates: ['2026-09-20'], day_types: ['weekend'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }
    const suggestions = availableRelaxations(expiredWeekendIntent, now)

    expect(suggestions.map(({ id }) => id)).toEqual(['date'])
    expect(suggestions[0].dates).toEqual(['2026-09-26', '2026-09-27', '2026-10-03', '2026-10-04'])
    expect(() => relaxedIntent(expiredWeekendIntent, 'date', now)).toThrow(/explicit available date/)
  })

  it('always clears saved location and radius when preparing a new round', () => {
    const result = relaxedIntent({
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 15_000,
      exclusion_slugs: [], location: { lat: 55.75, lng: 37.61 }, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'budget', now, 3_500)

    expect(result.radius_m).toBeNull()
    expect(result.location).toBeNull()
  })

  it('offers explicit choices that can expand current categories, dates, time, or budget', () => {
    const suggestions = availableRelaxations({
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, now)

    expect(suggestions.map(({ id }) => id)).toEqual(['budget', 'category', 'date', 'time'])
    expect(suggestions[0].intent).toBeNull()
    expect(suggestions[0].budgets?.[0]).toBe(3_500)
    expect(suggestions[1].intent).toBeNull()
    expect(suggestions[1].categories?.map(({ slug }) => slug)).not.toContain('concerts')
    expect(suggestions[2].intent).toBeNull()
    expect(suggestions[2].dates).toContain('2026-09-23')
    expect(suggestions[3].intent?.time_slots).toEqual(['morning', 'day', 'evening', 'night'])
    expect(suggestions.filter(({ intent }) => intent !== null).every(({ intent }) => intent!.radius_m === null && intent!.location === null)).toBe(true)
  })

  it('does not offer budget or time changes when already unrestricted', () => {
    const suggestions = availableRelaxations({
      dates: Array.from({ length: 14 }, (_, i) => new Date(2026, 8, 22 + i).toLocaleDateString('sv-SE')), day_types: [], time_slots: [], category_slugs: ['concerts'], budget_max_minor: 1_000_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, now)

    expect(suggestions.map(({ id }) => id)).toEqual(['category'])
    expect(suggestions[0].categories).toHaveLength(5)
  })

  it('offers concrete dates inside the next 14 days that fit allowed day types', () => {
    expect(availableDates(['2026-09-22'], ['weekend'], now)).toEqual(['2026-09-26', '2026-09-27', '2026-10-03', '2026-10-04'])
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
