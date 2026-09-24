import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { EventCard } from '../../shared/api/types'
import { CatalogMap } from './CatalogMap'

const { navigate, getMapEvents, FakeYMap, FakeYMapMarker, FakeYMapListener, listenerRef } = vi.hoisted(() => {
  class Marker {
    element: HTMLElement
    constructor(_options: unknown, element: HTMLElement) { this.element = element }
  }
  class Map {
    container: HTMLElement
    constructor(container: HTMLElement) { this.container = container }
    addChild(child: unknown) {
      if (child instanceof Marker) this.container.append(child.element)
      return this
    }
    removeChild(child: unknown) {
      if (child instanceof Marker) child.element.remove()
      return this
    }
    setLocation() {}
    destroy() { this.container.replaceChildren() }
  }
  class Listener { constructor(options: { onUpdate?: (event: unknown) => void }) { listenerRef.current = options.onUpdate ?? null } }
  return { navigate: vi.fn(), getMapEvents: vi.fn(), listenerRef: { current: null as null | ((event: unknown) => void) }, FakeYMap: Map, FakeYMapMarker: Marker, FakeYMapListener: Listener }
})

vi.mock('../../shared/api/client', () => ({ apiClient: { getMapEvents, searchEvents: vi.fn() } }))

vi.mock('@tanstack/react-query', () => ({
  useInfiniteQuery: () => ({
    data: { pages: [] },
    fetchNextPage: vi.fn(), hasNextPage: false, isFetching: false, isFetchingNextPage: false,
    isPending: false, isError: false, refetch: vi.fn(),
  }),
}))

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return { ...actual, useNavigate: () => navigate }
})

vi.mock('./yandexMaps', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./yandexMaps')>()
  return {
    ...actual,
    loadYandexMaps: async () => ({
      ready: Promise.resolve(), YMap: FakeYMap, YMapMarker: FakeYMapMarker,
      YMapDefaultFeaturesLayer: class {}, YMapDefaultSchemeLayer: class {}, YMapListener: FakeYMapListener,
    }),
  }
})

const event = (id: string, title: string): EventCard => ({
  id, title, subtitle: null, category_slug: 'concerts', starts_at: '2026-09-24T19:00:00+03:00',
  timezone: 'Europe/Moscow', date_label: `${title} · завтра, 19:00`, venue_name: `Площадка ${id}`,
  latitude: 55.75, longitude: 37.61, currency: 'RUB', price_label: 'Бесплатно', saved: false,
  reasons: [], imageUrl: null, distanceM: null, distanceLabel: null, priceFromMinor: 0,
})

describe('CatalogMap event preview integration', () => {
  beforeEach(() => {
    vi.stubEnv('VITE_YANDEX_MAPS_API_KEY', 'test-key')
    navigate.mockClear()
    getMapEvents.mockResolvedValue([
      { kind: 'event', id: 'a', longitude: 37.61, latitude: 55.75, event: event('a', 'Событие A') },
      { kind: 'event', id: 'b', longitude: 37.62, latitude: 55.75, event: event('b', 'Событие B') },
    ])
    listenerRef.current = null
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      disconnect() {}
    })
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  it('switches selected markers, closes the preview, and navigates only from Подробнее', async () => {
    render(<MemoryRouter><CatalogMap filters={{}} /></MemoryRouter>)

    const markerA = await screen.findByRole('button', { name: 'Открыть событие «Событие A»' })
    const markerB = screen.getByRole('button', { name: 'Открыть событие «Событие B»' })
    expect(markerA).toHaveAttribute('aria-pressed', 'false')
    expect(markerB).toHaveAttribute('aria-pressed', 'false')

    fireEvent.click(markerA)
    expect(screen.getByRole('region', { name: 'Событие: Событие A' })).toBeInTheDocument()
    expect(screen.getByText('Площадка a')).toBeInTheDocument()
    expect(markerA).toHaveAttribute('aria-pressed', 'true')
    expect(markerB).toHaveAttribute('aria-pressed', 'false')
    expect(navigate).not.toHaveBeenCalled()

    fireEvent.click(markerB)
    expect(screen.queryByRole('region', { name: 'Событие: Событие A' })).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Событие: Событие B' })).toBeInTheDocument()
    expect(screen.getByText('Площадка b')).toBeInTheDocument()
    expect(markerA).toHaveAttribute('aria-pressed', 'false')
    expect(markerB).toHaveAttribute('aria-pressed', 'true')
    expect(navigate).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Закрыть превью события' }))
    expect(screen.queryByRole('region', { name: 'Событие: Событие B' })).not.toBeInTheDocument()
    expect(markerB).toHaveAttribute('aria-pressed', 'false')
    expect(navigate).not.toHaveBeenCalled()

    fireEvent.click(markerA)
    fireEvent.click(screen.getByRole('button', { name: 'Подробнее' }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/events/a'))
  })

  it('keeps the selected event preview when a completed pan loads a different area', async () => {
    const nextEvent = event('c', 'Событие C')
    getMapEvents.mockResolvedValueOnce([
      { kind: 'event', id: 'a', longitude: 37.61, latitude: 55.75, event: event('a', 'Событие A') },
    ]).mockResolvedValueOnce([
      { kind: 'event', id: 'c', longitude: 37.8, latitude: 55.8, event: nextEvent },
    ])
    render(<MemoryRouter><CatalogMap filters={{ q: 'new' }} /></MemoryRouter>)
    const markerA = await screen.findByRole('button', { name: 'Открыть событие «Событие A»' })
    fireEvent.click(markerA)
    await waitFor(() => expect(listenerRef.current).toBeTypeOf('function'))
    listenerRef.current?.({ location: { center: [38, 56], zoom: 12 }, mapInAction: false })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Открыть событие «Событие C»' })).toBeInTheDocument(), { timeout: 2000 })
    expect(screen.getByRole('region', { name: 'Событие: Событие A' })).toBeInTheDocument()
  })

  it('clears the previous filter markers and selection as soon as filters change', async () => {
    const pending = new Promise<never>(() => {})
    getMapEvents.mockResolvedValueOnce([
      { kind: 'event', id: 'a', longitude: 37.61, latitude: 55.75, event: event('a', 'Событие A') },
    ]).mockReturnValueOnce(pending)
    const view = render(<MemoryRouter><CatalogMap filters={{ q: 'first' }} /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: 'Открыть событие «Событие A»' }))
    expect(screen.getByRole('region', { name: 'Событие: Событие A' })).toBeInTheDocument()
    view.rerender(<MemoryRouter><CatalogMap filters={{ q: 'second' }} /></MemoryRouter>)
    expect(screen.queryByRole('button', { name: 'Открыть событие «Событие A»' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Событие: Событие A' })).not.toBeInTheDocument()
  })
})
