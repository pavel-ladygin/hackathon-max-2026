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
      return state && ['collecting_intents', 'ranking', 'voting'].includes(state) ? 1_000 : false
    },
    refetchIntervalInBackground: false,
  })
}

export function useRoomEvents(roomId: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['room-events', roomId],
    queryFn: async () => {
      const response = await apiClient.getRoomEvents(roomId!, { limit: 50 })
      return { ...response, items: response.items.map((item) => ({ ...item, event: mapEvent(item.event) })) }
    },
    enabled: Boolean(roomId) && enabled,
  })
}
