import { describe, expect, it } from 'vitest'
import { publicPlanPreset } from './public-plan'

describe('publicPlanPreset', () => {
  it('uses the city calendar date and backend evening window without shifting to tomorrow', () => {
    expect(publicPlanPreset('tonight', 'Europe/Moscow', new Date('2026-10-19T13:00:00.000Z'))).toEqual({
      dates: ['2026-10-19'],
      time_slots: ['evening'],
    })
    expect(publicPlanPreset('tonight', 'Europe/Moscow', new Date('2026-10-19T18:59:00.000Z'))).toEqual({
      dates: ['2026-10-19'],
      time_slots: ['evening'],
    })
    expect(publicPlanPreset('tonight', 'Europe/Moscow', new Date('2026-10-19T19:00:00.000Z'))).toBeNull()
  })

  it('uses the city timezone at UTC date boundaries', () => {
    expect(publicPlanPreset('tonight', 'Asia/Tokyo', new Date('2026-10-19T16:30:00.000Z'))).toEqual({
      dates: ['2026-10-20'],
      time_slots: ['evening'],
    })
  })

  it('chooses the nearest upcoming weekend for weekdays, including Friday and year rollover', () => {
    expect(publicPlanPreset('weekend', 'UTC', new Date('2026-10-19T12:00:00.000Z'))).toEqual({
      dates: ['2026-10-24', '2026-10-25'],
    })
    expect(publicPlanPreset('weekend', 'UTC', new Date('2026-10-23T12:00:00.000Z'))).toEqual({
      dates: ['2026-10-24', '2026-10-25'],
    })
    expect(publicPlanPreset('weekend', 'UTC', new Date('2026-12-31T12:00:00.000Z'))).toEqual({
      dates: ['2027-01-02', '2027-01-03'],
    })
  })

  it('keeps only dates on or after today when run on Saturday or Sunday', () => {
    expect(publicPlanPreset('weekend', 'UTC', new Date('2026-10-24T12:00:00.000Z'))).toEqual({
      dates: ['2026-10-24', '2026-10-25'],
    })
    expect(publicPlanPreset('weekend', 'UTC', new Date('2026-10-25T12:00:00.000Z'))).toEqual({
      dates: ['2026-10-25'],
    })
  })

  it('returns the exact budget patch and rejects unsupported timezones for every preset', () => {
    expect(publicPlanPreset('budget', 'Europe/Moscow', new Date('2026-10-19T12:00:00.000Z'))).toEqual({
      budget_max_minor: 100_000,
    })
    expect(publicPlanPreset('budget', 'Invalid/City', new Date('2026-10-19T12:00:00.000Z'))).toBeNull()
    expect(publicPlanPreset('tonight', 'Invalid/City', new Date('2026-10-19T12:00:00.000Z'))).toBeNull()
    expect(publicPlanPreset('weekend', 'UTC', new Date(Number.NaN))).toBeNull()
  })
})
