import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { InviteContextDto, RoomSnapshot } from '../../shared/api/types'
import { ApiError } from '../../shared/api/errors'
import { apiClient } from '../../shared/api/client'
import { JoinPage } from './RoomPages'

const mocks = vi.hoisted(() => ({ track: vi.fn(), trackFailure: vi.fn(), openMaxLink: vi.fn() }))

vi.mock('../../shared/analytics/client', () => ({ track: mocks.track, trackFailure: mocks.trackFailure }))
vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: { getStartParam: () => 'invite-secret', getInitData: () => 'signed-init-data', isMax: true, openMaxLink: mocks.openMaxLink } }))
vi.mock('../../shared/api/client', async () => {
  const actual = await vi.importActual<typeof import('../../shared/api/client')>('../../shared/api/client')
  return { ...actual, apiClient: { bootstrap: vi.fn(), joinRoom: vi.fn() } }
})

const now = Date.UTC(2030, 4, 10, 12)
const inviteContext = (overrides: Partial<InviteContextDto> = {}): InviteContextDto => ({
  token: 'invite-secret',
  room_id: 'room-from-server',
  room_name: 'Планы на вечер',
  city_id: 'city-moscow',
  inviter: { id: 'inviter-id', display_name: 'Павел', avatar_url: null, role: 'creator', intent_ready: false },
  expires_at: new Date(now + 60 * 24 * 60 * 60 * 1000).toISOString(),
  already_joined: false,
  status: 'joinable',
  ...overrides,
})

function bootstrapResponse(context: InviteContextDto | null) {
  return { accessToken: 'access-token', user: { id: 'user-id', displayName: 'Анна', avatarUrl: null, cityId: null, locale: 'ru-RU' as const }, onboardingState: 'complete' as const, preferences: null, dailyNotificationsEnabled: false, inviteContext: context, sharedEventId: null }
}

const joinedRoom: RoomSnapshot = { id: 'joined-room', name: '', city_id: '', state: 'collecting_intents', round_no: 1, version: 1, participants: [], myIntent: null, pool: null, match: null, invite: null, allowed_actions: [], created_at: '', expires_at: '' }

function renderJoin(queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })) {
  const result = render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/invite/invite-secret']}><Routes>
    <Route path="/" element={<p>Главная</p>} />
    <Route path="/invite/:inviteToken" element={<JoinPage />} />
    <Route path="/rooms/:roomId/:roomScreen" element={<IntentRoute />} />
  </Routes></MemoryRouter></QueryClientProvider>)
  return { ...result, queryClient }
}

function IntentRoute() {
  const { roomId } = useParams()
  return <p data-testid="intent-route">Комната: {roomId}</p>
}

describe('JoinPage recovery and recipient telemetry', () => {
  beforeEach(() => {
    vi.spyOn(Date, 'now').mockReturnValue(now)
    window.sessionStorage.clear()
    vi.mocked(apiClient.bootstrap).mockReset()
    vi.mocked(apiClient.joinRoom).mockReset()
    mocks.track.mockReset()
    mocks.trackFailure.mockReset()
    mocks.openMaxLink.mockReset()
    vi.mocked(apiClient.bootstrap).mockResolvedValue(bootstrapResponse(inviteContext()))
    vi.mocked(apiClient.joinRoom).mockResolvedValue(joinedRoom)
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('deduplicates recipient-open telemetry across remounts and never includes the token', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const first = renderJoin(queryClient)
    await screen.findByRole('button', { name: 'Присоединиться' })
    await waitFor(() => expect(mocks.track).toHaveBeenCalledWith('invite_link_opened', { roomId: 'room-from-server' }))
    expect(JSON.stringify(mocks.track.mock.calls)).not.toContain('invite-secret')
    expect(JSON.stringify(mocks.track.mock.calls)).not.toContain('signed-init-data')
    expect(mocks.track).toHaveBeenCalledTimes(1)

    first.unmount()
    renderJoin(queryClient)
    await screen.findByRole('button', { name: 'Присоединиться' })
    expect(mocks.track).toHaveBeenCalledTimes(1)
    expect(apiClient.joinRoom).not.toHaveBeenCalled()
  })

  it.each([
    ['full', inviteContext({ status: 'full' })],
    ['expired', inviteContext({ status: 'expired' })],
  ] as const)('blocks join for a %s invite', async (_status, context) => {
    vi.mocked(apiClient.bootstrap).mockResolvedValue(bootstrapResponse(context))
    renderJoin()

    expect(await screen.findByText('Вас приглашает Павел')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Присоединиться' })).toBeDisabled()
    expect(apiClient.joinRoom).not.toHaveBeenCalled()
  })

  it('does not return to the room when join succeeds after leaving for home', async () => {
    let finishJoin!: (value: typeof joinedRoom) => void
    vi.mocked(apiClient.joinRoom).mockReturnValue(new Promise((resolve) => { finishJoin = resolve }))
    const { queryClient } = renderJoin()
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await screen.findByText('Вас приглашает Павел')
    fireEvent.click(screen.getByRole('button', { name: 'Присоединиться' }))
    await waitFor(() => expect(apiClient.joinRoom).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: 'На главную' }))
    expect(screen.getByText('Главная')).toBeVisible()

    await act(async () => { finishJoin(joinedRoom) })
    await waitFor(() => expect(queryClient.getMutationCache().getAll()[0].state.status).toBe('success'))
    expect(screen.getByText('Главная')).toBeVisible()
    expect(apiClient.joinRoom).toHaveBeenCalledTimes(1)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['room', joinedRoom.id] })
  })

  it.each(['creator', 'participant'] as const)('automatically restores an existing %s without joining or displaying a full-room error', async (role) => {
    const response = bootstrapResponse(inviteContext({ status: 'full', already_joined: true }))
    if (role === 'creator') response.user.id = response.inviteContext!.inviter.id
    vi.mocked(apiClient.bootstrap).mockResolvedValue(response)
    renderJoin()
    expect(await screen.findByTestId('intent-route')).toHaveTextContent('room-from-server')
    expect(screen.queryByText('Комната уже заполнена.')).not.toBeInTheDocument()
    expect(apiClient.joinRoom).not.toHaveBeenCalled()
  })

  it.each([
    ['full', inviteContext({ status: 'full' })],
    ['expired', inviteContext({ status: 'expired' })],
    ['unknown', null],
  ] as const)('can leave a %s invitation for home', async (_status, context) => {
    vi.mocked(apiClient.bootstrap).mockResolvedValue(bootstrapResponse(context))
    renderJoin()
    fireEvent.click(await screen.findByRole('button', { name: 'На главную' }))
    expect(await screen.findByText('Главная')).toBeVisible()
    expect(apiClient.joinRoom).not.toHaveBeenCalled()
  })

  it('refreshes bootstrap after a 409 and prevents another join after the invite becomes full', async () => {
    vi.mocked(apiClient.bootstrap)
      .mockResolvedValueOnce(bootstrapResponse(inviteContext()))
      .mockResolvedValueOnce(bootstrapResponse(inviteContext({ status: 'full' })))
    vi.mocked(apiClient.joinRoom).mockRejectedValueOnce(new ApiError(409, { error: { code: 'ROOM_FULL', message: 'Full' } }))
    const { queryClient } = renderJoin()
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await screen.findByText('Вас приглашает Павел')
    fireEvent.click(screen.getByRole('button', { name: 'Присоединиться' }))
    expect(mocks.track).toHaveBeenCalledWith('room_join_started', { roomId: 'room-from-server' })

    await waitFor(() => expect(apiClient.joinRoom).toHaveBeenCalledTimes(1))
    expect(await screen.findAllByText('Комната уже заполнена.')).toHaveLength(2)
    await waitFor(() => expect(apiClient.bootstrap).toHaveBeenCalledTimes(2))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['bootstrap'] })
    expect(screen.getByRole('button', { name: 'Присоединиться' })).toBeDisabled()
    expect(apiClient.joinRoom).toHaveBeenCalledTimes(1)
  })

  it('offers a safe home exit for ACTIVE_ROOM_EXISTS without constructing a room URL', async () => {
    vi.mocked(apiClient.joinRoom).mockRejectedValueOnce(new ApiError(409, { error: { code: 'ACTIVE_ROOM_EXISTS', message: 'Active room exists' } }))
    renderJoin()
    await screen.findByText('Вас приглашает Павел')
    fireEvent.click(screen.getByRole('button', { name: 'Присоединиться' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('У вас уже есть активная комната.')
    expect(screen.getByRole('button', { name: 'На главную' })).toBeEnabled()
    expect(document.body.textContent).not.toMatch(/\/rooms\/[\w-]+\/(?:intent|waiting|vote)/)
    expect(mocks.trackFailure).toHaveBeenCalledWith('room_join_failed', expect.any(ApiError), { roomId: 'room-from-server' })
  })

  it('offers the existing MAX auth recovery action after a 401 mutation failure', async () => {
    vi.mocked(apiClient.joinRoom).mockRejectedValueOnce(new ApiError(401, { error: { code: 'UNAUTHENTICATED', message: 'Unauthorized' } }))
    renderJoin()
    await screen.findByText('Вас приглашает Павел')
    fireEvent.click(screen.getByRole('button', { name: 'Присоединиться' }))

    expect(await screen.findByRole('button', { name: 'Восстановить вход в MAX' })).toBeEnabled()
    expect(screen.getByRole('alert')).toHaveTextContent('Откройте приложение заново')
  })
})
