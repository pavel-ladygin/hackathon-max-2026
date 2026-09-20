import { useQuery } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'
import { maxPlatform } from '../../shared/platform/max/adapter'

export function useBootstrap() {
  return useQuery({
    queryKey: ['bootstrap', maxPlatform.getStartParam()],
    queryFn: () => apiClient.bootstrap({
      init_data: maxPlatform.getInitData(),
      start_param: maxPlatform.getStartParam(),
    }),
    staleTime: Infinity,
  })
}
