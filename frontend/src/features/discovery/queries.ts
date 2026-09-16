import { useQuery } from '@tanstack/react-query'
import { apiClient } from '../../shared/api/client'

export const useHomeFeed = () => useQuery({ queryKey: ['home-feed'], queryFn: () => apiClient.getHomeFeed() })
export const useEventDetail = (eventId: string | undefined) => useQuery({ queryKey: ['event', eventId], queryFn: () => apiClient.getEvent(eventId!), enabled: Boolean(eventId) })
