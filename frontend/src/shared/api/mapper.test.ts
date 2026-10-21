import { describe, expect, it } from 'vitest'
import { EVENTS } from '../../test/fixtures'
import { mapDetail, mapEvent, mapRoom, mapUser } from './mapper'
import type { RoomSnapshotDto } from './types'
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

  it('preserves the other occurrences count, including zero and absence', () => {
    expect(mapEvent({ ...EVENTS[0], other_occurrences_count: 3 }).other_occurrences_count).toBe(3)
    expect(mapEvent({ ...EVENTS[0], other_occurrences_count: 0 }).other_occurrences_count).toBe(0)
    expect(mapEvent(EVENTS[0]).other_occurrences_count).toBeUndefined()
  })

  it('decodes named, numeric, and ampersand entities in event titles', () => {
    expect(mapEvent({ ...EVENTS[0], title: '«&quot;Алое золото&#34; &amp; ещё»' }).title).toBe('«"Алое золото" & ещё»')
    expect(mapEvent(EVENTS[0]).title).toBe(EVENTS[0].title)
  })

  it('keeps a public plan separate from the caller personal intent and clones its arrays', () => {
    const plan = { dates: ['2026-10-24'], time_slots: ['evening' as const], category_slugs: ['concerts' as const], budget_max_minor: 250_000 }
    const dto: RoomSnapshotDto = {
      id: 'room-1', name: 'Выходные', city_id: 'city-1', public_plan: plan, state: 'collecting_intents', round_no: 1, version: 1,
      participants: [], my_intent: {
        dates: ['2026-10-25'], day_types: ['weekend'], time_slots: ['day'], category_slugs: ['cinema'], budget_max_minor: 100_000,
        radius_m: null, exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2026-10-20T00:00:00Z',
      },
      pool: null, match: null, invite: null, allowed_actions: [], created_at: '2026-10-20T00:00:00Z', expires_at: '2026-10-22T00:00:00Z',
    }

    const room = mapRoom(dto)

    expect(room.public_plan).toEqual(plan)
    expect(room.myIntent?.dates).toEqual(['2026-10-25'])
    expect(room.public_plan?.dates).not.toBe(dto.public_plan?.dates)
    expect(room.public_plan?.time_slots).not.toBe(dto.public_plan?.time_slots)
    expect(room.public_plan?.category_slugs).not.toBe(dto.public_plan?.category_slugs)
    expect(mapRoom({ ...dto, public_plan: null }).public_plan).toBeNull()
    expect(mapRoom({ ...dto, public_plan: undefined }).public_plan).toBeUndefined()
  })
})
