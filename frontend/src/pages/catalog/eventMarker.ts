import type { EventDetail } from '../../shared/api/types'
import { eventImage, eventImageFallback } from '../../shared/lib/events'
import styles from './catalogMap.module.css'

export function createEventMarkerElement(event: Pick<EventDetail, 'title' | 'imageUrl' | 'category_slug'>, onOpen: () => void, entranceDelay = 0) {
  const marker = document.createElement('button')
  marker.type = 'button'
  marker.className = styles.mapMarker
  marker.setAttribute('aria-label', `Открыть событие «${event.title}»`)
  marker.setAttribute('aria-pressed', 'false')
  marker.dataset.active = 'false'
  marker.style.setProperty('--marker-delay', `${Math.min(120, Math.max(0, entranceDelay))}ms`)

  const image = document.createElement('img')
  image.className = styles.markerImage
  const fallbackSrc = eventImageFallback(event.category_slug)
  image.src = eventImage(event.imageUrl, event.category_slug)
  image.alt = ''
  image.width = 48
  image.height = 48
  image.addEventListener('error', () => {
    if (image.dataset.fallbackApplied === 'true') {
      image.hidden = true
      return
    }
    image.dataset.fallbackApplied = 'true'
    image.src = fallbackSrc
  })

  const tooltip = document.createElement('span')
  tooltip.className = styles.markerTooltip
  tooltip.setAttribute('aria-hidden', 'true')
  tooltip.textContent = event.title

  marker.append(image, tooltip)
  marker.addEventListener('click', onOpen)
  return marker
}
