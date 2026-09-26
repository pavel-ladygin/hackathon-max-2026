import { fireEvent } from '@testing-library/dom'
import { afterEach, describe, expect, it } from 'vitest'
import { createEventMarkerElement } from './eventMarker'

describe('createEventMarkerElement image placeholder', () => {
  afterEach(() => document.body.replaceChildren())

  it('keeps a non-text loading skeleton until the image loads, then shows unavailable text on error', () => {
    const marker = createEventMarkerElement({
      title: 'Концерт',
      imageUrl: 'https://images.example.com/concert.jpg',
      category_slug: 'concerts',
    }, () => {})
    document.body.append(marker)
    const image = marker.querySelector('img')!
    const placeholder = marker.querySelector('span')!

    expect(image.src).toBe('https://images.example.com/concert.jpg')
    expect(placeholder).toBeEmptyDOMElement()
    expect(placeholder).toBeVisible()
    expect(placeholder.className).toContain('markerImagePlaceholder')
    expect(placeholder).not.toHaveTextContent('Загрузка фото')

    fireEvent.load(image)
    expect(placeholder).not.toBeVisible()

    fireEvent.error(image)
    expect(image.src).toBe('https://images.example.com/concert.jpg')
    expect(image.dataset.fallbackApplied).toBeUndefined()
    expect(image).not.toBeVisible()
    expect(placeholder).toHaveTextContent('Фото недоступно')
    expect(placeholder).toBeVisible()
  })

  it('starts in the visible placeholder state when an event has no image', () => {
    const marker = createEventMarkerElement({
      title: 'Выставка',
      imageUrl: null,
      category_slug: 'exhibitions',
    }, () => {})
    document.body.append(marker)
    const image = marker.querySelector('img')
    const placeholder = marker.querySelector('span')!

    expect(placeholder).toHaveTextContent('Фото недоступно')
    expect(placeholder).toBeVisible()
    expect(image).not.toBeVisible()
    expect(image).not.toHaveAttribute('src')
  })
})
