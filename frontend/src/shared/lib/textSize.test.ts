import { beforeEach, describe, expect, it, vi } from 'vitest'
import { initializeTextSize, readTextSize, setTextSize } from './textSize'

describe('text size preference', () => {
  beforeEach(() => { localStorage.clear(); document.documentElement.style.removeProperty('--font-scale') })
  it('uses normal for missing or invalid stored values', () => {
    expect(readTextSize()).toBe('normal')
    localStorage.setItem('max-text-size', 'huge')
    expect(readTextSize()).toBe('normal')
  })
  it('restores saved scale before rendering and persists changes', () => {
    localStorage.setItem('max-text-size', 'large')
    initializeTextSize()
    expect(document.documentElement.style.getPropertyValue('--font-scale')).toBe('1.15')
    setTextSize('compact')
    expect(localStorage.getItem('max-text-size')).toBe('compact')
    expect(document.documentElement.style.getPropertyValue('--font-scale')).toBe('.9')
  })
  it('falls back safely when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    expect(readTextSize()).toBe('normal')
    expect(() => setTextSize('large')).not.toThrow()
    vi.restoreAllMocks()
  })
})
