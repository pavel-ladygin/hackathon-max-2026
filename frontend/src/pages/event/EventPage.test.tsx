import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../shared/api/errors'
import { eventImageFallback } from '../../shared/lib/events'
import { EventPage } from './EventPage'

const mocks = vi.hoisted(() => ({
  event: { data: undefined as any, isPending: false, isError: false, error: undefined as unknown, isFetching: false, refetch: vi.fn() },
  openTicketLink: vi.fn(),
  recordTicketClick: vi.fn(),
}))

vi.mock('../../features/discovery/queries', () => ({
  useEventDetail: () => mocks.event,
  useSetSavedEvent: () => ({ isPending: false, mutate: vi.fn() }),
}))
vi.mock('../../shared/api/client', () => ({ apiClient: { recordBehavior: vi.fn(), recordTicketClick: mocks.recordTicketClick } }))
vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: { openTicketLink: mocks.openTicketLink } }))

const baseEvent = {
  id: 'event-1', title: 'Jazz вечер', subtitle: 'Живой концерт', category_slug: 'concerts', starts_at: '2026-09-23T16:00:00Z', timezone: 'Europe/Moscow', date_label: 'Сегодня, 19:00', venue_name: 'г. Москва, ул. Берзарина, д. 16, метро Октябрьское Поле', venue: { id: 'venue-1', name: 'Клуб', address: 'г. Москва, ул. Берзарина, д. 16, метро Октябрьское Поле', metro: null, district: null }, distance_m: null, distance_label: null, price_from_minor: null, currency: 'RUB', price_label: 'Бесплатно', image_url: null, imageUrl: null, saved: false, reasons: [], description: '   ', endsAt: null, ticketAvailable: true, status: 'published', age_rating: null, dataProvenance: { source: 'demo', source_updated_at: null, is_demo: false }, images: [],
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/events/event-1']}><EventPage /></MemoryRouter></QueryClientProvider>)
}

describe('EventPage', () => {
  beforeEach(() => {
    mocks.event = { data: { ...baseEvent }, isPending: false, isError: false, error: undefined, isFetching: false, refetch: vi.fn() }
    mocks.openTicketLink.mockReset()
    mocks.recordTicketClick.mockReset()
    mocks.recordTicketClick.mockResolvedValue({ external_url: 'https://tickets.example/event-1' })
  })

  it('shows a description placeholder, hides empty reasons, and avoids repeating the venue address', () => {
    renderPage()

    expect(screen.getByText('Организатор пока не добавил описание')).toBeInTheDocument()
    expect(screen.queryByText('Почему вам подходит')).not.toBeInTheDocument()
    const venue = screen.getByText('г. Москва, ул. Берзарина, д. 16, метро Октябрьское Поле')
    expect(venue.querySelector('small')).toBeNull()
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
})
