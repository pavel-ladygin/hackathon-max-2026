export type SwipeDirection = 'like' | 'dislike' | null

export function resolveSwipeIntent(offsetX: number, velocityX = 0, threshold = 90, velocityThreshold = 650): SwipeDirection {
  if (offsetX >= threshold || velocityX >= velocityThreshold) return 'like'
  if (offsetX <= -threshold || velocityX <= -velocityThreshold) return 'dislike'
  return null
}

export function participantInitials(name: string): string {
  return name.trim().split(/\s+/).map((part) => part[0]).filter(Boolean).slice(0, 2).join('').toUpperCase() || '?'
}
