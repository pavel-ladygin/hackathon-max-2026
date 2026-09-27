import { useQuery } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'
import { mapEvent } from '../../shared/api/mapper'

export function useRoom(roomId: string | undefined) {
  return useQuery({
    queryKey: ['room', roomId],
    queryFn: () => apiClient.getRoom(roomId!),
    enabled: Boolean(roomId),
    refetchInterval: (query) => {
      const state = query.state.data?.state
      if (state === 'collecting_intents') return jitter(3_000)
      if (state === 'ranking') return jitter(1_000)
      if (state === 'voting') return jitter(2_000)
      if (state === 'exhausted') return jitter(3_000)
      return false
    },
    refetchIntervalInBackground: false,
  })
}

export function useRoomEvents(roomId: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['room-events', roomId],
    queryFn: async () => {
      // The endpoint returns the next unvoted event for the current user.  Do not
      // cache a page of candidates here: its offsets become stale after a vote.
      const response = await apiClient.getRoomEvents(roomId!, { limit: 1 })
      return { ...response, items: response.items.map((item) => ({ ...item, event: mapEvent(item.event) })) }
    },
    enabled: Boolean(roomId) && enabled,
    refetchInterval: enabled ? jitter(2_000) : false,
    refetchIntervalInBackground: false,
  })
}

function jitter(base: number) {
  return Math.round(base * (0.8 + Math.random() * 0.4))
}
