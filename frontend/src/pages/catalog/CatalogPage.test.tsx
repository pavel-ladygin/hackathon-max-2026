import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CatalogPage } from './CatalogPage'

const searchCalls: Array<Record<string, unknown>> = []

vi.mock('../../features/discovery/queries', () => ({
  useEventSearch: (params: Record<string, unknown>) => {
    searchCalls.push(params)
    return {
      data: { pages: [{ items: [{ id: 'event-1', title: 'Jazz evening', imageUrl: null, date_label: 'Сегодня', venue_name: 'Club', price_label: 'Бесплатно', saved: false }], totalEstimate: 1 }] },
      isPending: false,
      isFetching: false,
      isFetchingNextPage: false,
      isError: false,
      hasNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
    }
  },
  useSetSavedEvent: () => ({ isPending: false, mutate: vi.fn() }),
}))

function renderCatalog() {
  return render(<MemoryRouter><CatalogPage /></MemoryRouter>)
}

describe('CatalogPage search', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    searchCalls.length = 0
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

  it('keeps active filters while changing the text query', () => {
    renderCatalog()
    fireEvent.click(screen.getByRole('button', { name: 'Концерты' }))
    const input = screen.getByRole('searchbox')
    fireEvent.change(input, { target: { value: 'gallery' } })

    act(() => { vi.advanceTimersByTime(300) })

    expect(searchCalls.at(-1)).toMatchObject({ q: 'gallery', category_slugs: ['concerts'] })
  })
})
