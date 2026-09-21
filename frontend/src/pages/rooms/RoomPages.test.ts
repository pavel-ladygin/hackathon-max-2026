import { describe, expect, it } from 'vitest'
import { relaxedIntent } from '../../features/rooms/relaxation'

const now = new Date(2026, 8, 21, 12) // Monday, 21 September 2026

describe('relaxedIntent', () => {
  it('removes past dates and adds the nearest future Friday', () => {
    const result = relaxedIntent({
      dates: ['2026-09-20', '2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'date', now)

    expect(result.dates).toEqual(['2026-09-22', '2026-09-25'])
  })

  it('keeps a valid date when every saved date is in the past', () => {
    const result = relaxedIntent({
      dates: ['2026-09-01'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 500_000, radius_m: 5_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-01T12:00:00Z',
    }, 'budget', now)

    expect(result.dates).toEqual(['2026-09-21'])
    expect(result.budget_max_minor).toBe(500_000)
  })

  it('does not invent a radius when distance was not selected', () => {
    const result = relaxedIntent({
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: null,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'radius', now)

    expect(result.radius_m).toBeNull()
  })

  it('does not narrow a radius that is already larger than the preset', () => {
    const result = relaxedIntent({
      dates: ['2026-09-22'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 15_000,
      exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-09-20T12:00:00Z',
    }, 'radius', now)

    expect(result.radius_m).toBe(15_000)
  })
})
