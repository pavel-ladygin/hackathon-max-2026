import { describe, expect, it } from 'vitest'
import type { RoomSnapshot } from '../../shared/api/types'
import {
  KAZAN_CITY_ID,
  MOSCOW_CITY_ID,
  cityToday,
  isCanonicalDate,
  publicPlanSchema,
  resolveCreateCity,
  roomCity,
  sharePlanText,
} from './public-plan'

describe('public plan helpers', () => {
  it('keeps explicit room choice ahead of profile and only defaults to Moscow when both are absent', () => {
    expect(resolveCreateCity(KAZAN_CITY_ID, MOSCOW_CITY_ID)).toBe(KAZAN_CITY_ID)
    expect(resolveCreateCity(null, KAZAN_CITY_ID)).toBe(KAZAN_CITY_ID)
    expect(resolveCreateCity(null, null)).toBe(MOSCOW_CITY_ID)
    expect(resolveCreateCity('unknown-city', MOSCOW_CITY_ID)).toBe('unknown-city')
    expect(roomCity('unknown-city')).toBeUndefined()
  })

  it('computes a canonical city date and rejects impossible calendar dates', () => {
    expect(cityToday('Europe/Moscow', new Date('2026-10-19T22:30:00.000Z'))).toBe('2026-10-20')
    expect(isCanonicalDate('2024-02-29')).toBe(true)
    expect(isCanonicalDate('2025-02-29')).toBe(false)
    expect(isCanonicalDate('2026-2-01')).toBe(false)
  })

  it('accepts 1 through 14 unique canonical dates and rejects duplicates or out of range counts', () => {
    const base = { time_slots: [], category_slugs: ['concerts'], budget_max_minor: 0 }
    const dates = Array.from({ length: 14 }, (_, index) => `2030-01-${String(index + 1).padStart(2, '0')}`)
    expect(publicPlanSchema.safeParse({ ...base, dates }).success).toBe(true)
    expect(publicPlanSchema.safeParse({ ...base, dates: [dates[0], dates[0]] }).success).toBe(false)
    expect(publicPlanSchema.safeParse({ ...base, dates: [...dates, '2030-02-01'] }).success).toBe(false)
  })

  it('builds share text from the server public plan and excludes private snapshot data and invite credentials', () => {
    const room = {
      id: 'room-1',
      name: 'Пятничный вечер',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2030-01-12'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_000 },
      myIntent: { free_text: 'PRIVATE_MARKER', location: { lat: 55.7, lng: 37.6 } },
      invite: { url: 'https://example.test/SECRET_TOKEN', max_deep_link: 'SECRET_TOKEN' },
    } as unknown as RoomSnapshot
    const text = sharePlanText(room)
    expect(text).toContain('Москва')
    expect(text).toContain('Концерты')
    expect(text).toContain('2 500 ₽')
    expect(text).not.toContain('PRIVATE_MARKER')
    expect(text).not.toContain('SECRET_TOKEN')
    expect(text).not.toContain('55.7')
  })
})
