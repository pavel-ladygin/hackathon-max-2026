import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { lazy, Suspense } from 'react'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { BrowserRouter, MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RoomFlowPage } from './RoomPages'
import { ApiError } from '../../shared/api/errors'

const mocks = vi.hoisted(() => ({
  room: undefined as any,
  roomIds: [] as Array<string | undefined>,
  roomRefetch: vi.fn(),
  event: undefined as any,
  eventIds: [] as Array<string | undefined>,
  eventRefetch: vi.fn(),
  events: undefined as any,
  replaceMyIntent: vi.fn(),
  vote: vi.fn(),
  createRoom: vi.fn(),
}))

vi.mock('../../features/rooms/queries', () => ({
  useRoom: (roomId: string | undefined) => { mocks.roomIds.push(roomId); return { ...(mocks.room ?? {}), refetch: mocks.roomRefetch } },
  useRoomEvents: () => mocks.events,
}))
vi.mock('../../features/discovery/queries', () => ({
  useEventDetail: (eventId: string | undefined) => { mocks.eventIds.push(eventId); return { ...(mocks.event ?? {}), refetch: mocks.eventRefetch } },
}))
vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: { user: { id: 'user-1' }, preferences: {} }, isPending: false }) }))
vi.mock('../../shared/api/client', () => ({ apiClient: { replaceMyIntent: mocks.replaceMyIntent, vote: mocks.vote, createRoom: mocks.createRoom } }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackFailure: vi.fn() }))

const intent = {
  dates: ['2099-10-25'], day_types: [], time_slots: ['evening'],
  category_slugs: ['concerts', 'standup'], budget_max_minor: 200_000,
  radius_m: null, exclusion_slugs: [], location: null, free_text: null,
  version: 1, round_no: 1, submitted_at: '2099-01-01T00:00:00Z',
}

function room(overrides: Record<string, unknown> = {}) {
  return {
    id: 'room-1', name: 'Test room', city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', state: 'voting', round_no: 1, version: 1,
    participants: [], myIntent: intent,
    pool: { version: 1, round_no: 1, state: 'ready', total: 2, voted_by_me: 0, my_pool_finished: false, room_exhausted: false, retry_after_seconds: null, exhaustion_reasons: [] },
    match: null, invite: null, allowed_actions: ['vote'],
    ...overrides,
  }
}

function roomTree(path: string, client: QueryClient) {
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} />
          <Route path="/rooms/new" element={<h1>Новая комната</h1>} />
          <Route path="/" element={<h1>Главная</h1>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const result = render(roomTree(path, client))
  return { ...result, client }
}

function event(id = 'event-1') {
  return { id, title: 'Концерт', subtitle: '', category_slug: 'concerts', starts_at: '2099-10-25T18:00:00Z', timezone: 'Europe/Moscow', date_label: '25 октября', venue_name: 'Клуб', other_occurrences_count: 0, currency: 'RUB', price_label: 'Бесплатно', saved: false, reasons: [], imageUrl: null, distanceM: null, distanceLabel: null, priceFromMinor: null }
}

function eventCard() {
  return { cursor: 'cursor-1', position: 0, event: event() }
}

function exhaustedRoom(total: number | null | undefined = 0, roundNo = 1) {
  return room({
    state: 'exhausted', round_no: roundNo,
    pool: total === null ? null : { version: 1, round_no: roundNo, state: 'exhausted', total, voted_by_me: 1, my_pool_finished: true, room_exhausted: true, retry_after_seconds: null, exhaustion_reasons: [] },
    allowed_actions: roundNo < 3 ? ['restart_with_new_intent'] : [],
  })
}

describe('room recovery and read-error reliability', () => {
  afterEach(() => vi.useRealTimers())
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-10-20T09:00:00+03:00'))
    window.sessionStorage.clear()
    mocks.room = { isPending: false, isError: false, data: room() }
    mocks.roomIds = []
    mocks.roomRefetch.mockReset().mockResolvedValue(undefined)
    mocks.event = { isPending: false, isError: false, data: event() }
    mocks.eventIds = []
    mocks.eventRefetch.mockReset().mockResolvedValue(undefined)
    mocks.events = { isPending: false, isError: false, data: { total: 1, items: [eventCard()] } }
    mocks.replaceMyIntent.mockReset().mockImplementation(async () => mocks.room?.data ?? room())
    mocks.vote.mockReset().mockResolvedValue({ accepted_vote: 'dislike', pool_version: 1, my_pool_finished: false, room_exhausted: false, match: null })
    mocks.createRoom.mockReset()
  })

  it('retries a failed room read using the same room ID and offers home navigation', async () => {
    mocks.room = { isPending: false, isError: true, error: new Error('offline') }
    renderAt('/rooms/room-read-error/vote')

    expect(await screen.findByText(/Нет соединения|offline|Что-то пошло не так/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Повторить|Загрузить снова/ }))
    await waitFor(() => expect(mocks.roomRefetch).toHaveBeenCalledTimes(1))
    expect(mocks.roomIds).toContain('room-read-error')
    fireEvent.click(screen.getByRole('button', { name: /На главную/ }))
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
  })

  it('offers the MAX login recovery action for an unauthorized room read without reloading during render', async () => {
    mocks.room = { isPending: false, isError: true, error: new ApiError(401, { error: { code: 'UNAUTHENTICATED' } }) }
    renderAt('/rooms/room-unauthorized/vote')

    expect(await screen.findByRole('button', { name: 'Восстановить вход в MAX' })).toBeInTheDocument()
  })

  it('retries a failed match event read using the same event ID', async () => {
    mocks.room = { isPending: false, isError: false, data: room({ state: 'matched', allowed_actions: ['view_match'], match: { id: 'match-1', event_id: 'event-match-42', participants: [] } }) }
    mocks.event = { isPending: false, isError: true, error: new Error('offline') }
    renderAt('/rooms/room-1/match')

    expect(await screen.findByRole('heading', { name: 'Мэтч найден, но событие не загрузилось' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Повторить|Загрузить снова/ }))
    await waitFor(() => expect(mocks.eventRefetch).toHaveBeenCalledTimes(1))
    expect(mocks.eventIds).toContain('event-match-42')
    expect(mocks.createRoom).not.toHaveBeenCalled()
  })

  it.each([
    [0, 'Общих событий пока нет'],
    [3, 'События закончились, общего лайка нет'],
    [null, 'Подборка завершена'],
  ] as const)('uses the pool total strictly (%s)', (_total, heading) => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(_total) }
    renderAt('/rooms/room-1/recovery')
    expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument()
  })

  it.each([1, 2, 24])('keeps voting available with pool size %i', async (total) => {
    mocks.room = { isPending: false, isError: false, data: room({ pool: { ...room().pool, total } }) }
    mocks.events = { isPending: false, isError: false, data: { total, items: [eventCard()] } }
    renderAt('/rooms/room-1/vote')
    if (total <= 2) expect(await screen.findByText(new RegExp(`В подборке всего ${total}`))).toBeInTheDocument()
    else expect(screen.queryByText(/В подборке всего/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Не подходит/ })).toBeEnabled()
  })

  it.each(['participant-a', 'participant-b'])('waits when %s has finished their own pool', async (participant) => {
    mocks.room = { isPending: false, isError: false, data: room({ id: participant, pool: { ...room().pool, my_pool_finished: true }, allowed_actions: ['view_pool', 'wait'] }) }
    renderAt(`/rooms/${participant}/vote`)
    expect(await screen.findByText('Вы просмотрели все события. Ждём, когда второй участник завершит выбор.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Подходит|Не подходит/ })).not.toBeInTheDocument()
  })

  it('offers an explicit new-room exit after round three without creating one', async () => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(0, 3) }
    renderAt('/rooms/room-1/recovery')
    fireEvent.click(screen.getByRole('button', { name: /Создать новую комнату/ }))
    expect(await screen.findByRole('heading', { name: 'Новая комната' })).toBeInTheDocument()
    expect(mocks.createRoom).not.toHaveBeenCalled()
  })

  it.each([0, 3])('offers the terminal new-room exit after round three with pool total %i', async (total) => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(total, 3) }
    renderAt('/rooms/room-1/recovery')
    expect(screen.getByRole('heading', { name: 'Лимит подборок исчерпан' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Создать новую комнату/ })).toBeEnabled()
    expect(screen.queryByRole('button', { name: /Подтвердить мои условия/ })).not.toBeInTheDocument()
    expect(mocks.createRoom).not.toHaveBeenCalled()
  })

  it('refreshes the same room snapshot after a 409 while preserving reviewed own intent', async () => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(3) }
    mocks.replaceMyIntent.mockRejectedValueOnce(new ApiError(409, { error: { code: 'CONFLICT' } }))
    const { client } = renderAt('/rooms/room-1/recovery')
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    fireEvent.click(screen.getByRole('button', { name: /Добавить категорию/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Кино' }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить мои условия' }))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['room', 'room-1'] }))
    expect(screen.getByText('Добавим категорию: Кино')).toBeInTheDocument()
    expect(mocks.replaceMyIntent).toHaveBeenCalledWith('room-1', expect.objectContaining({ category_slugs: ['concerts', 'standup', 'cinema'] }), expect.objectContaining({ expectedRoundNo: 1, expectedRoomVersion: 1, idempotencyKey: expect.any(String) }))
    fireEvent.click(screen.getByRole('button', { name: 'Назад к изменениям' }))
    expect(screen.getByRole('button', { name: 'Кино' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('has no date or budget recovery choice for an unknown city timezone and offers home', () => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(0, 1) }
    mocks.room.data.city_id = 'unknown-city'
    renderAt('/rooms/room-1/recovery')

    expect(screen.queryByRole('button', { name: /Добавить дату|Увеличить бюджет/ })).not.toBeInTheDocument()
    expect(screen.getByText(/Не удалось определить часовой пояс города/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'На главную' }))
    expect(screen.getByRole('heading', { name: 'Главная' })).toBeInTheDocument()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
  })

  it.each([0, 3])('keeps home after a pending recovery save finishes (pool total %i)', async (total) => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(total) }
    const savedSnapshot = structuredClone(mocks.room.data)
    let finishSave!: () => void
    mocks.replaceMyIntent.mockReturnValue(new Promise<void>((resolve) => { finishSave = resolve }))
    const { client } = renderAt('/rooms/room-1/recovery')
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    fireEvent.click(screen.getByRole('button', { name: /Разрешить любое время/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить мои условия' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: 'На главную' }))
    expect(screen.getByRole('heading', { name: 'Главная' })).toBeVisible()

    await act(async () => { finishSave() })
    await waitFor(() => expect(client.getMutationCache().getAll()[0].state.status).toBe('success'))
    expect(screen.getByRole('heading', { name: 'Главная' })).toBeVisible()
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['room', 'room-1'] })
    expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1)
    expect(mocks.room.data).toEqual(savedSnapshot)
    expect(mocks.createRoom).not.toHaveBeenCalled()
  })

  it('keeps home after a pending preference save finishes', async () => {
    mocks.room = { isPending: false, isError: false, data: room({ state: 'collecting_intents', allowed_actions: ['edit_intent'] }) }
    let finishSave!: () => void
    mocks.replaceMyIntent.mockReturnValue(new Promise<void>((resolve) => { finishSave = resolve }))
    const { client } = renderAt('/rooms/room-1/intent')

    fireEvent.click(screen.getByRole('button', { name: 'Сохранить предпочтения' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: 'Назад' }))
    expect(screen.getByRole('heading', { name: 'Главная' })).toBeVisible()

    await act(async () => { finishSave() })
    await waitFor(() => expect(client.getMutationCache().getAll()[0].state.status).toBe('success'))
    expect(screen.getByRole('heading', { name: 'Главная' })).toBeVisible()
    expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1)
  })

  it.each([false, true])('does not override Home while its lazy screen is still loading (snapshot update=%s)', async (updateSnapshot) => {
    window.history.replaceState(null, '', '/rooms/room-1/recovery')
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(0) }
    let finishSave!: () => void
    mocks.replaceMyIntent.mockReturnValue(new Promise<void>((resolve) => { finishSave = resolve }))
    let finishHome!: (value: { default: () => React.JSX.Element }) => void
    const Home = lazy(() => new Promise<{ default: () => React.JSX.Element }>((resolve) => { finishHome = resolve }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const tree = () => <QueryClientProvider client={client}><BrowserRouter><Suspense fallback={<p>Загрузка главной</p>}><Routes>
      <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} />
      <Route path="/" element={<Home />} />
    </Routes></Suspense></BrowserRouter></QueryClientProvider>
    const view = render(tree())
    fireEvent.click(screen.getByRole('button', { name: /Разрешить любое время/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить мои условия' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))

    fireEvent.click(screen.getByRole('button', { name: 'На главную' }))
    expect(window.location.pathname).toBe('/')
    // BrowserRouter has pushed Home, but the previous screen still renders.
    expect(screen.getByRole('button', { name: 'На главную' })).toBeVisible()
    if (updateSnapshot) {
      mocks.room.data = room({ state: 'ranking', allowed_actions: ['wait'] })
      view.rerender(tree())
    }
    await act(async () => { finishSave() })
    await waitFor(() => expect(client.getMutationCache().getAll()[0].state.status).toBe('success'))
    const destination = window.location.pathname
    await act(async () => { finishHome({ default: () => <h1>Главная</h1> }) })
    expect(destination).toBe('/')
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeVisible()
  })

  it('does not prefill or submit recovery when the caller has no saved intent', () => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(3) }
    mocks.room.data.myIntent = null
    renderAt('/rooms/room-1/recovery')

    expect(screen.getByText(/Больше нечего изменить/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Добавить категорию|Увеличить бюджет|Добавить дату/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'На главную' })).toBeEnabled()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
  })

  it.each([
    ['participant-a', 'A_PRIVATE_INTENT', 'B_PRIVATE_INTENT'],
    ['participant-b', 'B_PRIVATE_INTENT', 'A_PRIVATE_INTENT'],
  ])('%s recovery submits only that participant’s own intent', async (participant, ownMarker, otherMarker) => {
    const ownIntent = { ...intent, free_text: ownMarker }
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(3) }
    mocks.room.data.id = participant
    mocks.room.data.myIntent = ownIntent
    mocks.replaceMyIntent.mockResolvedValue(undefined)
    renderAt(`/rooms/${participant}/recovery`)

    fireEvent.click(screen.getByRole('button', { name: /Добавить категорию/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Кино' }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить мои условия' }))

    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(1))
    expect(mocks.replaceMyIntent).toHaveBeenCalledWith(participant, expect.objectContaining({ free_text: ownMarker, category_slugs: ['concerts', 'standup', 'cinema'] }), expect.objectContaining({ expectedRoundNo: 1, expectedRoomVersion: 1, idempotencyKey: expect.any(String) }))
    expect(JSON.stringify(mocks.replaceMyIntent.mock.calls[0][1])).not.toContain(otherMarker)
  })

  it('retains the chosen recovery intent after a failed confirmation and retries only that intent', async () => {
    mocks.room = { isPending: false, isError: false, data: exhaustedRoom(3) }
    mocks.replaceMyIntent.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined)
    renderAt('/rooms/room-1/recovery')

    fireEvent.click(screen.getByRole('button', { name: /Добавить категорию/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Кино' }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    const confirm = screen.getByRole('button', { name: 'Подтвердить мои условия' })
    fireEvent.click(confirm)

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('Добавим категорию: Кино')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Подтвердить мои условия' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить мои условия' }))

    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledTimes(2))
    const firstIntent = mocks.replaceMyIntent.mock.calls[0][1]
    const secondIntent = mocks.replaceMyIntent.mock.calls[1][1]
    expect(firstIntent.category_slugs).toEqual(['concerts', 'standup', 'cinema'])
    expect(secondIntent).toEqual(firstIntent)
    expect(mocks.replaceMyIntent).toHaveBeenCalledWith('room-1', expect.any(Object), expect.objectContaining({ expectedRoundNo: 1, expectedRoomVersion: 1, idempotencyKey: expect.any(String) }))
  })

  it('keeps reviewed recovery selection mounted through a background room read error', () => {
    mocks.room = { isPending: false, isError: false, isFetching: false, data: exhaustedRoom(3) }
    const { client, rerender } = renderAt('/rooms/room-1/recovery')

    fireEvent.click(screen.getByRole('button', { name: /Добавить категорию/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Кино' }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить изменение' }))
    expect(screen.getByText('Добавим категорию: Кино')).toBeInTheDocument()

    mocks.room = { ...mocks.room, isError: true, isFetching: false, error: new Error('offline') }
    rerender(roomTree('/rooms/room-1/recovery', client))
    expect(screen.getByRole('heading', { name: 'Не удалось обновить комнату' })).toBeInTheDocument()

    mocks.room = { ...mocks.room, isError: false, error: undefined }
    rerender(roomTree('/rooms/room-1/recovery', client))
    expect(screen.getByText('Добавим категорию: Кино')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Подтвердить мои условия' })).toBeEnabled()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
  })
})
