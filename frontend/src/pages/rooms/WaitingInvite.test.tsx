import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RoomSnapshot } from '../../shared/api/types'
import { RoomFlowPage } from './RoomPages'

const platform = vi.hoisted(() => ({ shareInvite: vi.fn(), copyText: vi.fn() }))
const state = vi.hoisted(() => ({ room: null as RoomSnapshot | null, refetch: vi.fn() }))
vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: platform }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackFailure: vi.fn() }))
vi.mock('../../features/rooms/queries', () => ({
  useRoom: () => ({ data: state.room, isPending: false, isError: false, refetch: state.refetch }),
  useRoomEvents: vi.fn(),
}))

function roomFixture(): RoomSnapshot {
  return {
    id: 'room-1', name: 'Планы на вечер', city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24',
    state: 'collecting_intents', round_no: 1, version: 2,
    participants: [{ id: 'creator', displayName: 'Павел', avatarUrl: null, role: 'creator', intentReady: true }],
    myIntent: { dates: ['2099-10-21'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 200_000, radius_m: null, exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2099-10-20T12:00:00Z' },
    pool: null, match: null,
    invite: { url: 'https://example.test/join/abc', max_deep_link: 'https://max.ru/example?startapp=abc', expires_at: '2099-10-22T12:00:00Z' },
    allowed_actions: ['wait', 'invite'], created_at: '2099-10-20T12:00:00Z', expires_at: '2099-10-22T12:00:00Z',
  }
}

function renderWaiting() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-1/waiting']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>
  const rendered = render(tree())
  return { ...rendered, refresh: () => rendered.rerender(tree()) }
}

describe('BUG-01 waiting invitation recovery', () => {
  beforeEach(() => {
    vi.spyOn(Date, 'now').mockReturnValue(Date.UTC(2099, 9, 20, 12))
    state.room = roomFixture()
    state.refetch.mockReset().mockResolvedValue(undefined)
    platform.copyText.mockReset().mockResolvedValue(true)
    platform.shareInvite.mockReset().mockResolvedValue({ status: 'opened' })
  })
  afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

  it('restores the server invite after remount and copies/shares without leaving waiting', async () => {
    const first = renderWaiting()
    expect(screen.getByRole('heading', { name: 'Ждём второго участника' })).toBeVisible()
    expect(screen.getByTestId('invite-url')).toHaveTextContent(state.room!.invite!.url)
    expect(platform.shareInvite).not.toHaveBeenCalled()
    expect(platform.copyText).not.toHaveBeenCalled()
    first.unmount()
    renderWaiting()
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать ссылку' }))
    expect(await screen.findByText('✓ Ссылка скопирована')).toBeVisible()
    expect(platform.copyText).toHaveBeenCalledWith('https://example.test/join/abc')
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Пригласить через MAX' })).toBeEnabled())
    expect(screen.queryByText('Открыли отправку в MAX')).not.toBeInTheDocument()
    expect(platform.shareInvite).toHaveBeenCalledWith({ text: expect.any(String), link: 'https://max.ru/example?startapp=abc' })
    expect(screen.getByRole('heading', { name: 'Ждём второго участника' })).toBeVisible()
  })

  it.each([
    [{ status: 'cancelled' }, 'Отправка отменена'],
    [{ status: 'unavailable' }, 'Не удалось открыть отправку'],
  ])('keeps copying available after share result %s', async (result, message) => {
    platform.shareInvite.mockResolvedValue(result)
    renderWaiting()
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))
    expect(await screen.findByText(new RegExp(message))).toBeVisible()
    expect(screen.getByRole('button', { name: 'Скопировать ссылку' })).toBeEnabled()
  })

  it.each([false, new Error('Clipboard rejected')])('keeps a manual fallback when copying fails: %s', async (result) => {
    if (result instanceof Error) platform.copyText.mockRejectedValue(result)
    else platform.copyText.mockResolvedValue(result)
    renderWaiting()
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать ссылку' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Вы можете скопировать её вручную выше')
    expect(screen.getByTestId('invite-url')).toHaveTextContent('https://example.test/join/abc')
    expect(screen.getByRole('button', { name: 'Пригласить через MAX' })).toBeEnabled()
  })

  it('handles a rejected share promise and permits retry', async () => {
    platform.shareInvite.mockRejectedValueOnce(new Error('Bridge rejected'))
    renderWaiting()
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не удалось открыть отправку')
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Пригласить через MAX' })).toBeEnabled())
    expect(screen.queryByText('Открыли отправку в MAX')).not.toBeInTheDocument()
  })

  it.each([
    ['guest without invite', (room: RoomSnapshot) => { room.invite = null; room.allowed_actions = ['wait'] }],
    ['missing permission', (room: RoomSnapshot) => { room.allowed_actions = ['wait'] }],
    ['expired invite', (room: RoomSnapshot) => { room.invite!.expires_at = '2099-10-20T11:00:00Z' }],
    ['voting wait', (room: RoomSnapshot) => { room.state = 'voting' }],
    ['closed room', (room: RoomSnapshot) => { room.state = 'closed' }],
  ])('hides link and actions for %s', (_name, update) => {
    update(state.room!)
    renderWaiting()
    expect(screen.queryByTestId('invite-url')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Пригласить через MAX' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Скопировать ссылку' })).not.toBeInTheDocument()
  })

  it('removes the invitation as soon as a second participant appears, even with stale invite permission', () => {
    const view = renderWaiting()
    state.room = { ...state.room!, participants: [...state.room!.participants, { id: 'guest', displayName: 'Друг', avatarUrl: null, role: 'participant', intentReady: false }] }
    view.refresh()
    expect(screen.getByText('2 / 2 участника')).toBeVisible()
    expect(screen.queryByTestId('invite-url')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Пригласить через MAX' })).not.toBeInTheDocument()
  })

  it('expires the invitation while waiting and refreshes the room', async () => {
    vi.restoreAllMocks()
    vi.useFakeTimers()
    const now = Date.UTC(2099, 9, 20, 12)
    vi.setSystemTime(now)
    state.room!.invite!.expires_at = new Date(now + 60_000).toISOString()
    renderWaiting()
    expect(screen.getByRole('button', { name: 'Пригласить через MAX' })).toBeEnabled()
    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(screen.queryByTestId('invite-url')).not.toBeInTheDocument()
    expect(state.refetch).toHaveBeenCalledOnce()
  })

  it('blocks repeated and mixed actions while sharing', async () => {
    let finish!: (value: { status: 'opened' }) => void
    platform.shareInvite.mockReturnValue(new Promise((resolve) => { finish = resolve }))
    renderWaiting()
    const share = screen.getByRole('button', { name: 'Пригласить через MAX' })
    const copy = screen.getByRole('button', { name: 'Скопировать ссылку' })
    fireEvent.click(share)
    fireEvent.click(share)
    fireEvent.click(copy)
    expect(platform.shareInvite).toHaveBeenCalledTimes(1)
    expect(platform.copyText).not.toHaveBeenCalled()
    expect(copy).toBeDisabled()
    await act(async () => finish({ status: 'opened' }))
    expect(copy).toBeEnabled()
  })

  it.each(['room-1', 'room-2'])('does not leak pending actions or feedback into a new invite in %s', async (roomId) => {
    let finish!: (value: { status: 'opened' }) => void
    platform.shareInvite.mockReturnValueOnce(new Promise((resolve) => { finish = resolve }))
    const view = renderWaiting()
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))
    state.room = { ...roomFixture(), id: roomId, invite: { ...roomFixture().invite!, url: 'https://example.test/join/second' } }
    view.refresh()
    expect(screen.getByRole('button', { name: 'Скопировать ссылку' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать ссылку' }))
    expect(await screen.findByText('✓ Ссылка скопирована')).toBeVisible()
    expect(platform.copyText).toHaveBeenCalledWith('https://example.test/join/second')
    await act(async () => finish({ status: 'opened' }))
    expect(screen.queryByText('Открыли отправку в MAX')).not.toBeInTheDocument()
    expect(screen.getByText('✓ Ссылка скопирована')).toBeVisible()
  })
})
