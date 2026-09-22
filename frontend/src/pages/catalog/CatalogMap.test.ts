import { fireEvent } from '@testing-library/dom'
import { describe, expect, it, vi } from 'vitest'
import { createEventMarkerElement } from './eventMarker'

describe('event map marker', () => {
  it('renders an accessible image preview and opens the event', () => {
    const onOpen = vi.fn()
    const marker = createEventMarkerElement({ title: 'Очень длинное название события', imageUrl: null, category_slug: 'concerts' }, onOpen)

    expect(marker).toHaveAttribute('aria-label', 'Открыть событие «Очень длинное название события»')
    expect(marker.querySelector('img')).toMatchObject({ alt: '', width: 48, height: 48 })
    expect(marker.querySelector('[aria-hidden="true"]')).toHaveTextContent('Очень длинное название события')

    fireEvent.click(marker)
    expect(onOpen).toHaveBeenCalledOnce()
  })

  it('caps marker entrance delay so dense maps settle quickly', () => {
    const marker = createEventMarkerElement({ title: 'Event', imageUrl: null, category_slug: 'concerts' }, vi.fn(), 240)

    expect(marker.style.getPropertyValue('--marker-delay')).toBe('120ms')
  })
})
