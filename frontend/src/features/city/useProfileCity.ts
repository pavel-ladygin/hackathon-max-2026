import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useBootstrap } from '../auth/useBootstrap'
import { apiClient } from '../../shared/api/client'
import type { PreferencesRequestDto } from '../../shared/api/types'
import { ACTIVE_CITIES, cityById } from '../../shared/lib/cities'

type BootstrapData = Awaited<ReturnType<typeof apiClient.bootstrap>>

export function useProfileCity() {
  const bootstrap = useBootstrap()
  const queryClient = useQueryClient()
  const cityId = bootstrap.data?.preferences?.cityId ?? bootstrap.data?.user.cityId ?? null

  const saveCity = useMutation({
    mutationFn: async (nextCityId: string) => {
      const nextCity = cityById(nextCityId)
      if (!nextCity?.ready || !ACTIVE_CITIES.some((city) => city.id === nextCityId)) throw new Error('Этот город пока недоступен.')

      const activeBootstrap = bootstrap.data
      const preferences = activeBootstrap?.preferences
      if (!preferences) throw new Error('Сначала завершите настройку профиля.')
      if (preferences.cityId === nextCityId) return { nextCityId, preferences }

      const input: PreferencesRequestDto = {
        city_id: nextCityId,
        interest_slugs: preferences.interestSlugs,
        budget_max_minor: preferences.budgetMaxMinor,
        usual_day_types: preferences.usualDayTypes,
        usual_time_slots: preferences.usualTimeSlots,
      }
      return { nextCityId, preferences: await apiClient.replacePreferences(input) }
    },
    onSuccess: async ({ nextCityId, preferences }) => {
      await Promise.all([
        queryClient.cancelQueries({ queryKey: ['bootstrap'] }),
        queryClient.cancelQueries({ queryKey: ['home-feed'] }),
        queryClient.cancelQueries({ queryKey: ['event-search'] }),
        queryClient.cancelQueries({ queryKey: ['event-search-count'] }),
      ])
      queryClient.setQueriesData<BootstrapData>({ queryKey: ['bootstrap'] }, (current) => current ? {
        ...current,
        user: { ...current.user, cityId: nextCityId },
        preferences: { ...preferences, cityId: nextCityId },
      } : current)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['home-feed'] }),
        queryClient.invalidateQueries({ queryKey: ['event-search'] }),
        queryClient.invalidateQueries({ queryKey: ['event-search-count'] }),
      ])
    },
  })

  return {
    cityId,
    city: cityId ? cityById(cityId) ?? null : null,
    selectCity: (nextCityId: string) => saveCity.mutateAsync(nextCityId).then(() => undefined),
    isPending: saveCity.isPending,
    error: saveCity.error,
    canSelect: Boolean(bootstrap.data?.preferences) && !saveCity.isPending,
  }
}
