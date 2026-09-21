import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'
import type { CategorySlug } from '../../shared/api/types'

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
export const useEventSearch = (params: SearchParams) => useInfiniteQuery({ queryKey: ['event-search', params], queryFn: ({ pageParam }) => apiClient.searchEvents({ ...params, cursor: pageParam }), initialPageParam: undefined as string | undefined, getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined })
export const useSavedEvents = (tab: 'saved' | 'matches') => useInfiniteQuery({ queryKey: ['saved-events', tab], queryFn: ({ pageParam }) => apiClient.getSaved({ tab, cursor: pageParam }), initialPageParam: undefined as string | undefined, getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined })
export const useSetSavedEvent = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ eventId, saved }: { eventId: string; saved: boolean }) => apiClient.setSaved(eventId, saved),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['saved-events'] })
      void queryClient.invalidateQueries({ queryKey: ['home-feed'] })
      void queryClient.invalidateQueries({ queryKey: ['event-search'] })
    },
  })
}
