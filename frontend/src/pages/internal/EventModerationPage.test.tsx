import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventModerationPage } from './EventModerationPage'

const submission = (id: string, status: 'pending' | 'approved' | 'rejected' = 'pending') => ({
  id,
  author_id: `author-${id}`,
  author_name: `Author ${id}`,
  city_id: 'city-1',
  starts_at: '2026-10-21T19:00:00+03:00',
  timezone: 'Europe/Moscow',
  payload: {
    title: `Event ${id}`,
    description: `Description ${id}`,
    city_id: 'city-1',
    venue_name: 'Venue',
    venue_address: 'Address',
    starts_at_local: '2026-10-21T19:00',
    price_mode: 'free' as const,
    category_slug: 'concerts',
  },
  status,
  created_at: '2026-10-01T10:00:00Z',
  decided_at: null,
  decided_by: null,
  rejection_reason: null,
  event_id: null,
})

const listing = (items = [submission('A'), submission('B')], total = items.length) => ({
  items,
  total,
  counts: { pending: 2, approved: 0, rejected: 0 },
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const detail = (id: string) => ({ submission: submission(id), duplicates: [], resource_domains: [] })
async function resolveDeferred<T>(request: ReturnType<typeof deferred<T>>, value: T) {
  await act(async () => {
    request.resolve(value)
    await request.promise
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

afterEach(() => vi.unstubAllGlobals())

describe('EventModerationPage async request handling', () => {
  it('keeps the latest selected detail when responses arrive in reverse order', async () => {
    const first = deferred<Response>()
    const second = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/A')) return first.promise
      if (url.endsWith('/B')) return second.promise
      if (url.endsWith('/domains')) return Promise.resolve(json({ domains: [] }))
      return Promise.resolve(json(listing()))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<EventModerationPage />)
    await screen.findByRole('button', { name: /Event A/ })

    fireEvent.click(screen.getByRole('button', { name: /Event A/ }))
    fireEvent.click(screen.getByRole('button', { name: /Event B/ }))
    await resolveDeferred(second, json(detail('B')))
    expect(await screen.findByRole('heading', { name: 'Event B' })).toBeInTheDocument()
    await resolveDeferred(first, json(detail('A')))

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Event B' })).toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: 'Event A' })).not.toBeInTheDocument()
  })

  it('ignores a stale detail error and does not let its finally clear the newer detail loading state', async () => {
    const first = deferred<Response>()
    const second = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/A')) return first.promise
      if (url.endsWith('/B')) return second.promise
      if (url.endsWith('/domains')) return Promise.resolve(json({ domains: [] }))
      return Promise.resolve(json(listing()))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<EventModerationPage />)
    await screen.findByRole('button', { name: /Event A/ })

    fireEvent.click(screen.getByRole('button', { name: /Event A/ }))
    fireEvent.click(screen.getByRole('button', { name: /Event B/ }))
    await resolveDeferred(first, json({ error: { code: 'INTERNAL' } }, 500))

    expect(screen.getByText('Загружаем заявку…')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await resolveDeferred(second, json(detail('B')))
    expect(await screen.findByRole('heading', { name: 'Event B' })).toBeInTheDocument()
    expect(screen.queryByText('Загружаем заявку…')).not.toBeInTheDocument()
  })

  it.each([
    ['filter', (button: HTMLElement) => fireEvent.click(button), 'approved'],
    ['pagination', (button: HTMLElement) => fireEvent.click(button), 'pending'],
  ])('invalidates a pending detail when the %s changes', async (change, click, nextStatus) => {
    const pendingDetail = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/A')) return pendingDetail.promise
      if (url.endsWith('/domains')) return Promise.resolve(json({ domains: [] }))
      if (url.includes('status=approved')) return Promise.resolve(json(listing([submission('C', 'approved')])))
      if (url.includes('offset=20')) return Promise.resolve(json(listing([submission('C')], 21)))
      return Promise.resolve(json(listing([submission('A'), submission('B')], 21)))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<EventModerationPage />)
    await screen.findByRole('button', { name: /Event A/ })
    fireEvent.click(screen.getByRole('button', { name: /Event A/ }))

    if (change === 'filter') click(screen.getByRole('button', { name: 'Одобрено' }))
    else click(screen.getByRole('button', { name: 'Дальше' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(nextStatus === 'approved' ? 'status=approved' : 'offset=20'), expect.anything()))
    await resolveDeferred(pendingDetail, json(detail('A')))

    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Event A' })).not.toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'Выберите заявку' })).toBeInTheDocument()
  })

  it.each(['approve', 'reject'] as const)('refreshes the currently selected queue after an in-flight %s when the filter changes', async (decision) => {
    const decisionResponse = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/domains')) return Promise.resolve(json({ domains: [] }))
      if (url.endsWith('/A') && init?.method !== 'POST') return Promise.resolve(json(detail('A')))
      if (url.endsWith(`/${decision}`) && init?.method === 'POST') return decisionResponse.promise
      if (url.includes('status=approved')) return Promise.resolve(json(listing([submission('C', 'approved')])))
      if (url.includes('status=rejected')) return Promise.resolve(json(listing([submission('D', 'rejected')])))
      return Promise.resolve(json(listing()))
    })
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('confirm', vi.fn(() => true))
    render(<EventModerationPage />)
    fireEvent.click(await screen.findByRole('button', { name: /Event A/ }))
    await screen.findByRole('heading', { name: 'Event A' })
    if (decision === 'reject') {
      fireEvent.change(screen.getByLabelText('Причина отклонения'), { target: { value: 'Неполные сведения' } })
    }
    fireEvent.click(screen.getByRole('button', { name: decision === 'approve' ? 'Одобрить и опубликовать' : 'Отклонить заявку' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(`/A/${decision}`), expect.objectContaining({ method: 'POST' })))
    const secondRow = screen.getByRole('button', { name: /Event B/ })
    expect(secondRow).toBeDisabled()
    fireEvent.click(secondRow)
    expect(screen.getByRole('heading', { name: 'Event A' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Одобрено' }))
    await screen.findByRole('button', { name: /Event C/ })
    const approvedFetchCount = fetchMock.mock.calls.filter(([url]) => String(url).includes('status=approved')).length
    await resolveDeferred(decisionResponse, json({ event_id: 'event-1' }))

    await waitFor(() => expect(fetchMock.mock.calls.filter(([url]) => String(url).includes('status=approved')).length).toBe(approvedFetchCount + 1))
    expect(await screen.findByRole('button', { name: /Event C/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Event A/ })).not.toBeInTheDocument()
  })
})
