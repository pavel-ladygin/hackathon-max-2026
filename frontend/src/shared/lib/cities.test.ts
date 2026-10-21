import { describe, expect, it } from 'vitest'
import { ACTIVE_CITIES, CITIES, KAZAN_CITY_ID, MOSCOW_CITY_ID, SAINT_PETERSBURG_CITY_ID, cityById, searchCities } from './cities'

describe('city registry', () => {
  it('matches abbreviations and names without spaces or hyphens', () => {
    expect(searchCities('СПб').map(({ id }) => id)).toContain(SAINT_PETERSBURG_CITY_ID)
    expect(searchCities('Питер').map(({ id }) => id)).toContain(SAINT_PETERSBURG_CITY_ID)
    expect(searchCities('Санкт Петербург').map(({ id }) => id)).toContain(SAINT_PETERSBURG_CITY_ID)
    expect(searchCities('МСК').map(({ id }) => id)).toContain(MOSCOW_CITY_ID)
  })

  it('keeps only Moscow and Saint Petersburg active and preserves seeded Kazan metadata', () => {
    expect(ACTIVE_CITIES.map(({ id }) => id)).toEqual([MOSCOW_CITY_ID, SAINT_PETERSBURG_CITY_ID])
    expect(cityById(KAZAN_CITY_ID)).toMatchObject({ name: 'Казань', ready: false })
    expect(CITIES).toHaveLength(6)
  })
})
