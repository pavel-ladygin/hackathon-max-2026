import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { RoomFlowPage } from './RoomPages'

const mocks = vi.hoisted(() => ({
  events: undefined as any,
  vote: vi.fn(),
  replaceMyIntent: vi.fn(),
  room: undefined as any,
}))

vi.mock('../../features/rooms/queries', () => ({
  useRoom: () => ({ data: mocks.room ?? {
    id: 'room-1', name: 'Test room', state: 'voting', round_no: 1, version: 1,
    participants: [], myIntent: null,
    pool: { version: 1, round_no: 1, state: 'ready', total: 2, voted_by_me: 0, my_pool_finished: false, room_exhausted: false, retry_after_seconds: null, exhaustion_reasons: [] },
    match: null, invite: null, allowed_actions: ['vote'],
  }, isPending: false, isError: false }),
  useRoomEvents: () => mocks.events,
}))

vi.mock('../../shared/api/client', () => ({ apiClient: { vote: mocks.vote, replaceMyIntent: mocks.replaceMyIntent } }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackFailure: vi.fn() }))

function roomEvent(id: string, title: string) {
  return {
    cursor: `cursor-${id}`, position: id === 'event-1' ? 0 : 1,
    event: {
      id, title, subtitle: 'Details', category_slug: 'concerts', starts_at: '2026-09-27T18:00:00Z', timezone: 'Europe/Moscow', date_label: 'Сегодня', venue_name: 'Клуб', other_occurrences_count: 0,
      currency: 'RUB', price_label: 'Бесплатно', saved: false, reasons: [{ code: 'interest', text: 'По интересам' }], imageUrl: null, distanceM: null, distanceLabel: null, priceFromMinor: null,
    },
  }
}

function renderRoom() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-1/vote']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>)
}

function recoveryRoom() {
  return {
    id: 'room-recovery', name: 'Test room', state: 'exhausted', round_no: 1, version: 1,
    participants: [], myIntent: {
      dates: ['2099-09-27'], day_types: [], time_slots: ['evening'],
      category_slugs: ['concerts', 'standup'], budget_max_minor: 200_000,
      radius_m: null, exclusion_slugs: [], location: null, free_text: null,
      version: 1, round_no: 1, submitted_at: '2099-01-01T00:00:00Z',
    },
    pool: { version: 1, round_no: 1, state: 'exhausted', total: 0, voted_by_me: 0, my_pool_finished: true, room_exhausted: true, retry_after_seconds: null, exhaustion_reasons: [] },
    match: null, invite: null, allowed_actions: ['restart_with_new_intent'],
  }
}

function renderRecoveryRoom() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-recovery/recovery']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>)
}

describe('room vote card transition', () => {
  beforeEach(() => {
    mocks.events = { data: { total: 2, items: [roomEvent('event-1', 'Первое событие')] }, isPending: false, isError: false }
    mocks.vote.mockReset()
    mocks.replaceMyIntent.mockReset().mockResolvedValue(undefined)
    mocks.room = undefined
  })

  it('shows the next card after the previous card exits on a vote', async () => {
    mocks.vote.mockImplementation(async () => {
      mocks.events = { ...mocks.events, data: { ...mocks.events.data, items: [roomEvent('event-2', 'Второе событие')] } }
      return { accepted_vote: 'dislike', pool_version: 1, my_pool_finished: false, room_exhausted: false, match: null }
    })

    renderRoom()
    expect(screen.getByRole('heading', { name: 'Первое событие' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Не подходит/ }))

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Второе событие' })).toBeInTheDocument(), { timeout: 3000 })
    expect(screen.queryByRole('heading', { name: 'Первое событие' })).not.toBeInTheDocument()
  })

  it('requires choosing and reviewing a concrete category before saving it', async () => {
    mocks.room = recoveryRoom()
    renderRecoveryRoom()

    fireEvent.click(screen.getByRole('button', { name: /Добавить категорию/ }))
    expect(screen.getByRole('heading', { name: 'Выберите категорию' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Кино' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: 'Проверить изменение' })).toBeDisabled()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Кино' }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    expect(screen.getByText('Добавим категорию: Кино')).toBeInTheDocument()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить и обновить' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    expect(mocks.replaceMyIntent).toHaveBeenCalledWith('room-recovery', expect.objectContaining({
      category_slugs: ['concerts', 'standup', 'cinema'],
    }))
  })

  it('requires choosing and reviewing an exact higher budget before saving it', async () => {
    mocks.room = recoveryRoom()
    renderRecoveryRoom()

    fireEvent.click(screen.getByRole('button', { name: /Увеличить бюджет/ }))
    expect(screen.getByRole('heading', { name: 'Новый лимит стоимости' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /до 2\s?500 ₽/ })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: 'Проверить изменение' })).toBeDisabled()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /до 2\s?500 ₽/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    expect(screen.getByText('Лимит стоимости увеличится до 2 500 ₽.')).toBeInTheDocument()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить и обновить' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    expect(mocks.replaceMyIntent).toHaveBeenCalledWith('room-recovery', expect.objectContaining({
      budget_max_minor: 250_000,
    }))
  })
})
