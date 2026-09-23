import { describe, expect, it } from 'vitest'
import { skeletonVariant } from './skeletonVariant'

describe('route skeleton variants', () => {
  it.each([
    ['/', 'home'],
    ['/events', 'cards'],
    ['/events/123', 'event'],
    ['/saved', 'cards'],
    ['/preferences', 'form'],
    ['/onboarding/interests', 'form'],
    ['/rooms/new', 'room'],
    ['/join/invite-token', 'room'],
    ['/open-in-max', 'generic'],
  ] as const)('maps %s to %s skeleton', (pathname, variant) => {
    expect(skeletonVariant(pathname)).toBe(variant)
  })
})
