import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { EventCard } from '../../shared/api/types'
import { CatalogMap } from './CatalogMap'

const { navigate, pages, FakeYMap, FakeYMapMarker } = vi.hoisted(() => {
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
    destroy() { this.container.replaceChildren() }
  }
  return { navigate: vi.fn(), pages: [] as Array<{ items: EventCard[]; nextCursor?: string }>, FakeYMap: Map, FakeYMapMarker: Marker }
})

vi.mock('@tanstack/react-query', () => ({
  useInfiniteQuery: () => ({
    data: { pages },
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
      YMapDefaultFeaturesLayer: class {}, YMapDefaultSchemeLayer: class {}, YMapListener: class {},
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
    pages.splice(0, pages.length, { items: [event('a', 'Событие A'), event('b', 'Событие B')] })
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
})
