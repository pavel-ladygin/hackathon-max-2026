import { useQuery } from '@tanstack/react-query'
import { useSyncExternalStore } from 'react'
import { apiClient } from '../../shared/api/client'
import { launchContext } from '../../shared/platform/max/launchContext'

export function useBootstrap() {
  const launch = useSyncExternalStore(launchContext.subscribe, launchContext.getSnapshot, launchContext.getSnapshot)
  const query = useQuery({
    queryKey: ['bootstrap', launch.revision, launch.startParam],
    queryFn: ({ signal }) => apiClient.bootstrap({
      init_data: launch.initData,
      start_param: launch.startParam,
    }, signal),
    staleTime: Infinity,
  })
  return { ...query, launchRevision: launch.revision }
}
