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
