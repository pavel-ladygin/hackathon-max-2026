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
})
