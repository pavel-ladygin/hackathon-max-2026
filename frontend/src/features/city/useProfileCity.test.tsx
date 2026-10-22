import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../../shared/api/client'
import { maxPlatform } from '../../shared/platform/max/adapter'
import type { Preferences } from '../../shared/api/types'
import { MOSCOW_CITY_ID, SAINT_PETERSBURG_CITY_ID } from '../../shared/lib/cities'
import { useProfileCity } from './useProfileCity'

type Bootstrap = Awaited<ReturnType<typeof apiClient.bootstrap>>

const initialPreferences: Preferences = {
  cityId: MOSCOW_CITY_ID,
  interestSlugs: ['concerts', 'theatre'],
  budgetMaxMinor: 240_000,
  usualDayTypes: ['weekend'],
  usualTimeSlots: ['evening'],
  version: 3,
  updatedAt: '2026-10-01T00:00:00Z',
}

function setup(preferences: Preferences | null = initialPreferences) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const key = ['bootstrap', maxPlatform.getStartParam()]
  const bootstrap = {
    accessToken: 'token',
    user: { id: 'user-1', displayName: 'Ирина', avatarUrl: null, cityId: MOSCOW_CITY_ID, locale: 'ru' },
    onboardingState: 'complete' as const,
    preferences,
    dailyNotificationsEnabled: false,
    inviteContext: null,
    sharedEventId: null,
  }
  queryClient.setQueryData(key, bootstrap)
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  return { queryClient, key, bootstrap, ...renderHook(() => useProfileCity(), { wrapper }) }
}

afterEach(() => vi.restoreAllMocks())

describe('useProfileCity', () => {
  it('persists the new city without changing other preferences and updates bootstrap cache', async () => {
    const updated: Preferences = { ...initialPreferences, cityId: SAINT_PETERSBURG_CITY_ID, version: 4 }
    const replace = vi.spyOn(apiClient, 'replacePreferences').mockResolvedValue(updated)
    const { result, queryClient, key } = setup()
    queryClient.setQueryData(['bootstrap', 'stale-start-param'], {
      ...queryClient.getQueryData<Bootstrap>(key)!,
      preferences: { ...initialPreferences, interestSlugs: ['sports'], budgetMaxMinor: 100 },
    })
    queryClient.setQueryData(['home-feed', { city_id: MOSCOW_CITY_ID }], { feed_id: 'old-city' })

    await act(async () => { await result.current.selectCity(SAINT_PETERSBURG_CITY_ID) })

    expect(replace).toHaveBeenCalledWith({
      city_id: SAINT_PETERSBURG_CITY_ID,
      interest_slugs: initialPreferences.interestSlugs,
      budget_max_minor: initialPreferences.budgetMaxMinor,
      usual_day_types: initialPreferences.usualDayTypes,
      usual_time_slots: initialPreferences.usualTimeSlots,
    })
    await waitFor(() => expect(result.current.cityId).toBe(SAINT_PETERSBURG_CITY_ID))
    const cached = queryClient.getQueryData<Bootstrap>(key)
    expect(cached?.user.cityId).toBe(SAINT_PETERSBURG_CITY_ID)
    expect(cached?.preferences?.interestSlugs).toEqual(initialPreferences.interestSlugs)
  })

  it('leaves the selected profile city unchanged when persistence fails', async () => {
    vi.spyOn(apiClient, 'replacePreferences').mockRejectedValue(new Error('network'))
    const { result } = setup()
    await act(async () => {
      await expect(result.current.selectCity(SAINT_PETERSBURG_CITY_ID)).rejects.toThrow('network')
    })
    expect(result.current.cityId).toBe(MOSCOW_CITY_ID)
    await waitFor(() => expect(result.current.error).toBeTruthy())
  })

  it('does not cancel discovery requests when the profile save fails', async () => {
    vi.spyOn(apiClient, 'replacePreferences').mockRejectedValue(new Error('network'))
    const { result, queryClient } = setup()
    let resolveFeed!: (value: { feed_id: string }) => void
    let requestSignal: AbortSignal | undefined
    void queryClient.fetchQuery({
      queryKey: ['home-feed', { city_id: MOSCOW_CITY_ID }],
      queryFn: ({ signal }) => {
        requestSignal = signal
        return new Promise<{ feed_id: string }>((resolve) => { resolveFeed = resolve })
      },
    })
    await waitFor(() => expect(requestSignal).toBeDefined())

    await act(async () => {
      await expect(result.current.selectCity(SAINT_PETERSBURG_CITY_ID)).rejects.toThrow('network')
    })

    expect(requestSignal?.aborted).toBe(false)
    resolveFeed({ feed_id: 'still-current' })
  })

  it('rejects unavailable cities and refuses to save when profile preferences are missing', async () => {
    const replace = vi.spyOn(apiClient, 'replacePreferences')
    const noPreferences = setup(null)
    await act(async () => {
      await expect(noPreferences.result.current.selectCity(SAINT_PETERSBURG_CITY_ID)).rejects.toThrow('Сначала завершите настройку профиля.')
    })
    expect(noPreferences.result.current.canSelect).toBe(false)

    const ready = setup()
    await act(async () => {
      await expect(ready.result.current.selectCity('city:novosibirsk')).rejects.toThrow('Этот город пока недоступен.')
    })
    expect(replace).not.toHaveBeenCalled()
  })
})
