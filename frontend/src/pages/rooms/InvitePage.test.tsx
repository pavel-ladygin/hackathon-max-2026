import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RoomSnapshot } from '../../shared/api/types'
import { InvitePage } from './RoomPages'

const platform = vi.hoisted(() => ({ shareInvite: vi.fn(), copyText: vi.fn() }))
const state = vi.hoisted(() => ({ room: null as RoomSnapshot | null, track: vi.fn(), trackFailure: vi.fn(), refetch: vi.fn() }))

vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: platform }))
vi.mock('../../shared/analytics/client', () => ({ track: state.track, trackFailure: state.trackFailure }))
vi.mock('../../features/rooms/queries', () => ({
  useRoom: () => ({ isPending: false, isError: false, data: state.room, refetch: state.refetch }),
  useRoomEvents: vi.fn(),
}))

const future = '2099-10-21T12:00:00.000Z'
const roomFixture = (): RoomSnapshot => ({
  id: 'room-1',
  name: 'Планы на вечер',
  city_id: 'moscow',
  state: 'collecting_intents',
  round_no: 1,
  version: 1,
  participants: [{ id: 'creator', displayName: 'Павел', avatarUrl: null, role: 'creator', intentReady: false }],
  myIntent: null,
  pool: null,
  match: null,
  invite: { url: 'https://example.test/join/abc', max_deep_link: 'https://max.ru/example?startapp=abc', expires_at: future },
  allowed_actions: ['invite', 'edit_intent'],
  created_at: '2099-10-20T12:00:00.000Z',
  expires_at: future,
})

function renderInvite() {
  return render(<MemoryRouter initialEntries={['/rooms/room-1/invite']}><Routes><Route path="/rooms/:roomId/invite" element={<InvitePage />} /></Routes></MemoryRouter>)
}

describe('InvitePage feedback and action gating', () => {
  beforeEach(() => {
    vi.spyOn(Date, 'now').mockReturnValue(Date.UTC(2099, 9, 20, 12))
    state.room = roomFixture()
    state.track.mockReset()
    state.trackFailure.mockReset()
    state.refetch.mockReset().mockResolvedValue(undefined)
    platform.shareInvite.mockReset()
    platform.copyText.mockReset()
    platform.shareInvite.mockResolvedValue({ status: 'unavailable' })
    platform.copyText.mockResolvedValue(false)
  })

  afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

  it.each([
    [{ status: 'opened' }, 'Открыли отправку в MAX'],
    [{ status: 'cancelled' }, 'Отправка отменена'],
    [{ status: 'failed' }, 'Не удалось открыть отправку'],
    [{ status: 'unavailable' }, 'Не удалось открыть отправку'],
  ] as const)('shows accurate feedback when share returns %s', async (result, message) => {
    platform.shareInvite.mockResolvedValue(result)
    renderInvite()

    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))

    expect(await screen.findByText(new RegExp(message))).toBeVisible()
    expect(platform.shareInvite).toHaveBeenCalledWith({ text: expect.any(String), link: 'https://max.ru/example?startapp=abc' })
    expect(state.track).not.toHaveBeenCalledWith('invite_opened', expect.anything())
    expect(state.track).not.toHaveBeenCalledWith('invite_shared', expect.anything())
  })

  it('reports clipboard success only after the adapter confirms it', async () => {
    platform.copyText.mockResolvedValue(true)
    renderInvite()
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать ссылку' }))

    await waitFor(() => expect(platform.copyText).toHaveBeenCalledWith('https://example.test/join/abc'))
    expect(await screen.findByText('✓ Ссылка скопирована')).toBeVisible()
    expect(screen.getByTestId('invite-url')).toHaveTextContent('https://example.test/join/abc')
    expect(platform.shareInvite).not.toHaveBeenCalled()
  })

  it('shows a manual fallback after clipboard rejection', async () => {
    platform.copyText.mockResolvedValue(false)
    renderInvite()
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать ссылку' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Вы можете скопировать её вручную выше')
    expect(screen.getByTestId('invite-url')).toHaveTextContent('https://example.test/join/abc')
    expect(screen.getByRole('button', { name: 'Пригласить через MAX' })).toBeEnabled()
  })

  it('does not automatically share or record a recipient-open event on mount or remount', () => {
    const first = renderInvite()
    expect(platform.shareInvite).not.toHaveBeenCalled()
    first.unmount()
    renderInvite()
    expect(platform.shareInvite).not.toHaveBeenCalled()
    expect(state.track.mock.calls.flat().filter((value) => value === 'invite_opened')).toHaveLength(0)
  })

  it.each([
    ['full room', (room: RoomSnapshot) => { room.participants.push({ id: 'guest', displayName: 'Друг', avatarUrl: null, role: 'participant', intentReady: false }) }],
    ['matched room', (room: RoomSnapshot) => { room.state = 'matched' }],
    ['closed room', (room: RoomSnapshot) => { room.state = 'closed' }],
    ['expired invite', (room: RoomSnapshot) => { room.invite!.expires_at = '2099-10-20T11:59:00.000Z' }],
    ['room without invite permission', (room: RoomSnapshot) => { room.allowed_actions = ['edit_intent'] }],
    ['missing invite', (room: RoomSnapshot) => { room.invite = null }],
  ])('hides share and copy actions for %s', (_name, update) => {
    update(state.room!)
    renderInvite()

    expect(screen.queryByRole('button', { name: 'Пригласить через MAX' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Скопировать ссылку' })).not.toBeInTheDocument()
    expect(platform.shareInvite).not.toHaveBeenCalled()
    expect(platform.copyText).not.toHaveBeenCalled()
  })

  it('ignores repeated share clicks while a share surface is opening', async () => {
    let resolveShare!: (value: { status: 'opened' }) => void
    platform.shareInvite.mockReturnValue(new Promise((resolve) => { resolveShare = resolve }))
    renderInvite()
    const button = screen.getByRole('button', { name: 'Пригласить через MAX' })

    fireEvent.click(button)
    fireEvent.click(button)
    expect(platform.shareInvite).toHaveBeenCalledTimes(1)
    expect(button).toBeDisabled()

    resolveShare({ status: 'opened' })
    expect(await screen.findByText('Открыли отправку в MAX')).toBeVisible()
  })

  it('removes share actions and refreshes the room when an invite expires on screen', async () => {
    vi.useFakeTimers()
    const now = Date.UTC(2030, 4, 10, 12)
    vi.setSystemTime(now)
    state.room!.invite!.expires_at = new Date(now + 60_000).toISOString()
    renderInvite()
    expect(screen.getByRole('button', { name: 'Пригласить через MAX' })).toBeEnabled()

    await act(async () => { vi.advanceTimersByTime(60_000) })

    expect(screen.queryByRole('button', { name: 'Пригласить через MAX' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Скопировать ссылку' })).not.toBeInTheDocument()
    expect(state.refetch).toHaveBeenCalledOnce()
  })
})
