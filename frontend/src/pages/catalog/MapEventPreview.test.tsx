import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { EventCard } from '../../shared/api/types'
import { MapEventPreview } from './MapEventPreview'
import { createEventMarkerElement } from './eventMarker'

const event: EventCard = {
  id: 'event-1', title: 'Джазовый вечер', subtitle: null, category_slug: 'concerts',
  starts_at: '2026-09-24T19:00:00+03:00', timezone: 'Europe/Moscow', date_label: 'Завтра, 19:00',
  venue_name: 'Клуб «Север»', latitude: 55.75, longitude: 37.61,
  currency: 'RUB', price_label: 'Бесплатно', imageUrl: null, saved: false, reasons: [],
  distanceM: null, distanceLabel: null, priceFromMinor: 0,
}

describe('map event preview', () => {
  it('shows event summary and invokes close/details actions', () => {
    const onClose = vi.fn()
    const onDetails = vi.fn()
    render(<MapEventPreview event={event} onClose={onClose} onDetails={onDetails} />)

    expect(screen.getByRole('region', { name: 'Событие: Джазовый вечер' })).toBeInTheDocument()
    expect(screen.getByText('Завтра, 19:00')).toBeInTheDocument()
    expect(screen.getByText('Клуб «Север»')).toBeInTheDocument()
    expect(screen.getByText('Бесплатно')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть превью события' }))
    fireEvent.click(screen.getByRole('button', { name: 'Подробнее' }))
    expect(onClose).toHaveBeenCalledOnce()
    expect(onDetails).toHaveBeenCalledOnce()
  })

  it('marker selection calls the selection handler without navigating', () => {
    const onSelect = vi.fn()
    const marker = createEventMarkerElement(event, onSelect)
    expect(marker).toHaveAttribute('aria-pressed', 'false')
    marker.click()
    expect(onSelect).toHaveBeenCalledOnce()
    expect(marker).toHaveAttribute('aria-label', 'Открыть событие «Джазовый вечер»')
  })
})
