import { describe, expect, it } from 'vitest'
import { markerSizeForZoom } from './yandexMaps'

describe('markerSizeForZoom', () => {
  it('follows zoom while staying within marker bounds', () => {
    expect(markerSizeForZoom(11)).toBe(48)
    expect(markerSizeForZoom(12)).toBe(52)
    expect(markerSizeForZoom(13)).toBe(56)
    expect(markerSizeForZoom(1)).toBe(36)
    expect(markerSizeForZoom(30)).toBe(64)
  })

  it('is monotonic as zoom increases', () => {
    const sizes = [8, 10, 11, 12, 16].map(markerSizeForZoom)
    expect(sizes).toEqual([...sizes].sort((a, b) => a - b))
  })
})
