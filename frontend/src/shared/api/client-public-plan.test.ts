import { describe, expect, it, vi } from 'vitest'
import { ApiClient } from './client'
import type { PublicPlan } from './types'

const snapshot = (publicPlan?: PublicPlan | null) => ({
  id: 'room-server', name: 'Выходные', city_id: 'city-server', ...(publicPlan === undefined ? {} : { public_plan: publicPlan }),
  state: 'collecting_intents' as const, round_no: 1, version: 1, participants: [], my_intent: null, pool: null, match: null, invite: null,
  allowed_actions: ['invite'], created_at: '2026-10-20T10:00:00Z', expires_at: '2026-10-22T10:00:00Z',
})

const invite = { token: 'invite-token', url: 'https://example.test/invite', max_deep_link: 'https://max.ru/join', expires_at: '2026-10-22T10:00:00Z' }

describe('ApiClient.createRoom public plan', () => {
  it('sends the chosen city and plan, then uses the server snapshot and invite', async () => {
    const serverPlan: PublicPlan = { dates: ['2026-10-25'], time_slots: [], category_slugs: ['cinema'], budget_max_minor: 300_000 }
    const fetchImpl = vi.fn<typeof fetch>(async () => Response.json({ room: snapshot(serverPlan), invite }))
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl })
    const inputPlan = { dates: ['2026-10-24'], time_slots: ['evening' as const], category_slugs: ['concerts' as const], budget_max_minor: 250_000 }

    const created = await client.createRoom({ name: 'Выходные', city_id: 'city-selected', public_plan: inputPlan }, '11111111-1111-1111-1111-111111111111')

    expect(String(fetchImpl.mock.calls[0][0])).toBe('/api/v1/rooms')
    expect(fetchImpl.mock.calls[0][1]?.method).toBe('POST')
    expect(new Headers(fetchImpl.mock.calls[0][1]?.headers).get('Idempotency-Key')).toBe('11111111-1111-1111-1111-111111111111')
    expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body))).toEqual({ name: 'Выходные', city_id: 'city-selected', public_plan: inputPlan })
    expect(created.room.id).toBe('room-server')
    expect(created.room.city_id).toBe('city-server')
    expect(created.room.public_plan).toEqual(serverPlan)
    expect(created.invite).toEqual(invite)
  })

  it('preserves legacy rooms and explicit null plans without inventing a plan', async () => {
    const fetchImpl = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json({ room: snapshot(), invite }))
      .mockResolvedValueOnce(Response.json({ room: snapshot(null), invite }))
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl })

    const legacy = await client.createRoom({ name: 'Старая комната', city_id: 'city-1' }, '22222222-2222-2222-2222-222222222222')
    const noPlan = await client.createRoom({ name: 'Без плана', city_id: 'city-1', public_plan: null }, '33333333-3333-3333-3333-333333333333')

    expect(legacy.room.public_plan).toBeUndefined()
    expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body))).toEqual({ name: 'Старая комната', city_id: 'city-1' })
    expect(noPlan.room.public_plan).toBeNull()
    expect(JSON.parse(String(fetchImpl.mock.calls[1][1]?.body))).toEqual({ name: 'Без плана', city_id: 'city-1', public_plan: null })
  })

  it('rejects missing or incomplete server rooms instead of synthesizing one', async () => {
    const fetchImpl = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json({}))
      .mockResolvedValueOnce(Response.json({ room: { ...snapshot(), id: '  ' }, invite }))
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl })

    await expect(client.createRoom({ name: 'Выходные', city_id: 'city-1' }, '44444444-4444-4444-4444-444444444444')).rejects.toThrow('Invalid create room response')
    await expect(client.createRoom({ name: 'Выходные', city_id: 'city-1' }, '55555555-5555-5555-5555-555555555555')).rejects.toThrow('Invalid create room response')
  })
})
