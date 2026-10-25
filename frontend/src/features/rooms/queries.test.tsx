import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { apiClient } from '../../shared/api/client'
import { useRoom, useRoomEvents } from './queries'

vi.mock('../../shared/api/client', () => ({ apiClient: { getRoom: vi.fn(), getRoomEvents: vi.fn() } }))

const roomDto = (id: string, version: number) => ({
  id, name: id, city_id: 'city-1', state: 'voting' as const, round_no: 1, version, participants: [], my_intent: null,
  next_round: null, pool: { version: 1, round_no: 1, state: 'ready' as const, total: 1, voted_by_me: 0, my_pool_finished: false, room_exhausted: false, retry_after_seconds: null, exhaustion_reasons: [] },
  match: null, invite: null, allowed_actions: ['vote'], created_at: '', expires_at: '',
})

const eventResponse = (roundNo: number, poolVersion: number) => ({
  room_id: 'room-1', round_no: roundNo, pool_version: poolVersion, next_cursor: null, total: 1,
  items: [{ cursor: 'c1', position: 0, event: { id: 'event-1', title: 'Event', subtitle: null, category_slug: 'concerts', starts_at: '2099-11-20T18:00:00Z', timezone: 'Europe/Moscow', date_label: '20 ноября', venue_name: 'Venue', other_occurrences_count: 0, currency: 'RUB', price_label: 'Бесплатно', saved: false, reasons: [], image_url: null, distance_m: null, distance_label: null, price_from_minor: null } }],
})

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return { client, Wrapper: ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> }
}

describe('room query freshness fences', () => {
  it('keeps cached room v12 when a late v11 response arrives', async () => {
    vi.mocked(apiClient.getRoom).mockReset().mockResolvedValue(roomDto('room-1', 11) as never)
    const { client, Wrapper } = wrapper()
    client.setQueryData(['room', 'room-1'], { ...roomDto('room-1', 12), myIntent: null })
    const query = renderHook(() => useRoom('room-1'), { wrapper: Wrapper })
    await waitFor(() => expect(query.result.current.data?.version).toBe(12))
  })

  it('does not reuse another room id cache when resolving a lower version', async () => {
    vi.mocked(apiClient.getRoom).mockReset().mockResolvedValue(roomDto('room-2', 2) as never)
    const { client, Wrapper } = wrapper()
    client.setQueryData(['room', 'room-1'], { ...roomDto('room-1', 12), myIntent: null })
    const query = renderHook(() => useRoom('room-2'), { wrapper: Wrapper })
    await waitFor(() => expect(query.result.current.data?.id).toBe('room-2'))
    expect(query.result.current.data?.version).toBe(2)
  })

  it('rejects stale event tags and refetches when round and pool query keys change', async () => {
    vi.mocked(apiClient.getRoomEvents).mockReset().mockResolvedValueOnce(eventResponse(1, 1) as never).mockResolvedValueOnce(eventResponse(1, 1) as never).mockResolvedValueOnce(eventResponse(2, 2) as never)
    const { Wrapper } = wrapper()
    const stale = renderHook(() => useRoomEvents('room-1', true, 2, 2), { wrapper: Wrapper })
    await waitFor(() => expect(stale.result.current.isError).toBe(true))
    expect(stale.result.current.data).toBeUndefined()

    const current = renderHook(({ round, pool }: { round: number; pool: number }) => useRoomEvents('room-1', true, round, pool), { initialProps: { round: 1, pool: 1 }, wrapper: Wrapper })
    await waitFor(() => expect(current.result.current.data?.round_no).toBe(1))
    current.rerender({ round: 2, pool: 2 })
    await waitFor(() => expect(current.result.current.data?.round_no).toBe(2))
    expect(apiClient.getRoomEvents).toHaveBeenCalledTimes(3)
    expect(current.result.current.data?.items[0].event.title).toBe('Event')
  })
})
