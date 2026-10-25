import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../shared/api/errors'
import { eventImageFallback } from '../../shared/lib/events'
import { createMatchedEventPreview } from '../../features/discovery/matchedEventPreview'
import { EventPage } from './EventPage'

const mocks = vi.hoisted(() => ({
  event: { data: undefined as any, isPending: false, isError: false, error: undefined as unknown, isFetching: false, refetch: vi.fn() },
  openTicketLink: vi.fn(),
  shareInvite: vi.fn(),
  recordTicketClick: vi.fn(),
  loadDetail: vi.fn(),
}))

vi.mock('../../features/discovery/queries', () => ({
  useEventDetail: () => { mocks.loadDetail(); return mocks.event },
  useSetSavedEvent: () => ({ isPending: false, mutate: vi.fn() }),
}))
vi.mock('../../shared/api/client', () => ({ apiClient: { recordBehavior: vi.fn(), recordTicketClick: mocks.recordTicketClick } }))
vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: { openTicketLink: mocks.openTicketLink, shareInvite: mocks.shareInvite } }))

const baseEvent = {
  id: '6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1', title: 'Jazz вечер', subtitle: 'Живой концерт', category_slug: 'concerts', starts_at: '2026-09-23T16:00:00Z', timezone: 'Europe/Moscow', date_label: 'Сегодня, 19:00', venue_name: 'г. Москва, ул. Берзарина, д. 16, метро Октябрьское Поле', venue: { id: 'venue-1', name: 'Клуб', address: 'г. Москва, ул. Берзарина, д. 16, метро Октябрьское Поле', metro: null, district: null }, distance_m: null, distance_label: null, price_from_minor: null, currency: 'RUB', price_label: 'Бесплатно', image_url: null, imageUrl: null, saved: false, reasons: [], description: '   ', endsAt: null, ticketAvailable: true, status: 'published', age_rating: null, dataProvenance: { source: 'demo', source_updated_at: null, is_demo: false }, images: [],
}

function renderPage(sharedEventEntry = false, onLeaveSharedEvent?: () => void, entryState: Record<string, unknown> = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={[{ pathname: `/events/${baseEvent.id}`, state: { ...entryState, ...(sharedEventEntry ? { sharedEventEntry: true } : {}) } }]}><Routes><Route path="/" element={<p>Главная страница</p>} /><Route path="/saved" element={<p>Раздел мэтчей</p>} /><Route path="/events/:eventId" element={<EventPage onLeaveSharedEvent={onLeaveSharedEvent} />} /></Routes></MemoryRouter></QueryClientProvider>)
}

describe('EventPage', () => {
  beforeEach(() => {
    mocks.event = { data: { ...baseEvent }, isPending: false, isError: false, error: undefined, isFetching: false, refetch: vi.fn() }
    mocks.openTicketLink.mockReset()
    mocks.shareInvite.mockReset()
    mocks.recordTicketClick.mockReset()
    mocks.loadDetail.mockReset()
    mocks.recordTicketClick.mockResolvedValue({ external_url: 'https://tickets.example/event-1' })
  })

  it('shows a description placeholder, hides empty reasons, and avoids repeating the venue address', () => {
    renderPage()

    expect(screen.getByText('Организатор пока не добавил описание')).toBeInTheDocument()
    expect(screen.queryByText('Почему вам подходит')).not.toBeInTheDocument()
    const venue = screen.getByText('г. Москва, ул. Берзарина, д. 16, метро Октябрьское Поле')
    expect(venue.querySelector('small')).toBeNull()
  })

  it('renders an HTML-decoded event title and image alternative text', () => {
    mocks.event = { ...mocks.event, data: { ...baseEvent, title: 'Квест "Алое золото"' } }
    renderPage()

    expect(screen.getByRole('heading', { name: 'Квест "Алое золото"' })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Квест "Алое золото"' })).toBeInTheDocument()
  })

  it('keeps the shimmer while an event photo loads and uses the neutral fallback only after an error', () => {
    mocks.event = { ...mocks.event, data: { ...baseEvent, imageUrl: 'https://media.kudago.com/photo.jpg' } }
    renderPage()

    const hero = screen.getByRole('img', { name: 'Jazz вечер' })
    expect(hero).toHaveAttribute('src', 'https://media.kudago.com/photo.jpg')
    expect(hero.className).toContain('eventImageLoading')
    expect(hero.style.backgroundImage).toBe('')
    const fallback = eventImageFallback('concerts')

    fireEvent.error(hero)
    expect(hero).toHaveAttribute('src', fallback)
  })

  it('shows a retry state for non-404 event loading errors', () => {
    mocks.event = { ...mocks.event, data: undefined, isError: true, error: new Error('network'), refetch: vi.fn() }
    renderPage()

    expect(screen.getByText('Не удалось загрузить событие')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(mocks.event.refetch).toHaveBeenCalledOnce()
  })

  it('keeps the not-found state distinct from a service error', () => {
    mocks.event = { ...mocks.event, data: undefined, isError: true, error: new ApiError(404, { error: { code: 'NOT_FOUND' } }) }
    renderPage()

    expect(screen.getByText('Событие не найдено')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Повторить' })).not.toBeInTheDocument()
  })

  it('shows past match information without loading the full event or offering tickets', () => {
    const matchedEvent = createMatchedEventPreview({ ...baseEvent, starts_at: '2000-10-24T16:00:00Z' })
    renderPage(false, undefined, { matchedEvent })
    expect(screen.getByRole('heading', { name: 'Мероприятие уже началось или завершилось' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Информация о событии' })).toHaveTextContent(baseEvent.title)
    expect(screen.getByText(/24 октября 2000/)).toBeInTheDocument()
    expect(mocks.loadDetail).not.toHaveBeenCalled()
    expect(mocks.recordTicketClick).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /Открыть билеты/ })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Вернуться к мэтчам' }))
    expect(screen.getByText('Раздел мэтчей')).toBeInTheDocument()
  })

  it('opens the normal full event and ticket action for a future match', () => {
    const futureEvent = { ...baseEvent, starts_at: '2099-10-24T16:00:00Z' }
    mocks.event.data = futureEvent
    renderPage(false, undefined, { matchedEvent: createMatchedEventPreview(futureEvent) })
    expect(screen.getByRole('heading', { name: baseEvent.title })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' })).toBeEnabled()
    expect(screen.queryByText('Этот мэтч остаётся в вашей истории')).not.toBeInTheDocument()
    expect(mocks.loadDetail).toHaveBeenCalled()
  })

  it('keeps future match information when the detail API returns not found', () => {
    mocks.event = { ...mocks.event, data: undefined, isError: true, error: new ApiError(404, { error: { code: 'NOT_FOUND' } }) }
    renderPage(false, undefined, { matchedEvent: createMatchedEventPreview({ ...baseEvent, starts_at: '2099-10-24T16:00:00Z' }) })
    expect(screen.getByRole('heading', { name: 'Событие больше недоступно' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Информация о событии' })).toHaveTextContent(baseEvent.title)
    expect(screen.queryByText('Событие не найдено')).not.toBeInTheDocument()
    expect(mocks.recordTicketClick).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Назад' }))
    expect(screen.getByText('Раздел мэтчей')).toBeInTheDocument()
  })

  it('does not label a network failure as an unavailable match', () => {
    mocks.event = { ...mocks.event, data: undefined, isError: true, error: new TypeError('offline') }
    renderPage(false, undefined, { matchedEvent: createMatchedEventPreview({ ...baseEvent, starts_at: '2099-10-24T16:00:00Z' }) })
    expect(screen.getByText('Не удалось загрузить событие')).toBeInTheDocument()
    expect(screen.queryByText('Событие больше недоступно')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(mocks.event.refetch).toHaveBeenCalledOnce()
  })

  it('ignores a match preview belonging to a different event', () => {
    renderPage(false, undefined, { matchedEvent: createMatchedEventPreview({ ...baseEvent, id: 'another-event', starts_at: '2000-10-24T16:00:00Z' }) })
    expect(screen.queryByText('Этот мэтч остаётся в вашей истории')).not.toBeInTheDocument()
    expect(mocks.loadDetail).toHaveBeenCalled()
  })

  it('renders recommendation reasons and a distinct venue address when provided', () => {
    mocks.event = { ...mocks.event, data: { ...baseEvent, venue_name: 'Клуб', reasons: [{ code: 'interest', text: 'Вам нравятся концерты' }] } }
    renderPage()

    expect(screen.getByText('Почему вам подходит')).toBeInTheDocument()
    expect(screen.getByText('✓ Вам нравятся концерты')).toBeInTheDocument()
    expect(screen.getByText('Клуб').querySelector('small')).toHaveTextContent(baseEvent.venue.address)
  })

  it('shows an actionable error when MAX cannot open the ticket link', async () => {
    mocks.openTicketLink.mockResolvedValue(false)
    renderPage()

    fireEvent.click(screen.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' }))

    await waitFor(() => expect(screen.getByText('Не удалось открыть билетный сервис. Можно повторить попытку.')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Открыть билеты во внешнем билетном сервисе' })).toBeEnabled()
  })

  it('uses the user event website label and explains when no link was supplied', () => {
    mocks.event = { ...mocks.event, data: { ...baseEvent, ticketAvailable: false, dataProvenance: { source: 'user', source_updated_at: null, is_demo: false } } }
    renderPage()

    expect(screen.getByText('Ссылка не указана.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Открыть сайт мероприятия' })).toHaveTextContent('Ссылка не указана')
    expect(screen.getByRole('button', { name: 'Открыть сайт мероприятия' })).toBeDisabled()
  })

  it('shares the event title and MAX event deep link', async () => {
    mocks.shareInvite.mockResolvedValue({ status: 'opened' })
    renderPage()

    fireEvent.click(screen.getByRole('button', { name: 'Поделиться через MAX' }))

    await waitFor(() => expect(mocks.shareInvite).toHaveBeenCalledWith({
      text: 'Посмотри событие «Jazz вечер»',
      link: expect.stringMatching(/^https:\/\/max\.ru(?:\/[^?]*)?\?startapp=event_6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1$/),
    }))
    expect(await screen.findByRole('status')).toHaveTextContent('Открыли отправку в MAX')
  })

  it('shows a retryable error if sharing fails', async () => {
    mocks.shareInvite.mockResolvedValueOnce({ status: 'failed' }).mockResolvedValueOnce({ status: 'opened' })
    renderPage()

    fireEvent.click(screen.getByRole('button', { name: 'Поделиться через MAX' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не удалось открыть отправку')

    fireEvent.click(screen.getByRole('button', { name: 'Поделиться через MAX' }))
    await waitFor(() => expect(mocks.shareInvite).toHaveBeenCalledTimes(2))
    expect(await screen.findByRole('status')).toHaveTextContent('Открыли отправку в MAX')
  })

  it('returns to the home page from an event opened through a shared link', () => {
    const onLeaveSharedEvent = vi.fn()
    renderPage(true, onLeaveSharedEvent)

    fireEvent.click(screen.getByRole('button', { name: 'Назад' }))

    expect(screen.getByText('Главная страница')).toBeInTheDocument()
    expect(onLeaveSharedEvent).toHaveBeenCalledOnce()
  })
})
