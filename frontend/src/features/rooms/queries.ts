import { useQuery, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'
import { mapEvent } from '../../shared/api/mapper'
import type { RoomSnapshot } from '../../shared/api/types'

export function useRoom(roomId: string | undefined) {
  const queryClient = useQueryClient()
  return useQuery({
    queryKey: ['room', roomId],
    queryFn: async () => {
      const incoming = await apiClient.getRoom(roomId!)
      const current = queryClient.getQueryData<RoomSnapshot>(['room', roomId])
      const hasCachedHomeFeed = queryClient.getQueriesData({ queryKey: ['home-feed'] }).some(([, data]) => data !== undefined)
      if ((current && incoming.version >= current.version && roomPhaseKey(current) !== roomPhaseKey(incoming)) || (!current && hasCachedHomeFeed && isTerminal(incoming))) {
        void queryClient.invalidateQueries({ queryKey: ['home-feed'] })
      }
      return current && current.version > incoming.version ? current : incoming
    },
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

function roomPhaseKey(room: RoomSnapshot) {
  return `${room.round_no}:${room.state}:${room.pool?.version ?? ''}:${room.pool?.my_pool_finished ?? ''}:${room.next_round?.round_no ?? ''}:${room.next_round?.my_confirmed ?? ''}`
}
function isTerminal(room: RoomSnapshot) { return room.state === 'matched' || room.state === 'closed' || room.state === 'exhausted' }

export function useRoomEvents(roomId: string | undefined, enabled: boolean, roundNo?: number, poolVersion?: number) {
  return useQuery({
    queryKey: ['room-events', roomId, roundNo, poolVersion],
    queryFn: async () => {
      // The endpoint returns the next unvoted event for the current user.  Do not
      // cache a page of candidates here: its offsets become stale after a vote.
      const response = await apiClient.getRoomEvents(roomId!, { limit: 1 })
      if ((roundNo !== undefined && response.round_no !== roundNo) || (poolVersion !== undefined && response.pool_version !== poolVersion)) throw new Error('Stale room pool response')
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
