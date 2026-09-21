import { describe, expect, it } from 'vitest'
import { EVENTS } from '../../test/fixtures'
import { mapDetail, mapEvent, mapUser } from './mapper'
import { IVAN } from '../../test/fixtures'

describe('API boundary mappers', () => {
  it('keeps snake_case DTO fields at the boundary and exposes UI-friendly aliases', () => {
    const event = mapEvent(EVENTS[0])
    expect(event.imageUrl).toBe('/events/concert-singer.png')
    expect(event.distanceM).toBe(EVENTS[0].distance_m)
    expect(event.priceFromMinor).toBe(250_000)

    const detail = mapDetail(EVENTS[0])
    expect(detail.ticketAvailable).toBe(true)
    expect(detail.dataProvenance.is_demo).toBe(true)

    expect(mapUser(IVAN)).toMatchObject({ displayName: 'Иван', cityId: IVAN.city_id })
  })
})
