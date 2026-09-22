import type { EventDetail } from '../../shared/api/types'
import { eventImage } from '../../shared/lib/events'
import styles from './catalogMap.module.css'

export function createEventMarkerElement(event: Pick<EventDetail, 'title' | 'imageUrl' | 'category_slug'>, onOpen: () => void) {
  const marker = document.createElement('button')
  marker.type = 'button'
  marker.className = styles.mapMarker
  marker.setAttribute('aria-label', `Открыть событие «${event.title}»`)
  marker.dataset.active = 'false'

  const image = document.createElement('img')
  image.className = styles.markerImage
  image.src = eventImage(event.imageUrl, event.category_slug)
  image.alt = ''
  image.width = 48
  image.height = 48

  const tooltip = document.createElement('span')
  tooltip.className = styles.markerTooltip
  tooltip.setAttribute('aria-hidden', 'true')
  tooltip.textContent = event.title

  marker.append(image, tooltip)
  marker.addEventListener('click', onOpen)
  return marker
}
