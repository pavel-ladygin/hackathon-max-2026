import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CatalogPage } from './CatalogPage'
import { maxPlatform } from '../../shared/platform/max/adapter'

const searchCalls: Array<Record<string, unknown>> = []
const defaultResult = {
  data: { pages: [{ items: [{ id: 'event-1', title: 'Jazz evening', imageUrl: null, category_slug: 'concerts' as const, date_label: 'Сегодня', venue_name: 'Club', price_label: 'Бесплатно', saved: false }], totalEstimate: 1 }] },
  isPending: false,
  isFetching: false,
  isFetchingNextPage: false,
  isError: false,
  hasNextPage: false,
  fetchNextPage: vi.fn(),
  refetch: vi.fn(),
}
let resultState: Omit<typeof defaultResult, 'data'> & { data: typeof defaultResult.data | undefined } = defaultResult

vi.mock('../../features/discovery/queries', () => ({
  useEventSearch: (params: Record<string, unknown>) => {
    searchCalls.push(params)
    return resultState
  },
  useSetSavedEvent: () => ({ isPending: false, mutate: vi.fn() }),
}))

vi.mock('./CatalogMap', () => ({ CatalogMap: ({ filters, userLocation }: { filters: Record<string, unknown>; userLocation?: { latitude: number; longitude: number } }) => <div aria-label="map-placeholder" data-filters={JSON.stringify(filters)} data-user-location={JSON.stringify(userLocation)} /> }))

function renderCatalog() {
  return render(<MemoryRouter><CatalogPage /></MemoryRouter>)
}

describe('CatalogPage search', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    searchCalls.length = 0
    resultState = defaultResult
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('debounces and trims search text before passing q to the query', () => {
    renderCatalog()
    const input = screen.getByRole('searchbox')

    fireEvent.change(input, { target: { value: '  jazz  ' } })
    expect(searchCalls.at(-1)?.q).toBeUndefined()

    act(() => { vi.advanceTimersByTime(299) })
    expect(searchCalls.at(-1)?.q).toBeUndefined()

    act(() => { vi.advanceTimersByTime(1) })
    expect(searchCalls.at(-1)?.q).toBe('jazz')
  })

  it('only searches for the final value after rapid typing and searches again when cleared', () => {
    renderCatalog()
    const input = screen.getByRole('searchbox')

    fireEvent.change(input, { target: { value: 'j' } })
    act(() => { vi.advanceTimersByTime(100) })
    fireEvent.change(input, { target: { value: 'ja' } })
    act(() => { vi.advanceTimersByTime(100) })
    fireEvent.change(input, { target: { value: 'jaz' } })
    act(() => { vi.advanceTimersByTime(299) })
    expect(searchCalls.at(-1)?.q).toBeUndefined()

    act(() => { vi.advanceTimersByTime(1) })
    expect(searchCalls.at(-1)?.q).toBe('jaz')

    fireEvent.change(input, { target: { value: '' } })
    act(() => { vi.advanceTimersByTime(300) })
    expect(searchCalls.at(-1)?.q).toBeUndefined()
  })

  it('shows an accessible clear button only for a nonempty search and clears the query', () => {
    renderCatalog()
    const input = screen.getByRole('searchbox')
    expect(screen.queryByRole('button', { name: 'Очистить поиск' })).not.toBeInTheDocument()

    fireEvent.change(input, { target: { value: 'gallery' } })
    fireEvent.click(screen.getByRole('button', { name: 'Очистить поиск' }))

    expect(input).toHaveValue('')
    expect(screen.queryByRole('button', { name: 'Очистить поиск' })).not.toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(300) })
    expect(searchCalls.at(-1)?.q).toBeUndefined()
  })

  it('keeps active filters while changing the text query', () => {
    renderCatalog()
    fireEvent.click(screen.getByRole('button', { name: /Фильтры/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Концерты' }))
    const input = screen.getByRole('searchbox')
    fireEvent.change(input, { target: { value: 'gallery' } })

    act(() => { vi.advanceTimersByTime(300) })

    expect(searchCalls.at(-1)).toMatchObject({ q: 'gallery', category_slugs: ['concerts'] })
  })

  it('opens advanced filters and exposes the active filter count', () => {
    renderCatalog()
    const filters = screen.getByRole('button', { name: 'Фильтры' })
    expect(filters).toHaveAttribute('aria-expanded', 'false')
    expect(filters).toHaveAttribute('aria-controls', 'catalog-advanced-filters')
    expect(screen.queryByRole('group', { name: 'Категории' })).not.toBeInTheDocument()

    fireEvent.click(filters)
    expect(filters).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(screen.getByRole('button', { name: 'Концерты' }))
    expect(screen.getByRole('button', { name: 'Фильтры · 1' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Очистить фильтры' })).toBeInTheDocument()
  })

  it('resets all filters while preserving the search query', () => {
    renderCatalog()
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'jazz' } })
    act(() => { vi.advanceTimersByTime(300) })
    fireEvent.click(screen.getByRole('button', { name: 'Фильтры' }))
    fireEvent.click(screen.getByRole('button', { name: 'Концерты' }))
    fireEvent.click(screen.getByRole('button', { name: 'Очистить фильтры' }))

    expect(screen.getByRole('searchbox')).toHaveValue('jazz')
    expect(screen.getByRole('button', { name: 'Фильтры' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Очистить фильтры' })).not.toBeInTheDocument()
    expect(searchCalls.at(-1)).toMatchObject({ q: 'jazz', category_slugs: undefined, free_only: undefined, date_from: undefined, date_to: undefined, price_max_minor: undefined, distance_m: undefined })
  })

  it('renders event skeleton cards during the initial load', () => {
    resultState = { ...defaultResult, data: undefined, isPending: true }
    const { container } = renderCatalog()

    expect(screen.getByRole('status', { name: 'Загружаем события…' })).toHaveAttribute('aria-busy', 'true')
    expect(container.querySelectorAll('[class*="screenCard_"]').length).toBe(4)
    expect(screen.queryByText('Jazz evening')).not.toBeInTheDocument()
  })

  it('announces a subtle refresh while previous results remain visible', () => {
    resultState = { ...defaultResult, isFetching: true }
    renderCatalog()

    expect(screen.getByText('Обновляем результаты…')).toBeInTheDocument()
    expect(screen.getByText('Jazz evening')).toBeInTheDocument()
  })

  it('reserves room for the favorite control inside each event card', () => {
    renderCatalog()

    const card = screen.getAllByRole('button', { name: /Jazz evening/ }).find((button) => button.className.includes('eventCardWithSave'))
    if (!card) throw new Error('Event card does not reserve space for its favorite control')
    expect(card.className).toContain('eventCardWithSave')
    expect(card.querySelector('strong')).toHaveTextContent('Jazz evening')
  })

  it('shows pagination only in list view', async () => {
    resultState = { ...defaultResult, hasNextPage: true }
    renderCatalog()

    expect(screen.getByRole('button', { name: 'Показать ещё' })).toBeInTheDocument()
    await act(async () => {
      fireEvent.click(screen.getByRole('tab', { name: 'Карта' }))
      await Promise.resolve()
    })

    expect(screen.getByLabelText('map-placeholder')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Показать ещё' })).not.toBeInTheDocument()
  })

  it('explains a denied location permission without exposing coordinates', async () => {
    vi.spyOn(maxPlatform, 'requestLocation').mockResolvedValue({ ok: false, reason: 'permission_denied' })
    renderCatalog()

    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Найти рядом' })) })

    expect(screen.getByRole('status')).toHaveTextContent('Доступ к геолокации запрещён')
    expect(screen.queryByRole('button', { name: 'Повторить' })).not.toBeInTheDocument()
    expect(searchCalls.at(-1)).toMatchObject({ lat: undefined, lng: undefined })
  })

  it('offers a retry for a timeout and clears the error when a later attempt succeeds', async () => {
    const requestLocation = vi.spyOn(maxPlatform, 'requestLocation')
      .mockResolvedValueOnce({ ok: false, reason: 'timeout' })
      .mockResolvedValueOnce({ ok: true, position: { lat: 55.75, lng: 37.61, accuracyM: 30 } })
    renderCatalog()

    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Найти рядом' })) })
    expect(screen.getByRole('status')).toHaveTextContent('Попробуйте ещё раз')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Повторить' })) })

    expect(requestLocation).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Рядом · 10 км' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('keeps the current view and applies the 10 km radius and user marker to the map after locating', async () => {
    vi.spyOn(maxPlatform, 'requestLocation').mockResolvedValue({ ok: true, position: { lat: 55.75, lng: 37.61, accuracyM: 30 } })
    renderCatalog()

    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Найти рядом' })) })

    expect(screen.getByRole('tab', { name: 'Список' })).toHaveAttribute('aria-selected', 'true')
    expect(searchCalls.at(-1)).toMatchObject({ lat: 55.75, lng: 37.61, distance_m: 10_000 })

    await act(async () => { fireEvent.click(screen.getByRole('tab', { name: 'Карта' })) })
    const map = screen.getByLabelText('map-placeholder')
    expect(JSON.parse(map.getAttribute('data-filters') ?? '{}')).toMatchObject({ lat: 55.75, lng: 37.61, distance_m: 10_000 })
    expect(JSON.parse(map.getAttribute('data-user-location') ?? 'null')).toEqual({ latitude: 55.75, longitude: 37.61 })
  })
})
