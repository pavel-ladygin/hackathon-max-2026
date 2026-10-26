import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { RoomFlowPage } from './RoomPages'
import { ApiError } from '../../shared/api/errors'

const mocks = vi.hoisted(() => ({ room: undefined as any, replaceMyIntent: vi.fn() }))

vi.mock('../../features/rooms/queries', () => ({
  useRoom: (roomId: string | undefined) => ({ data: mocks.room, isPending: false, isError: false, refetch: vi.fn(), roomId }),
  useRoomEvents: () => ({ isPending: false, isError: false, data: { total: 0, items: [] }, refetch: vi.fn() }),
}))
vi.mock('../../features/discovery/queries', () => ({ useEventDetail: () => ({ isPending: false, isError: false, data: undefined, refetch: vi.fn() }) }))
vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: { user: { id: 'caller-1' }, preferences: {} }, isPending: false }) }))
vi.mock('../../shared/api/client', () => ({ apiClient: { replaceMyIntent: mocks.replaceMyIntent } }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackFailure: vi.fn() }))

const ownIntent = {
  dates: ['2099-11-20'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'],
  budget_max_minor: 200_000, radius_m: null, exclusion_slugs: [], location: null, free_text: 'CALLER_ONLY',
  version: 2, round_no: 1, submitted_at: '2099-01-01T00:00:00Z',
}

function exhausted(overrides: Record<string, unknown> = {}) {
  return {
    id: 'room-rounds', name: 'Раунды', city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', state: 'exhausted', round_no: 1, version: 10,
    participants: [], myIntent: ownIntent, pool: { version: 4, round_no: 1, state: 'exhausted', total: 0, voted_by_me: 1, my_pool_finished: true, room_exhausted: true, retry_after_seconds: null, exhaustion_reasons: [] },
    next_round: null, match: null, invite: null, allowed_actions: ['restart_with_new_intent'], ...overrides,
  }
}

function renderRoom() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-rounds/recovery']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /><Route path="/" element={<h1>Главная</h1>} /></Routes></MemoryRouter></QueryClientProvider>
  const view = render(tree())
  return { ...view, client, tree }
}

function RouteSwitcher() {
  const navigate = useNavigate()
  return <button type="button" onClick={() => navigate('/rooms/room-two/intent')}>Перейти к другой комнате</button>
}

describe('room round recovery flow', () => {
  beforeEach(() => {
    mocks.room = exhausted()
    mocks.replaceMyIntent.mockReset().mockResolvedValue(exhausted({ version: 12 }))
    window.sessionStorage.clear()
  })

  it('keeps an exhausted round visible while the caller is waiting for the other confirmation', () => {
    mocks.room = exhausted({
      version: 11,
      next_round: { round_no: 2, my_confirmed: true, confirmed_count: 1, my_intent: ownIntent },
      allowed_actions: ['wait'],
    })
    renderRoom()

    expect(screen.getByRole('heading', { name: 'Общих событий пока нет' })).toBeInTheDocument()
    expect(screen.getByText('Условия сохранены')).toBeInTheDocument()
    expect(screen.getByText(/Ждём, когда второй участник подтвердит/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Подтвердить мои условия' })).not.toBeInTheDocument()
  })

  it('shows the remote preparation notice first and preserves the caller-only recovery selection', async () => {
    const { rerender, tree } = renderRoom()
    mocks.room = exhausted({ version: 11, next_round: { round_no: 2, my_confirmed: false, confirmed_count: 1, my_intent: ownIntent }, allowed_actions: ['restart_with_new_intent', 'confirm_next_round'] })
    rerender(tree())

    expect(await screen.findByText('Друг предложил обновить подбор')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Оставить мои условия' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(screen.getByText('Если общих событий не найдётся, эта попытка всё равно будет использована')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Оставить мои условия' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    const [roomId, body, options] = mocks.replaceMyIntent.mock.calls[0]
    expect(roomId).toBe('room-rounds')
    expect(body).toMatchObject({ category_slugs: ['concerts'], free_text: 'CALLER_ONLY' })
    expect(body).not.toHaveProperty('other_participant_intent')
    expect(options).toMatchObject({ expectedRoundNo: 1, expectedRoomVersion: 11 })
    expect(options.idempotencyKey).toEqual(expect.any(String))
  })

  it('ignores an older remote snapshot instead of changing the acknowledged recovery screen', async () => {
    const { rerender, tree } = renderRoom()
    mocks.room = exhausted({ version: 12, next_round: { round_no: 2, my_confirmed: true, confirmed_count: 1, my_intent: ownIntent } })
    rerender(tree())
    expect(await screen.findByRole('heading', { name: 'Общих событий пока нет' })).toBeInTheDocument()
    mocks.room = exhausted({ version: 9, next_round: null })
    rerender(tree())
    expect(screen.getByRole('heading', { name: 'Общих событий пока нет' })).toBeInTheDocument()
  })

  it('requires Continue before adopting a remote waiting-to-vote transition', async () => {
    mocks.room = { ...exhausted(), state: 'ranking', pool: null, allowed_actions: ['wait'], version: 20 }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-rounds/waiting']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>
    const { rerender } = render(tree())
    mocks.room = { ...mocks.room, state: 'voting', version: 21, pool: { version: 5, round_no: 1, state: 'ready', total: 1, voted_by_me: 0, my_pool_finished: false, room_exhausted: false, retry_after_seconds: null, exhaustion_reasons: [] }, allowed_actions: ['vote'] }
    rerender(tree())
    expect(await screen.findByText('Подборка обновилась')).toBeInTheDocument()
    expect(screen.getByText('Формируем общий пул…')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    await waitFor(() => expect(screen.queryByText('Подборка обновилась')).not.toBeInTheDocument())
  })

  it('keeps prepared recovery visible until Continue adopts a remote voting snapshot', async () => {
    mocks.room = exhausted({ version: 10, next_round: { round_no: 2, my_confirmed: true, confirmed_count: 1, my_intent: ownIntent }, allowed_actions: ['wait'] })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-rounds/recovery']}><Routes>
      <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /><Route path="/rooms/room-rounds/vote" element={<h1>Голосование</h1>} />
    </Routes></MemoryRouter></QueryClientProvider>
    const { rerender } = render(tree())
    expect(screen.getByText('Условия сохранены')).toBeInTheDocument()
    mocks.room = { ...mocks.room, state: 'voting', version: 11, next_round: null, pool: { version: 5, round_no: 1, state: 'ready', total: 1, voted_by_me: 0, my_pool_finished: false, room_exhausted: false, retry_after_seconds: null, exhaustion_reasons: [] }, allowed_actions: ['vote'] }
    rerender(tree())
    expect(await screen.findByText('Подборка обновилась')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Общих событий пока нет' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(await screen.findByRole('heading', { name: 'Голосование' })).toBeInTheDocument()
  })

  it('does not retain the previous room screen when the route changes to another room', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-rounds/recovery']}><Routes><Route path="*" element={<><RouteSwitcher /><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></>} /></Routes></MemoryRouter></QueryClientProvider>
    render(tree())
    expect(screen.getByRole('heading', { name: 'Общих событий пока нет' })).toBeInTheDocument()
    mocks.room = { ...exhausted(), id: 'room-two', state: 'collecting_intents', version: 1, allowed_actions: ['edit_intent'] }
    fireEvent.click(screen.getByRole('button', { name: 'Перейти к другой комнате' }))
    expect(await screen.findByRole('heading', { name: 'Мои предпочтения' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Общих событий пока нет' })).not.toBeInTheDocument()
  })

  it('reuses one idempotency key when an intent submission is retried after an ambiguous failure', async () => {
    mocks.room = { ...exhausted(), state: 'collecting_intents', allowed_actions: ['edit_intent'] }
    mocks.replaceMyIntent.mockReset().mockRejectedValueOnce(new Error('ambiguous')).mockResolvedValueOnce(exhausted({ version: 11 }))
    renderRoom()
    const save = screen.getByRole('button', { name: 'Сохранить предпочтения' })
    fireEvent.click(save)
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить предпочтения' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(2))
    expect(mocks.replaceMyIntent.mock.calls[0][2].idempotencyKey).toBe(mocks.replaceMyIntent.mock.calls[1][2].idempotencyKey)
  })

  it('does not submit a stale intent form when Enter is pressed after a remote phase change', async () => {
    mocks.room = { ...exhausted(), state: 'collecting_intents', allowed_actions: ['edit_intent'] }
    const { rerender, tree } = renderRoom()
    const form = screen.getByRole('textbox', { name: 'Дополнительное пожелание' })
    mocks.room = { ...mocks.room, state: 'ranking', version: 11, pool: null, allowed_actions: ['wait'] }
    rerender(tree())
    expect(await screen.findByText('Подборка обновилась')).toBeInTheDocument()
    fireEvent.keyDown(form, { key: 'Enter', code: 'Enter', charCode: 13 })
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
  })

  it('shows Keep failure and preserves the current preparation choice without adopting', async () => {
    const { rerender, tree } = renderRoom()
    mocks.room = exhausted({ version: 11, next_round: { round_no: 2, my_confirmed: false, confirmed_count: 1, my_intent: ownIntent }, allowed_actions: ['restart_with_new_intent', 'confirm_next_round'] })
    mocks.replaceMyIntent.mockReset().mockRejectedValueOnce(new ApiError(500, { error: { code: 'INTERNAL_ERROR' } }))
    rerender(tree())
    fireEvent.click(await screen.findByRole('button', { name: 'Продолжить' }))
    fireEvent.click(screen.getByRole('button', { name: 'Оставить мои условия' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/Не удалось выполнить действие/)
    expect(screen.getByRole('heading', { name: 'Общих событий пока нет' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Оставить мои условия' })).toBeInTheDocument()
  })

  it('invalidates after a stale Keep and retries with the same key until the source changes', async () => {
    const { rerender, tree, client } = renderRoom()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    mocks.room = exhausted({ version: 11, next_round: { round_no: 2, my_confirmed: false, confirmed_count: 1, my_intent: ownIntent }, allowed_actions: ['restart_with_new_intent', 'confirm_next_round'] })
    mocks.replaceMyIntent.mockReset().mockRejectedValueOnce(new ApiError(409, { error: { code: 'CONFLICT' } })).mockResolvedValueOnce(exhausted({ version: 12 }))
    rerender(tree())
    fireEvent.click(await screen.findByRole('button', { name: 'Продолжить' }))
    fireEvent.click(screen.getByRole('button', { name: 'Оставить мои условия' }))
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['room', 'room-rounds'] }))
    fireEvent.click(screen.getByRole('button', { name: 'Оставить мои условия' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(2))
    expect(mocks.replaceMyIntent.mock.calls[0][2].idempotencyKey).toBe(mocks.replaceMyIntent.mock.calls[1][2].idempotencyKey)
  })

  it('does not submit an intent form while reviewing a remote proposal; Edit requires explicit Save', async () => {
    mocks.room = { ...exhausted(), state: 'collecting_intents', version: 10, allowed_actions: ['edit_intent'] }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-rounds/intent']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>
    const { rerender } = render(tree())
    fireEvent.change(screen.getByRole('textbox', { name: 'Дополнительное пожелание' }), { target: { value: 'UNSAVED_CALLER_DRAFT' } })
    mocks.replaceMyIntent.mockReset().mockResolvedValue(exhausted({ version: 12 }))
    mocks.room = exhausted({ version: 11, next_round: { round_no: 2, my_confirmed: false, confirmed_count: 1, my_intent: ownIntent }, allowed_actions: ['restart_with_new_intent', 'confirm_next_round'] })
    rerender(tree())
    fireEvent.click(await screen.findByRole('button', { name: 'Продолжить' }))
    fireEvent.click(screen.getByRole('button', { name: 'Изменить условия' }))
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: 'Дополнительное пожелание' })).toHaveValue('UNSAVED_CALLER_DRAFT')
    expect(screen.getByRole('button', { name: 'Сохранить предпочтения' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить предпочтения' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    expect(mocks.replaceMyIntent.mock.calls[0][1]).toMatchObject({ free_text: 'UNSAVED_CALLER_DRAFT' })
  })

  it('Keep submits the saved intent once and never submits a dirty local draft', async () => {
    mocks.room = { ...exhausted(), state: 'collecting_intents', version: 10, allowed_actions: ['edit_intent'] }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/room-rounds/intent']}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>
    const { rerender } = render(tree())
    fireEvent.change(screen.getByRole('textbox', { name: 'Дополнительное пожелание' }), { target: { value: 'UNSAVED_KEEP_DRAFT' } })
    mocks.replaceMyIntent.mockReset().mockResolvedValue(exhausted({ version: 12 }))
    mocks.room = exhausted({ version: 11, next_round: { round_no: 2, my_confirmed: false, confirmed_count: 1, my_intent: ownIntent }, allowed_actions: ['restart_with_new_intent', 'confirm_next_round'] })
    rerender(tree())
    fireEvent.click(await screen.findByRole('button', { name: 'Продолжить' }))
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Оставить мои условия' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    expect(mocks.replaceMyIntent.mock.calls[0][1]).toMatchObject({ free_text: 'CALLER_ONLY' })
    expect(mocks.replaceMyIntent.mock.calls[0][1].free_text).not.toBe('UNSAVED_KEEP_DRAFT')
  })

  it('migrates a legacy single recovery selection and preserves it when another choice is toggled', () => {
    mocks.room = exhausted({ pool: { ...exhausted().pool, total: 3 } })
    window.sessionStorage.setItem('room-recovery-v1:caller-1:room-rounds:1', JSON.stringify({ selected: 'category', selectedCategory: 'cinema' }))
    renderRoom()

    const category = screen.getByRole('checkbox', { name: 'Добавить категорию' })
    expect(category).toBeChecked()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Разрешить любое время' }))
    expect(category).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Разрешить любое время' })).toBeChecked()
  })
})
