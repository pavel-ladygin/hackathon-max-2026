import { describe, expect, it } from 'vitest'
import { boundsContain, MapAreaCache, paddedViewportBounds } from './mapResults'

describe('map viewport bounds', () => {
  it('pads viewport bounds in Web Mercator and keeps latitude valid', () => {
    const bounds = paddedViewportBounds([37.6, 55.75], 11, 700, 500)
    expect(bounds.west).toBeLessThan(37.6)
    expect(bounds.east).toBeGreaterThan(37.6)
    expect(bounds.south).toBeLessThan(55.75)
    expect(bounds.north).toBeGreaterThan(55.75)
    expect(bounds.north).toBeLessThanOrEqual(85.05112878)
  })

  it('represents a viewport crossing the antimeridian with west greater than east', () => {
    const bounds = paddedViewportBounds([179.8, 0], 4, 700, 500)
    expect(bounds.west).toBeGreaterThan(bounds.east)
  })

  it('checks containment for ordinary and wrapped longitude intervals', () => {
    expect(boundsContain({ west: 10, east: 20, south: 10, north: 20 }, { west: 12, east: 18, south: 12, north: 18 })).toBe(true)
    expect(boundsContain({ west: 170, east: -170, south: -20, north: 20 }, { west: 175, east: -175, south: -5, north: 5 })).toBe(true)
    expect(boundsContain({ west: 170, east: -170, south: -20, north: 20 }, { west: 160, east: 175, south: -5, north: 5 })).toBe(false)
  })
})

describe('MapAreaCache', () => {
  it('reuses an enclosing area for the same key and expires it after five minutes', () => {
    const cache = new MapAreaCache<string>(20, 300_000)
    const outer = { west: 0, south: 0, east: 10, north: 10 }
    const inner = { west: 2, south: 2, east: 8, north: 8 }
    cache.set('filters\u000011', outer, 'area', 1_000)
    expect(cache.get('filters\u000011', inner, 299_000)).toBe('area')
    expect(cache.get('other-filters\u000011', inner, 299_000)).toBeUndefined()
    expect(cache.get('filters\u000011', inner, 301_001)).toBeUndefined()
  })

  it('evicts the least recently used region after the configured capacity', () => {
    const cache = new MapAreaCache<number>(2, 300_000)
    cache.set('x', { west: 0, south: 0, east: 10, north: 10 }, 1, 1)
    cache.set('x', { west: 20, south: 0, east: 30, north: 10 }, 2, 2)
    cache.get('x', { west: 21, south: 1, east: 29, north: 9 }, 3)
    cache.set('x', { west: 40, south: 0, east: 50, north: 10 }, 3, 4)
    expect(cache.get('x', { west: 1, south: 1, east: 9, north: 9 }, 5)).toBeUndefined()
  })
})
