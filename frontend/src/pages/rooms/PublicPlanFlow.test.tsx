import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MyIntentDto, PublicPlan, RoomSnapshot } from '../../shared/api/types'
import { CreateRoomForm } from './CreateRoomForm'
import { PlanSummary } from './PlanSummary'
import { InvitePage, JoinPage, RoomFlowPage } from './RoomPages'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { cityToday } from './public-plan'

const mocks = vi.hoisted(() => ({
  bootstrapData: undefined as any,
  room: undefined as any,
  joinRoom: vi.fn(),
  replaceMyIntent: vi.fn(),
}))

vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: mocks.bootstrapData, isPending: false }) }))
vi.mock('../../features/rooms/queries', () => ({
  useRoom: () => ({ data: mocks.room, isPending: false, isError: false }),
  useRoomEvents: () => ({ data: { total: 0, items: [] }, isPending: false, isError: false }),
}))
vi.mock('../../shared/api/client', () => ({ apiClient: { joinRoom: mocks.joinRoom, replaceMyIntent: mocks.replaceMyIntent } }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackFailure: vi.fn() }))
vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: { isMax: true, shareInvite: vi.fn(), copyText: vi.fn(), getStartParam: () => '', getInitData: () => '' } }))

const cityId = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
const plan: PublicPlan = {
  dates: ['2099-11-20'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_025,
}
const inviteContext = {
  token: 'invite-e2e', room_id: 'room-server', room_name: 'Комната сервера', city_id: cityId,
  public_plan: plan, inviter: { id: 'creator', display_name: 'Создатель', avatar_url: null, role: 'creator', intent_ready: false },
  expires_at: '2099-12-01T00:00:00Z', already_joined: false, status: 'joinable' as const,
}

function roomFixture(overrides: Partial<RoomSnapshot> = {}): RoomSnapshot {
  return {
    id: 'room-server', name: 'Комната сервера', city_id: cityId, public_plan: plan, state: 'collecting_intents',
    round_no: 1, version: 1, participants: [{ id: 'creator', displayName: 'Создатель', avatarUrl: null, role: 'creator', intentReady: false }],
    myIntent: null, pool: null, match: null, invite: { url: '/join/invite-e2e', max_deep_link: 'max://invite-e2e', expires_at: '2099-12-01T00:00:00Z' },
    allowed_actions: ['invite', 'edit_intent'], created_at: '2099-01-01T00:00:00Z', expires_at: '2099-12-01T00:00:00Z', ...overrides,
  }
}

function roomView(initialPath = '/rooms/room-server/intent') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={[initialPath]}><Routes><Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} /></Routes></MemoryRouter></QueryClientProvider>
  const view = render(tree())
  return { ...view, rerenderRoom: () => view.rerender(tree()) }
}

function renderJoin() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const tree = () => <QueryClientProvider client={client}><MemoryRouter initialEntries={['/join/invite-e2e']}><Routes><Route path="/join/:inviteToken" element={<JoinPage />} /><Route path="/rooms/:roomId/:roomScreen" element={<p>Joined intent screen</p>} /></Routes></MemoryRouter></QueryClientProvider>
  const view = render(tree())
  return { ...view, rerenderJoin: () => view.rerender(tree()) }
}


describe('public room plan flow', () => {
  beforeEach(() => {
    mocks.bootstrapData = undefined
    mocks.room = undefined
    mocks.joinRoom.mockReset().mockResolvedValue(roomFixture())
    mocks.replaceMyIntent.mockReset().mockResolvedValue(undefined)
    window.sessionStorage.clear()
  })

  afterEach(() => window.sessionStorage.clear())

  it('submits the selected city and public plan in minor currency units', () => {
    const onSubmit = vi.fn()
    render(<CreateRoomForm profileCityId={cityId} draftKey="public-plan-create-test" pending={false} success={false} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByLabelText('Название комнаты'), { target: { value: 'Куда идём в субботу?' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    const date = cityToday('Europe/Moscow')
    fireEvent.click(screen.getByRole('button', { name: 'Выбор дат' }))
    const dateLabel = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' }).format(new Date(`${date}T12:00:00`))
    fireEvent.click(screen.getByRole('gridcell', { name: dateLabel }))
    fireEvent.click(screen.getByRole('button', { name: 'Применить' }))
    fireEvent.click(screen.getByRole('button', { name: 'Вечер' }))
    fireEvent.click(screen.getByRole('button', { name: 'Концерты' }))
    fireEvent.change(screen.getByLabelText(/Бюджет на человека/), { target: { value: '2500.25' } })
    fireEvent.click(screen.getByRole('button', { name: 'Создать комнату' }))

    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Куда идём в субботу?', city_id: cityId,
      public_plan: { dates: [date], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_025 },
    })
  })

  it('keeps validation and server errors visible and does not submit an incomplete plan', () => {
    const onSubmit = vi.fn()
    const { rerender } = render(<CreateRoomForm profileCityId={cityId} draftKey="public-plan-error-test" pending={false} success={false} onSubmit={onSubmit} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Создать комнату' }))

    expect(screen.getAllByText('Добавьте хотя бы одну дату').length).toBeGreaterThan(1)
    expect(screen.getAllByText('Выберите время или отметьте «Любое время»').length).toBeGreaterThan(1)
    expect(screen.getAllByText('Выберите хотя бы одну категорию').length).toBeGreaterThan(1)
    expect(onSubmit).not.toHaveBeenCalled()

    rerender(<CreateRoomForm profileCityId={cityId} draftKey="public-plan-error-test" pending={false} success={false} error={new Error('Ошибка создания комнаты')} onSubmit={onSubmit} />)
    expect(screen.getByText('Не удалось создать комнату')).toHaveAttribute('role', 'alert')
  })

  it('shows the server public plan in the creator summary and keeps legacy rooms explicit', () => {
    const { rerender } = render(<PlanSummary plan={plan} cityId={cityId} />)
    expect(screen.getByText('Москва')).toBeVisible()
    expect(screen.getByText(/20 ноября/)).toBeVisible()
    expect(screen.getByText(/До\s*2.?500,25/)).toBeVisible()
    rerender(<PlanSummary plan={null} cityId={cityId} />)
    expect(screen.getByText('Детали встречи можно уточнить у пригласившего.')).toBeVisible()
  })

  it('shows API invite preview without joining, including the legacy no-plan state', async () => {
    mocks.bootstrapData = { inviteContext }
    const view = renderJoin()
    expect(screen.getByRole('region', { name: 'Публичное предложение комнаты' })).toBeVisible()
    expect(screen.getByRole('heading', { name: /Создатель/ })).toBeVisible()
    expect(screen.getByText(/20 ноября/)).toBeVisible()
    expect(mocks.joinRoom).not.toHaveBeenCalled()

    mocks.bootstrapData = { inviteContext: { ...inviteContext, public_plan: null } }
    view.rerenderJoin()
    expect(screen.getByText('Детали встречи можно уточнить у пригласившего.')).toBeVisible()
    expect(mocks.joinRoom).not.toHaveBeenCalled()

    mocks.bootstrapData = { inviteContext: null }
    view.rerenderJoin()
    expect(screen.queryByRole('region', { name: 'Публичное предложение комнаты' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Присоединиться' })).toBeDisabled()
    expect(mocks.joinRoom).not.toHaveBeenCalled()
  })

  it('prefills personal fields from the proposal, saves only on explicit submit, then prefers saved intent', async () => {
    mocks.room = roomFixture()
    const firstView = roomView()
    expect(screen.getByRole('heading', { name: 'Подтвердите личные пожелания' })).toBeVisible()
    expect(screen.getByText(/Мы перенесли условия из публичного плана встречи/)).toBeVisible()
    expect(screen.getByRole('button', { name: /Удалить .*20 нояб/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Вечер' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'true')
    expect((screen.getByLabelText(/Бюджет, ₽/) as HTMLInputElement).value).toBe('2500.25')
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить пожелания' }))
    await waitFor(() => expect(mocks.replaceMyIntent).toHaveBeenCalledWith('room-server', expect.objectContaining({
      dates: ['2099-11-20'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_025,
    })))
    firstView.unmount()

    const savedIntent: MyIntentDto = { dates: ['2099-11-25'], day_types: ['weekday'], time_slots: ['morning'], category_slugs: ['theatre'], budget_max_minor: 400_000, radius_m: null, exclusion_slugs: [], location: null, free_text: null, version: 1, round_no: 1, submitted_at: '2099-01-01T00:00:00Z' }
    mocks.room = roomFixture({ myIntent: savedIntent })
    roomView()
    expect(screen.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()
    expect(screen.queryByText(/Мы перенесли условия из публичного плана встречи/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Удалить .*25 нояб/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Утро' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Театр' })).toHaveAttribute('aria-pressed', 'true')
    expect((screen.getByLabelText(/Бюджет, ₽/) as HTMLInputElement).value).toBe('4000')
  })

  it('preserves edits across refetch and resets them when the room changes', () => {
    mocks.room = roomFixture()
    const view = roomView()
    fireEvent.click(screen.getByRole('button', { name: 'Кино' }))
    expect(screen.getByRole('button', { name: 'Кино' })).toHaveAttribute('aria-pressed', 'true')

    mocks.room = roomFixture({ public_plan: { ...plan, dates: ['2099-11-22'] } })
    view.rerenderRoom()
    expect(screen.getByRole('button', { name: 'Кино' })).toHaveAttribute('aria-pressed', 'true')

    mocks.room = roomFixture({ id: 'room-other', public_plan: { ...plan, dates: ['2099-11-24'], category_slugs: ['theatre'] } })
    view.rerenderRoom()
    expect(screen.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: 'Театр' })).toHaveAttribute('aria-pressed', 'true')
  })
  it('keeps missing invite context neutral and never joins from a missing preview', () => {
    mocks.bootstrapData = { inviteContext: null }
    renderJoin()
    expect(screen.getByRole('button', { name: 'Присоединиться' })).toBeDisabled()
    expect(screen.queryByRole('region', { name: 'Публичное предложение комнаты' })).not.toBeInTheDocument()
    expect(mocks.joinRoom).not.toHaveBeenCalled()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
  })

  it('keeps legacy personal defaults without submitting an invented public plan', () => {
    mocks.room = roomFixture({ public_plan: undefined })
    roomView()
    expect(screen.getByRole('heading', { name: 'Мои предпочтения' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Сохранить предпочтения' })).toBeVisible()
    expect(screen.queryByText(/Мы перенесли условия из публичного плана встречи/)).not.toBeInTheDocument()
    expect(screen.getByLabelText('Бюджет, ₽')).toHaveValue('3000')
    expect(screen.getByRole('button', { name: 'Стендап' })).toHaveAttribute('aria-pressed', 'true')
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
  })

  it('shows a past plan date unchanged and requires an explicit valid replacement', async () => {
    mocks.room = roomFixture({ public_plan: { ...plan, dates: ['2000-01-01'] } })
    roomView()
    expect(screen.getByRole('button', { name: /Удалить .*1 янв/ })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить пожелания' }))
    expect(await screen.findByText(/прошедшие даты нужно изменить самостоятельно/)).toBeVisible()
    expect(mocks.replaceMyIntent).not.toHaveBeenCalled()
    expect(mocks.room.public_plan.dates).toEqual(['2000-01-01'])
  })

  it('shares only the returned server proposal even when own intent has different private values', async () => {
    mocks.room = roomFixture({ myIntent: { dates: ['2099-12-03'], day_types: [], time_slots: ['morning'], category_slugs: ['sports'], budget_max_minor: 987654, free_text: 'PRIVATE_MARKER', location: { lat: 55.7, lng: 37.6 }, radius_m: null, exclusion_slugs: [], version: 1, round_no: 1, submitted_at: '2099-01-01T00:00:00Z' } })
    vi.mocked(maxPlatform.shareInvite).mockResolvedValueOnce({ status: 'opened' })
    render(<MemoryRouter initialEntries={['/rooms/room-server/invite']}><Routes><Route path="/rooms/:roomId/invite" element={<InvitePage />} /></Routes></MemoryRouter>)
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))
    await waitFor(() => expect(maxPlatform.shareInvite).toHaveBeenCalled())
    const payload = vi.mocked(maxPlatform.shareInvite).mock.calls.at(-1)![0]
    expect(payload.text).toContain('20 ноября')
    expect(payload.text).toContain('Концерты')
    expect(payload.text).toContain('2 500,25')
    expect(payload.text).not.toContain('PRIVATE_MARKER')
    expect(payload.text).not.toContain('987654')
    expect(payload.text).not.toContain('55.7')
    expect(payload.text).not.toContain('invite-e2e')
  })

})
