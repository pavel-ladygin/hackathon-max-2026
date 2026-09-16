import { useQuery } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'
import { maxPlatform } from '../../shared/platform/max/adapter'

export function useBootstrap() {
  const mockMode = (import.meta.env.VITE_API_MODE ?? 'mock') === 'mock'
  return useQuery({
    queryKey: ['bootstrap', maxPlatform.getStartParam()],
    queryFn: () => apiClient.bootstrap({
      init_data: maxPlatform.getInitData() || (mockMode ? 'mock-init-data' : ''),
      start_param: maxPlatform.getStartParam(),
    }),
    staleTime: Infinity,
  })
}
