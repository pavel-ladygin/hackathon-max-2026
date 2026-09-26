import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { EventCard } from '../../shared/api/types'
import { eventImageFallback } from '../../shared/lib/events'
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
  it('keeps the shimmer over a remote photo until it fails, then uses the neutral fallback', () => {
    const imageFallback = eventImageFallback('concerts')
    render(<MapEventPreview event={{ ...event, imageUrl: 'https://images.example.com/jazz.jpg' }} onClose={vi.fn()} onDetails={vi.fn()} />)

    const image = document.querySelector('img')!
    expect(image).toHaveAttribute('src', 'https://images.example.com/jazz.jpg')
    expect(image.className).toContain('eventImageLoading')
    expect(image.style.backgroundImage).toBe('')

    fireEvent.error(image)
    expect(image).toHaveAttribute('src', imageFallback)
  })

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

  it('opens details from the photo, title, and Подробнее button', () => {
    const onDetails = vi.fn()
    render(<MapEventPreview event={event} onClose={vi.fn()} onDetails={onDetails} />)

    fireEvent.click(screen.getByRole('button', { name: 'Открыть событие «Джазовый вечер» по фото' }))
    fireEvent.click(screen.getByRole('button', { name: 'Открыть событие «Джазовый вечер» по заголовку' }))
    fireEvent.click(screen.getByRole('button', { name: 'Подробнее' }))

    expect(onDetails).toHaveBeenCalledTimes(3)
  })

  it('supports keyboard activation of the photo and title buttons', async () => {
    const user = userEvent.setup()
    const onDetails = vi.fn()
    render(<MapEventPreview event={event} onClose={vi.fn()} onDetails={onDetails} />)
    const photoLink = screen.getByRole('button', { name: 'Открыть событие «Джазовый вечер» по фото' })
    const titleLink = screen.getByRole('button', { name: 'Открыть событие «Джазовый вечер» по заголовку' })

    photoLink.focus()
    expect(photoLink).toHaveFocus()
    await user.keyboard('{Enter}')
    titleLink.focus()
    expect(titleLink).toHaveFocus()
    await user.keyboard(' ')

    expect(onDetails).toHaveBeenCalledTimes(2)
  })

  it('closes without invoking details', () => {
    const onClose = vi.fn()
    const onDetails = vi.fn()
    render(<MapEventPreview event={event} onClose={onClose} onDetails={onDetails} />)

    fireEvent.click(screen.getByRole('button', { name: 'Закрыть превью события' }))

    expect(onClose).toHaveBeenCalledOnce()
    expect(onDetails).not.toHaveBeenCalled()
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
