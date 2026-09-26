import { useEffect, useRef, type ReactNode } from 'react'
import type { EventCard } from '../api/types'
import { track } from './client'

export function EventImpression({ event, listType, position, className, children }: {
  event: EventCard
  listType: string
  position: number
  className?: string
  children: ReactNode
}) {
  const element = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const node = element.current
    if (!node || typeof IntersectionObserver === 'undefined') return
    let recorded = false
    const observer = new IntersectionObserver(([entry]) => {
      if (recorded || !entry.isIntersecting || entry.intersectionRatio < 0.5) return
      recorded = true
      track('event_impression', { eventId: event.id, properties: {
        position,
        list_type: listType,
        category: event.category_slug,
        has_image: Boolean(event.imageUrl),
        has_price: event.priceFromMinor !== null,
      } })
      observer.disconnect()
    }, { threshold: 0.5 })
    observer.observe(node)
    return () => observer.disconnect()
  }, [event.id, event.category_slug, event.imageUrl, event.priceFromMinor, listType, position])
  return <div ref={element} className={className}>{children}</div>
}
