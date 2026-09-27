import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AnalyticsDashboard } from './AnalyticsDashboard'

const emptyDashboard = {
  period_days: 30,
  generated_at: '2026-09-27T00:00:00Z',
  daily: [],
  summary: { active_users: 0, rooms_created: 0, rooms_joined: 0, activated_rooms: 0, rooms_matched: 0, rooms_ticket_clicked: 0, match_rate: null, match_ticket_ctr: null },
  recommendations: [],
  retention: [],
  guardrails: { empty_pool_rate: null },
  providers: [],
  api_performance: [],
  catalog_quality: [],
  vote_agreement: [],
  vote_agreement_summary: { events_with_same_vote: 0, events_voted_by_both: 0, agreement_rate: null },
  pool_diversity: { pools: 0, average_categories_top10: null, average_venues_top10: null, average_providers_top20: null },
  repeat_exposure: [],
}

afterEach(() => vi.unstubAllGlobals())

describe('AnalyticsDashboard', () => {
  it('loads empty data and requests each selected period from the private API', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => emptyDashboard })
    vi.stubGlobal('fetch', fetchMock)

    render(<AnalyticsDashboard />)

    expect(await screen.findByText('Данных пока нет')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/internal/analytics/dashboard?days=30', expect.objectContaining({ cache: 'no-store', credentials: 'same-origin' }))

    fireEvent.click(screen.getByRole('button', { name: '7 дней' }))
    await waitFor(() => expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/internal/analytics/dashboard?days=7', expect.objectContaining({ cache: 'no-store', credentials: 'same-origin' })))
    expect(screen.getByText('Данных пока нет')).toBeInTheDocument()
  })

  it('shows an API error and retries the request', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: false, status: 500 })
      .mockResolvedValueOnce({ ok: true, json: async () => emptyDashboard })
    vi.stubGlobal('fetch', fetchMock)

    render(<AnalyticsDashboard />)

    expect(await screen.findByText('Не получилось загрузить аналитику')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(await screen.findByText('Данных пока нет')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('labels room conversion metrics precisely and renders the unique-room funnel', async () => {
    const funnelDashboard = {
      ...emptyDashboard,
      daily: [{ date: '2026-09-27', active_users: 8, rooms_created: 4, rooms_invite_shared: 3, rooms_invite_opened: 3, rooms_joined: 3, activated_rooms: 2, rooms_matched: 1, rooms_ticket_clicked: 1, rooms_match_shown: 1, room_creation_rate: null, invite_share_rate: null, invite_open_rate: null, invite_join_conversion: null, room_activation_rate: 0.5, match_rate: 0.5, match_ticket_ctr: 1, median_time_to_match_seconds: 120, median_time_to_join_seconds: 60, p75_time_to_join_seconds: null, p90_time_to_join_seconds: null, median_swipes_to_match: 1, second_room_rate_7d: null, second_room_rate_30d: null, recommendation_top10_like_rate: null }],
      summary: {
        active_users: 8, rooms_created: 4, rooms_invite_shared: 3, rooms_joined: 3,
        activated_rooms: 2, rooms_matched: 1, rooms_ticket_clicked: 1, ticket_clicks: 3,
        room_activation_rate: 0.5, match_rate: 0.5, created_match_conversion: 0.25,
        match_event_open_ctr: 1, match_ticket_ctr: 1, no_match_rate: 0.5,
        pool_exhausted_rate: 0, median_time_to_match_seconds: 120,
        median_swipes_to_match: 1, median_time_to_join_seconds: 60,
      },
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => funnelDashboard }))

    render(<AnalyticsDashboard />)

    expect(await screen.findByText('Room Activation Rate')).toBeInTheDocument()
    expect(screen.getByText('Activated Room Match Rate')).toBeInTheDocument()
    expect(screen.getByText('Created → Match Conversion')).toBeInTheDocument()
    expect(screen.getByText('Match → Event Open CTR')).toBeInTheDocument()
    expect(screen.getByText('No-match rate')).toBeInTheDocument()
    expect(screen.getByText('Pool exhausted rate')).toBeInTheDocument()
    expect(screen.getByText('Ticket clicks')).toBeInTheDocument()
    expect(screen.getByText('Уникальные события с голосом до мэтча')).toBeInTheDocument()
    expect(screen.getByText('От активации до первого мэтча')).toBeInTheDocument()

    const funnel = screen.getByRole('heading', { name: 'Воронка комнат' }).closest('section')
    expect(funnel?.textContent).toContain('Уникальные комнаты когорты по дате создания')
    expect(funnel?.textContent).toContain('Приглашение отправлено или использовано')
    expect(funnel?.textContent).toContain('4')
    expect(funnel?.textContent).toContain('3')
    expect(funnel?.textContent).toContain('2')
    expect(funnel?.textContent).toContain('1')
  })
})
