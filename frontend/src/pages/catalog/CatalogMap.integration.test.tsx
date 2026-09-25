import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { EventCard } from '../../shared/api/types'
import { CatalogMap } from './CatalogMap'

const { navigate, getMapEvents, setLocation, mapSize, FakeYMap, FakeYMapMarker, FakeYMapListener, listenerRef } = vi.hoisted(() => {
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
    setLocation(location: unknown) { setLocation(location) }
    destroy() { this.container.replaceChildren() }
  }
  class Listener { constructor(options: { onUpdate?: (event: unknown) => void }) { listenerRef.current = options.onUpdate ?? null } }
  return { navigate: vi.fn(), getMapEvents: vi.fn(), setLocation: vi.fn(), mapSize: { width: 700, height: 500 }, listenerRef: { current: null as null | ((event: unknown) => void) }, FakeYMap: Map, FakeYMapMarker: Marker, FakeYMapListener: Listener }
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
    mapSize.width = 700
    mapSize.height = 500
    navigate.mockClear()
    setLocation.mockClear()
    getMapEvents.mockReset()
    getMapEvents.mockResolvedValue([
      { kind: 'event', id: 'a', longitude: 37.61, latitude: 55.75, event: event('a', 'Событие A') },
      { kind: 'event', id: 'b', longitude: 37.62, latitude: 55.75, event: event('b', 'Событие B') },
    ])
    listenerRef.current = null
    vi.stubGlobal('ResizeObserver', class {
      constructor(private callback: ResizeObserverCallback) {}
      observe() { this.callback([{ contentRect: mapSize } as ResizeObserverEntry], this as unknown as ResizeObserver) }
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

  it('opens five clustered events, pages through them, and closes without moving the camera', async () => {
    const members = Array.from({ length: 5 }, (_, index) => ({ kind: 'event' as const, id: `e${index}`, longitude: 37.61, latitude: 55.75, event: event(`e${index}`, `Событие ${index}`) }))
    const cluster = {
      kind: 'cluster', id: 'cluster-1', longitude: 37.61, latitude: 55.75,
      west: 37.6, south: 55.74, east: 37.62, north: 55.76, count: 5, members,
    }
    getMapEvents.mockResolvedValue([cluster])
    render(<MemoryRouter><CatalogMap filters={{}} /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: 'Показать 5 событий' }))
    expect(await screen.findByRole('group', { name: 'Кластер: 5 событий' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Открыть событие «Событие 0»' }))
    expect(screen.getByRole('region', { name: 'Событие: Событие 0' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть превью события' }))
    expect(screen.getByRole('group', { name: 'Кластер: 5 событий' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Следующие события, страница 1 из 2' }))
    expect(screen.getByRole('button', { name: 'Открыть событие «Событие 4»' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть кластер' }))
    expect(screen.queryByRole('group', { name: 'Кластер: 5 событий' })).not.toBeInTheDocument()
    expect(setLocation).not.toHaveBeenCalled()
  })

  it('opens a server cluster at maximum zoom without another request', async () => {
    const members = ['a', 'b'].map((id) => ({ kind: 'event' as const, id, longitude: 37.61, latitude: 55.75, event: event(id, `Событие ${id}`) }))
    const cluster = { kind: 'cluster', id: 'stale', longitude: 37.61, latitude: 55.75, west: 37.6, south: 55.74, east: 37.62, north: 55.76, count: 2, members }
    getMapEvents.mockResolvedValue([cluster])
    render(<MemoryRouter><CatalogMap filters={{}} /></MemoryRouter>)
    await waitFor(() => expect(listenerRef.current).toBeTypeOf('function'))
    listenerRef.current?.({ location: { center: [37.61, 55.75], zoom: 22 }, mapInAction: false })
    const staleMarker = await screen.findByRole('button', { name: 'Показать 2 событий' }, { timeout: 2500 })
    await waitFor(() => expect(getMapEvents.mock.calls.filter(([params]) => params.zoom === 22)).toHaveLength(1))
    fireEvent.click(staleMarker)
    expect(await screen.findByRole('group', { name: 'Кластер: 2 событий' })).toBeInTheDocument()
    expect(getMapEvents.mock.calls.filter(([params]) => params.zoom === 22)).toHaveLength(1)
  })

  it('pages through a large coincident group on a mobile-width map so every event can be selected', async () => {
    const events = Array.from({ length: 19 }, (_, index) => event(`event-${index + 1}`, `Событие ${index + 1}`))
    events.push({ ...event('event-20', 'Событие 20'), longitude: 37.61002 })
    const cluster = { kind: 'cluster', id: 'cluster-1', longitude: 37.61, latitude: 55.75, west: 37.6, south: 55.74, east: 37.62, north: 55.76, count: 3, members: [] }
    getMapEvents.mockImplementation(async (params) => params.zoom === 22
      ? events.map((item) => ({ kind: 'event' as const, id: item.id, longitude: item.longitude ?? 37.61, latitude: item.latitude ?? 55.75, event: item }))
      : [cluster])
    mapSize.width = 375
    mapSize.height = 760
    render(<MemoryRouter><CatalogMap filters={{}} /></MemoryRouter>)
    await waitFor(() => expect(listenerRef.current).toBeTypeOf('function'))
    listenerRef.current?.({ location: { center: [37.61, 55.75], zoom: 22 }, mapInAction: false })
    await screen.findByRole('button', { name: 'Показать 20 событий в этой точке' }, { timeout: 2500 })
    await new Promise((resolve) => setTimeout(resolve, 400))
    const overlap = screen.getByRole('button', { name: 'Показать 20 событий в этой точке' })
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('1/5')
    const firstEvent = screen.getByRole('button', { name: 'Открыть событие «Событие 1»' })
    expect(Math.abs(Number.parseFloat(firstEvent.style.left))).toBeLessThanOrEqual(80)
    expect(Math.abs(Number.parseFloat(firstEvent.style.top))).toBeLessThanOrEqual(80)
    fireEvent.click(firstEvent)
    expect(screen.getByRole('region', { name: 'Событие: Событие 1' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть превью события' }))
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть кластер' }))
    expect(overlap).toHaveTextContent('20')
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('1/5')
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('2/5')
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('3/5')
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('4/5')
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('5/5')
    const lastEvent = screen.getByRole('button', { name: 'Открыть событие «Событие 20»' })
    expect(Math.abs(Number.parseFloat(lastEvent.style.left))).toBeLessThanOrEqual(160)
    expect(Math.abs(Number.parseFloat(lastEvent.style.top))).toBeLessThanOrEqual(80)
    fireEvent.click(lastEvent)
    expect(screen.getByRole('region', { name: 'Событие: Событие 20' })).toBeInTheDocument()
    fireEvent.click(overlap)
    expect(overlap).toHaveTextContent('20')
  })

  it('groups diagonally overlapping square markers at maximum zoom', async () => {
    const near = { ...event('near', 'Близкое событие'), longitude: 37.61002, latitude: 55.750011 }
    getMapEvents.mockImplementation(async (params) => params.zoom === 22 ? [
      { kind: 'event', id: 'a', longitude: 37.61, latitude: 55.75, event: event('a', 'Событие A') },
      { kind: 'event', id: 'near', longitude: near.longitude, latitude: near.latitude, event: near },
    ] : [])
    render(<MemoryRouter><CatalogMap filters={{}} /></MemoryRouter>)
    await waitFor(() => expect(listenerRef.current).toBeTypeOf('function'))
    listenerRef.current?.({ location: { center: [37.61, 55.75], zoom: 22 }, mapInAction: false })
    expect(await screen.findByRole('button', { name: 'Показать 2 событий в этой точке' }, { timeout: 2500 })).toBeInTheDocument()
  })

  it('shows the user location and sends the 10 km filter in map requests', async () => {
    render(<MemoryRouter><CatalogMap filters={{ lat: 55.75, lng: 37.61, distance_m: 10_000 }} initialCenter={[37.61, 55.75]} userLocation={{ latitude: 55.75, longitude: 37.61 }} /></MemoryRouter>)
    expect(await screen.findByRole('img', { name: 'Моё местоположение' })).toBeInTheDocument()
    await waitFor(() => expect(getMapEvents).toHaveBeenCalledWith(expect.objectContaining({ lat: 55.75, lng: 37.61, distance_m: 10_000 })))
  })
})
