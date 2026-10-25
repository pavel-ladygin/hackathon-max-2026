import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../../shared/api/client'
import { useBootstrap } from './useBootstrap'
import { createLaunchContextStore } from '../../shared/platform/max/launchContext'

const source = vi.hoisted(() => ({ initData: 'signed-initial', hint: null as string | null, store: null as ReturnType<typeof createLaunchContextStore> | null }))
vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: {
  getInitData: () => source.initData,
  getStartParam: () => source.hint,
  get isMax() { return true },
} }))
vi.mock('../../shared/api/client', () => ({ apiClient: { bootstrap: vi.fn() } }))
vi.mock('../../shared/platform/max/launchContext', async () => {
  const actual = await vi.importActual<typeof import('../../shared/platform/max/launchContext')>('../../shared/platform/max/launchContext')
  return { ...actual, launchContext: { getSnapshot: () => source.store!.getSnapshot(), subscribe: (listener: () => void) => source.store!.subscribe(listener) } }
})

function response(token: string | null = null): Awaited<ReturnType<typeof apiClient.bootstrap>> {
  return {
    accessToken: 'local-access', user: { id: 'user-1', displayName: 'Анна', avatarUrl: null, cityId: null, locale: 'ru' },
    onboardingState: 'complete', preferences: null, dailyNotificationsEnabled: false, sharedEventId: null,
    inviteContext: token ? { token, room_id: `room-${token}`, room_name: 'Планы на вечер', inviter: { id: 'creator', display_name: 'Павел', avatar_url: null, role: 'creator', intent_ready: true }, expires_at: '2099-01-01T00:00:00Z', already_joined: false, status: 'joinable' } : null,
  }
}

function setHash(data: string | null) {
  window.history.replaceState(null, '', data ? `/#${new URLSearchParams({ WebAppData: data })}` : '/')
}

function renderBootstrap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  return { ...renderHook(() => useBootstrap(), { wrapper }), client }
}

describe('BUG-02 reactive MAX bootstrap', () => {
  beforeEach(() => {
    setHash(null)
    source.initData = 'signed-initial'
    source.hint = null
    source.store = createLaunchContextStore()
    vi.mocked(apiClient.bootstrap).mockReset().mockResolvedValue(response())
  })
  afterEach(() => { cleanup(); setHash(null); vi.restoreAllMocks() })

  it('bootstraps once and ignores ordinary focus/pageshow without a new launch', async () => {
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await act(async () => { window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('pageshow')) })
    expect(apiClient.bootstrap).toHaveBeenCalledTimes(1)
    expect(apiClient.bootstrap).toHaveBeenCalledWith({ init_data: 'signed-initial', start_param: null }, expect.anything())
  })

  it('revalidates new bridge credentials and never puts signed data in query keys', async () => {
    const { result, client } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response('next-invite'))
    act(() => { source.initData = 'signed-next'; source.hint = 'next-invite'; window.dispatchEvent(new Event('focus')) })
    await waitFor(() => expect(result.current.data?.inviteContext?.token).toBe('next-invite'))
    expect(apiClient.bootstrap).toHaveBeenLastCalledWith({ init_data: 'signed-next', start_param: 'next-invite' }, expect.anything())
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain('signed-')
  })

  it('uses new WebAppData from the hash when the SDK still holds its constructor snapshot', async () => {
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response('hash-invite'))
    act(() => { setHash('signed-from-new-hash'); window.dispatchEvent(new HashChangeEvent('hashchange')) })
    await waitFor(() => expect(result.current.data?.inviteContext?.token).toBe('hash-invite'))
    expect(apiClient.bootstrap).toHaveBeenLastCalledWith({ init_data: 'signed-from-new-hash', start_param: null }, expect.anything())
    expect(source.initData).toBe('signed-initial')
    await act(async () => { setHash(null); window.dispatchEvent(new HashChangeEvent('hashchange')); window.dispatchEvent(new Event('focus')) })
    expect(apiClient.bootstrap).toHaveBeenCalledTimes(2)
    expect(result.current.data?.inviteContext?.token).toBe('hash-invite')
  })

  it('does not let an unchanged old hash override newer bridge credentials', async () => {
    setHash('signed-old-hash')
    source.store = createLaunchContextStore()
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response('bridge-invite'))
    act(() => { source.initData = 'signed-new-bridge'; source.hint = 'bridge-invite'; window.dispatchEvent(new Event('focus')) })
    await waitFor(() => expect(result.current.data?.inviteContext?.token).toBe('bridge-invite'))
    await act(async () => { window.dispatchEvent(new Event('focus')) })
    expect(apiClient.bootstrap).toHaveBeenCalledTimes(2)
    expect(apiClient.bootstrap).toHaveBeenLastCalledWith({ init_data: 'signed-new-bridge', start_param: 'bridge-invite' }, expect.anything())
  })

  it('ignores hint-only changes until new signed credentials arrive', async () => {
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await act(async () => { source.hint = 'unverified-other-invite'; window.dispatchEvent(new Event('focus')) })
    expect(apiClient.bootstrap).toHaveBeenCalledTimes(1)
    expect(result.current.data?.inviteContext).toBeNull()
  })

  it('detects delayed bridge updates while the app stays visible', async () => {
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    act(() => { window.dispatchEvent(new Event('focus')) })
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response('delayed-invite'))
    source.initData = 'signed-delayed'
    source.hint = 'delayed-invite'
    await waitFor(() => expect(result.current.data?.inviteContext?.token).toBe('delayed-invite'), { timeout: 2_000 })
    expect(apiClient.bootstrap).toHaveBeenCalledTimes(2)
  })

  it('refreshes the same invite when MAX supplies a new launch for that token', async () => {
    source.hint = 'same-invite'
    source.store = createLaunchContextStore()
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response('same-invite'))
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    act(() => { source.initData = 'signed-second-opening'; window.dispatchEvent(new Event('pageshow')) })
    await waitFor(() => expect(apiClient.bootstrap).toHaveBeenCalledTimes(2))
    expect(apiClient.bootstrap).toHaveBeenLastCalledWith({ init_data: 'signed-second-opening', start_param: 'same-invite' }, expect.anything())
  })

  it('keeps the latest invitation when an earlier bootstrap completes late', async () => {
    const { result } = renderBootstrap()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    let finishOld!: (value: Awaited<ReturnType<typeof apiClient.bootstrap>>) => void
    vi.mocked(apiClient.bootstrap).mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve }))
    act(() => { source.initData = 'signed-slow'; source.hint = 'slow-invite'; window.dispatchEvent(new Event('focus')) })
    await waitFor(() => expect(apiClient.bootstrap).toHaveBeenCalledTimes(2))
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response('latest-invite'))
    act(() => { source.initData = 'signed-latest'; source.hint = 'latest-invite'; window.dispatchEvent(new Event('focus')) })
    await waitFor(() => expect(result.current.data?.inviteContext?.token).toBe('latest-invite'))
    await act(async () => finishOld(response('slow-invite')))
    expect(result.current.data?.inviteContext?.token).toBe('latest-invite')
  })
})
