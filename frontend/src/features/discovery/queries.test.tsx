import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../../shared/api/client'
import { mapDetail, mapEvent } from '../../shared/api/mapper'
import { EVENTS } from '../../test/fixtures'
import { useSetSavedEvent } from './queries'

const eventId = EVENTS[0].id

function setup() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  return { queryClient, ...renderHook(() => useSetSavedEvent(), { wrapper }) }
}

afterEach(() => vi.restoreAllMocks())

describe('useSetSavedEvent', () => {
  it('updates the detail and list caches as soon as the mutation starts', async () => {
    let resolveRequest!: (value: { event_id: string; saved: boolean; saved_at: string | null }) => void
    vi.spyOn(apiClient, 'setSaved')
      .mockReturnValueOnce(new Promise((resolve) => { resolveRequest = resolve }))
      .mockResolvedValueOnce({ event_id: eventId, saved: false, saved_at: null })
    const { queryClient, result } = setup()
    queryClient.setQueryData(['event', eventId], mapDetail(EVENTS[0]))
    queryClient.setQueryData(['event-search', { limit: 24 }], { pages: [{ items: [mapEvent(EVENTS[0])], totalEstimate: 1, nextCursor: null }], pageParams: [undefined] })

    act(() => result.current.mutate({ eventId, saved: true }))

    await waitFor(() => expect(queryClient.getQueryData<ReturnType<typeof mapDetail>>(['event', eventId])?.saved).toBe(true))
    const search = queryClient.getQueryData<{ pages: Array<{ items: Array<ReturnType<typeof mapEvent>> }> }>(['event-search', { limit: 24 }])
    expect(search?.pages[0].items[0].saved).toBe(true)

    resolveRequest({ event_id: eventId, saved: true, saved_at: new Date().toISOString() })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    await act(async () => { await result.current.mutateAsync({ eventId, saved: false }) })
    expect(queryClient.getQueryData<ReturnType<typeof mapDetail>>(['event', eventId])?.saved).toBe(false)
  })

  it('rolls optimistic cache updates back when saving fails', async () => {
    vi.spyOn(apiClient, 'setSaved').mockRejectedValue(new Error('network'))
    const { queryClient, result } = setup()
    queryClient.setQueryData(['event', eventId], mapDetail(EVENTS[0]))

    act(() => result.current.mutate({ eventId, saved: true }))

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(queryClient.getQueryData<ReturnType<typeof mapDetail>>(['event', eventId])?.saved).toBe(false)
  })
})

describe('city-specific discovery caches', () => {
  it('starts a fresh list and cursor on city change and ignores a delayed response from the old city', async () => {
    const { useEventSearch } = await import('./queries')
    let resolveOld!: (value: Awaited<ReturnType<typeof apiClient.searchEvents>>) => void
    let resolveNew!: (value: Awaited<ReturnType<typeof apiClient.searchEvents>>) => void
    const search = vi.spyOn(apiClient, 'searchEvents')
      .mockResolvedValueOnce({ items: [mapEvent(EVENTS[0])], appliedFilters: {}, totalEstimate: 2, nextCursor: 'moscow-page-2' })
      .mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve }))
      .mockReturnValueOnce(new Promise((resolve) => { resolveNew = resolve }))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    const { result, rerender } = renderHook(({ cityId }) => useEventSearch({ city_id: cityId }), { initialProps: { cityId: 'moscow' }, wrapper })
    await waitFor(() => expect(result.current.data?.pages[0].items).toHaveLength(1))
    act(() => { void result.current.fetchNextPage() })
    await waitFor(() => expect(search).toHaveBeenCalledTimes(2))
    rerender({ cityId: 'spb' })
    await waitFor(() => expect(search).toHaveBeenCalledTimes(3))
    expect(result.current.data).toBeUndefined()
    expect(search.mock.calls[2][0]).toEqual({ city_id: 'spb', cursor: undefined })
    await act(async () => { resolveOld({ items: [mapEvent(EVENTS[0])], appliedFilters: {}, totalEstimate: 2, nextCursor: null }) })
    expect(result.current.data).toBeUndefined()
    await act(async () => { resolveNew({ items: [], appliedFilters: {}, totalEstimate: 0, nextCursor: null }) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.pages).toHaveLength(1)
    expect(result.current.data?.pages[0].items).toEqual([])
  })

  it('keys home feeds by city and sends the city explicitly', async () => {
    const { useHomeFeed } = await import('./queries')
    const home = vi.spyOn(apiClient, 'getHomeFeed').mockImplementation(async (params) => ({ feed_id: String(params?.city_id), generated_at: '', sections: [], activeRoom: null, roomClosedNotice: null }))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    const { result, rerender } = renderHook(({ cityId }) => useHomeFeed(cityId), { initialProps: { cityId: 'moscow' }, wrapper })
    await waitFor(() => expect(result.current.data?.feed_id).toBe('moscow'))
    rerender({ cityId: 'spb' })
    await waitFor(() => expect(result.current.data?.feed_id).toBe('spb'))
    expect(home).toHaveBeenCalledWith({ city_id: 'spb' })
    expect(queryClient.getQueryData(['home-feed', 'moscow'])).toBeDefined()
  })
})
