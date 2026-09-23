import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'
import type { CategorySlug, EventCard, EventDetail } from '../../shared/api/types'

type SearchParams = {
  q?: string
  city_id?: string
  date_from?: string
  date_to?: string
  day_types?: string[]
  time_slots?: string[]
  category_slugs?: CategorySlug[]
  price_max_minor?: number
  distance_m?: number
  lat?: number
  lng?: number
  free_only?: boolean
  limit?: number
  cursor?: string
}

export const useHomeFeed = () => useQuery({ queryKey: ['home-feed'], queryFn: () => apiClient.getHomeFeed() })
export const useEventDetail = (eventId: string | undefined) => useQuery({ queryKey: ['event', eventId], queryFn: () => apiClient.getEvent(eventId!), enabled: Boolean(eventId) })
export const useEventSearch = (params: SearchParams, enabled = true) => useInfiniteQuery({ queryKey: ['event-search', params], queryFn: ({ pageParam }) => apiClient.searchEvents({ ...params, cursor: pageParam }), initialPageParam: undefined as string | undefined, getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined, placeholderData: keepPreviousData, enabled })
export const useSavedEvents = (tab: 'saved' | 'matches') => useInfiniteQuery({ queryKey: ['saved-events', tab], queryFn: ({ pageParam }) => apiClient.getSaved({ tab, cursor: pageParam }), initialPageParam: undefined as string | undefined, getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined })
export const useSetSavedEvent = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ eventId, saved }: { eventId: string; saved: boolean }) => apiClient.setSaved(eventId, saved),
    onMutate: async ({ eventId, saved }) => {
      await Promise.all([
        queryClient.cancelQueries({ queryKey: ['event', eventId] }),
        queryClient.cancelQueries({ queryKey: ['saved-events'] }),
        queryClient.cancelQueries({ queryKey: ['home-feed'] }),
        queryClient.cancelQueries({ queryKey: ['event-search'] }),
      ])
      const snapshots = queryClient.getQueriesData({ predicate: ({ queryKey }) => ['event', 'saved-events', 'home-feed', 'event-search'].includes(String(queryKey[0])) })
      queryClient.setQueryData<EventDetail>(['event', eventId], (current) => current ? { ...current, saved } : current)
      queryClient.setQueriesData({ queryKey: ['home-feed'] }, (current) => updateSavedState(current, eventId, saved))
      queryClient.setQueriesData({ queryKey: ['event-search'] }, (current) => updateSavedState(current, eventId, saved))
      queryClient.setQueriesData({ queryKey: ['saved-events'] }, (current) => updateSavedState(current, eventId, saved))
      return { snapshots }
    },
    onError: (_error, _variables, context) => {
      context?.snapshots.forEach(([queryKey, data]) => queryClient.setQueryData(queryKey, data))
    },
    onSuccess: (response, { eventId }) => {
      queryClient.setQueryData<EventDetail>(['event', eventId], (current) => current ? { ...current, saved: response.saved } : current)
    },
    onSettled: (_data, _error, { eventId }) => {
      void queryClient.invalidateQueries({ queryKey: ['event', eventId] })
      void queryClient.invalidateQueries({ queryKey: ['saved-events'] })
      void queryClient.invalidateQueries({ queryKey: ['home-feed'] })
      void queryClient.invalidateQueries({ queryKey: ['event-search'] })
    },
  })
}

function updateSavedState(value: unknown, eventId: string, saved: boolean): unknown {
  if (Array.isArray(value)) return value.map((item) => updateSavedState(item, eventId, saved))
  if (!value || typeof value !== 'object') return value

  const record = value as Record<string, unknown>
  if (record.id === eventId && typeof record.saved === 'boolean') {
    return { ...record, saved } satisfies Partial<EventCard>
  }
  if ('event' in record && record.event && typeof record.event === 'object' && (record.event as { id?: unknown }).id === eventId) {
    return { ...record, event: { ...(record.event as Record<string, unknown>), saved } }
  }

  let changed = false
  const next: Record<string, unknown> = {}
  for (const [key, nested] of Object.entries(record)) {
    const updated = updateSavedState(nested, eventId, saved)
    next[key] = updated
    changed ||= updated !== nested
  }
  return changed ? next : value
}
