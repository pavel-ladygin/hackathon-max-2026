import type { EventCard } from '../../shared/api/types'
import { eventImage, eventImageFallback } from '../../shared/lib/events'
import { Button, EventImage } from '../../shared/ui'
import styles from './catalogMap.module.css'

export function MapEventPreview({ event, onClose, onDetails }: { event: EventCard; onClose: () => void; onDetails: () => void }) {
  const fallbackImage = eventImageFallback(event.category_slug)
  return <section className={styles.preview} aria-label={`Событие: ${event.title}`}>
    <button className={styles.previewClose} type="button" aria-label="Закрыть превью события" onClick={onClose}>×</button>
    <button className={styles.previewImageButton} type="button" aria-label={`Открыть событие «${event.title}» по фото`} onClick={onDetails}>
      <EventImage className={styles.previewImage} src={eventImage(event.imageUrl, event.category_slug)} fallbackSrc={fallbackImage} alt="" />
    </button>
    <div className={styles.previewContent}>
      <h2 className={styles.previewTitle}>
        <button className={styles.previewTitleButton} type="button" aria-label={`Открыть событие «${event.title}» по заголовку`} onClick={onDetails}>{event.title}</button>
      </h2>
      <p className={styles.previewMeta}>{event.date_label}</p>
      <p className={styles.previewMeta}>{event.venue_name}</p>
      <p className={styles.previewPrice}>{event.price_label}</p>
      <Button onClick={onDetails}>Подробнее</Button>
    </div>
  </section>
}
