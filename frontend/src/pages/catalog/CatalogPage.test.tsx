import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CatalogPage } from './CatalogPage'

const searchCalls: Array<Record<string, unknown>> = []
const countCalls: Array<{ params: Record<string, unknown>; enabled: boolean }> = []
const defaultResult = {
  data: { pages: [{ items: [{ id: 'event-1', title: 'Jazz evening', imageUrl: null, category_slug: 'concerts' as const, date_label: 'Сегодня', venue_name: 'Club', price_label: 'Бесплатно', saved: false }], totalEstimate: null as number | null }] },
  isPending: false,
  isFetching: false,
  isFetchingNextPage: false,
  isError: false,
  hasNextPage: false,
  fetchNextPage: vi.fn(),
  refetch: vi.fn(),
}
let resultState: Omit<typeof defaultResult, 'data'> & { data: typeof defaultResult.data | undefined } = defaultResult
let countState: { data: number | undefined; isSuccess: boolean } = { data: 1, isSuccess: true }

vi.mock('../../features/discovery/queries', () => ({
  useEventSearch: (params: Record<string, unknown>) => {
    searchCalls.push(params)
    return resultState
  },
  useEventSearchCount: (params: Record<string, unknown>, enabled: boolean) => {
    countCalls.push({ params, enabled })
    return countState
  },
  useSetSavedEvent: () => ({ isPending: false, mutate: vi.fn() }),
}))

vi.mock('./CatalogMap', () => ({ CatalogMap: ({ filters }: { filters: Record<string, unknown> }) => <div aria-label="map-placeholder" data-filters={JSON.stringify(filters)} /> }))

function renderCatalog() {
  return render(<MemoryRouter><CatalogPage /></MemoryRouter>)
}

describe('CatalogPage search', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    searchCalls.length = 0
    countCalls.length = 0
    resultState = defaultResult
    countState = { data: 1, isSuccess: true }
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows all API categories in the catalog filters', () => {
    renderCatalog()
    fireEvent.click(screen.getByRole('button', { name: 'Фильтры' }))
    const group = screen.getByRole('group', { name: 'Категории' })
    for (const label of ['Концерты', 'Кино', 'Театр', 'Стендап', 'Выставки', 'Спорт', 'Еда', 'Вечеринки', 'Фестивали', 'Прогулки', 'Другое']) {
      expect(within(group).getByRole('button', { name: label })).toBeInTheDocument()
    }
  })

  it('shows cards before the separate exact count finishes', () => {
    resultState = { ...defaultResult, data: { pages: [{ ...defaultResult.data.pages[0], totalEstimate: null }] } }
    countState = { data: undefined, isSuccess: false }
    const view = renderCatalog()

    expect(screen.getByText('Jazz evening')).toBeInTheDocument()
    expect(screen.queryByText(/найдено/)).not.toBeInTheDocument()
    expect(countCalls.at(-1)?.enabled).toBe(true)

    countState = { data: 42, isSuccess: true }
    view.rerender(<MemoryRouter><CatalogPage /></MemoryRouter>)
    expect(screen.getByText('42 найдено')).toBeInTheDocument()
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
    expect(searchCalls.at(-1)).toMatchObject({ q: 'jazz', category_slugs: undefined, free_only: undefined, date_from: undefined, date_to: undefined, price_max_minor: undefined })
    expect(searchCalls.at(-1)).not.toHaveProperty('distance_m')
    expect(searchCalls.at(-1)).not.toHaveProperty('lat')
    expect(searchCalls.at(-1)).not.toHaveProperty('lng')
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

  it('does not request location and keeps map available without geographic filters', async () => {
    renderCatalog()

    expect(screen.queryByRole('button', { name: 'Найти рядом' })).not.toBeInTheDocument()
    expect(searchCalls.at(-1)).not.toHaveProperty('distance_m')
    expect(searchCalls.at(-1)).not.toHaveProperty('lat')
    expect(searchCalls.at(-1)).not.toHaveProperty('lng')

    fireEvent.click(screen.getByRole('tab', { name: 'Карта' }))
    const map = screen.getByLabelText('map-placeholder')
    const filters = JSON.parse(map.getAttribute('data-filters') ?? '{}') as Record<string, unknown>
    expect(filters).not.toHaveProperty('distance_m')
    expect(filters).not.toHaveProperty('lat')
    expect(filters).not.toHaveProperty('lng')
  })
})
