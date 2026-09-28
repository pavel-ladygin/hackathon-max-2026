import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventSourcesPage } from './EventSourcesPage'

afterEach(() => vi.unstubAllGlobals())

describe('EventSourcesPage', () => {
  it('loads source list and posts a mapping preview with same-origin admin headers', async () => {
    const requestCalls: { url: string; init?: RequestInit }[] = []
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      requestCalls.push({ url: path, init })
      if (path.endsWith('/test')) return new Response(JSON.stringify({ connection_ok: true, received: 1, valid: 1, invalid: 0, complete: true, errors: [], preview: [{ title: 'Jazz evening', starts_at: '2026-10-03T19:00:00+03:00', venue: 'Club' }] }), { status: 200 })
      return new Response(JSON.stringify({ sources: [] }), { status: 200 })
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<EventSourcesPage />)
    expect(await screen.findByText('Пока нет подключённых источников.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '＋ Добавить источник' }))
    fireEvent.change(screen.getByLabelText('Название'), { target: { value: 'City Events' } })
    fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'https://events.example.com/api' } })
    fireEvent.change(screen.getByLabelText('Authentication'), { target: { value: 'bearer' } })
    fireEvent.change(screen.getByLabelText('Секрет'), { target: { value: 'secret-value' } })
    fireEvent.change(screen.getByLabelText('External ID path'), { target: { value: 'id' } })
    fireEvent.change(screen.getByLabelText('Title path'), { target: { value: 'title' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить' }))

    expect(await screen.findByRole('heading', { name: 'Результат проверки' })).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/internal/event-sources/test', expect.objectContaining({ method: 'POST', credentials: 'same-origin', headers: expect.objectContaining({ 'X-Admin-Request': '1' }) })))
    const init = requestCalls.find(({ url }) => url.endsWith('/test'))?.init
    const body = JSON.parse(String(init?.body))
    expect(body.source_key).toBe('generic:city-events')
    expect(body.auth_secret).toBe('secret-value')
    expect(body.mapping.price_from).toBeDefined()
    expect(screen.getByText('Jazz evening')).toBeInTheDocument()
  })

  it('requires an explicit approval click and saves a new source before domain approval', async () => {
    const calls: { url: string; method: string; body?: string }[] = []
    const sourceId = '550e8400-e29b-41d4-a716-446655440000'
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: typeof init?.body === 'string' ? init.body : undefined })
      if (url.endsWith('/test')) return new Response(JSON.stringify({ connection_ok: true, received: 2, valid: 2, invalid: 0, complete: true, errors: [], resource_domains: [{ hostname: 'cdn.partner.ru', purpose: 'image', approved: false, count: 2 }], preview: [] }), { status: 200 })
      if (url === '/api/v1/internal/event-sources' && method === 'POST') return new Response(JSON.stringify({ source: { id: sourceId } }), { status: 201 })
      if (url.endsWith('/domains') && method === 'POST') return new Response(JSON.stringify({ domain: { id: 'domain-1', source_id: sourceId, hostname: 'cdn.partner.ru', purpose: 'image' } }), { status: 201 })
      if (url.endsWith('/domains')) return new Response(JSON.stringify({ domains: [{ id: 'domain-1', hostname: 'cdn.partner.ru', purpose: 'image' }] }), { status: 200 })
      return new Response(JSON.stringify({ sources: [] }), { status: 200 })
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<EventSourcesPage />)
    await screen.findByText('Пока нет подключённых источников.')
    fireEvent.click(screen.getByRole('button', { name: '＋ Добавить источник' }))
    fireEvent.change(screen.getByLabelText('Название'), { target: { value: 'Partner Events' } })
    fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'https://api.partner.ru/events' } })
    fireEvent.change(screen.getByLabelText('External ID path'), { target: { value: 'id' } })
    fireEvent.change(screen.getByLabelText('Title path'), { target: { value: 'title' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить' }))
    expect(await screen.findByText('cdn.partner.ru')).toBeInTheDocument()
    expect(calls.some((call) => call.url.endsWith('/domains') && call.method === 'POST')).toBe(false)

    fireEvent.click(screen.getByRole('button', { name: 'Сохранить источник и разрешить домен' }))
    await waitFor(() => expect(calls.some((call) => call.url === `/api/v1/internal/event-sources/${sourceId}/domains` && call.method === 'POST')).toBe(true))
    expect(JSON.parse(calls.find((call) => call.url.endsWith('/domains') && call.method === 'POST')!.body!).hostname).toBe('cdn.partner.ru')
    expect(await screen.findByText('✓ Разрешён')).toBeInTheDocument()
  })
})
