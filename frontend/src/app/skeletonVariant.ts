import type { ScreenSkeletonVariant } from '../shared/ui'

export function skeletonVariant(pathname: string): ScreenSkeletonVariant {
  if (/^\/events\/[^/]+\/?$/.test(pathname)) return 'event'
  if (pathname === '/events' || pathname === '/saved') return 'cards'
  if (pathname === '/preferences' || pathname.startsWith('/onboarding')) return 'form'
  if (pathname.startsWith('/rooms') || pathname.startsWith('/join')) return 'room'
  if (pathname === '/') return 'home'
  return 'generic'
}
