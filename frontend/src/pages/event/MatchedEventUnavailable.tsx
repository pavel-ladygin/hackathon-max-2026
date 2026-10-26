import { useNavigate } from 'react-router-dom'
import { matchedEventDateLabel, type MatchedEventPreview } from '../../features/discovery/matchedEventPreview'
import { Button, PageContent, PageShell, TopBar } from '../../shared/ui'
import pageStyles from '../pages.module.css'
import styles from './matched-event-unavailable.module.css'

export function MatchedEventUnavailable({ event, started }: { event: MatchedEventPreview; started: boolean }) {
  const navigate = useNavigate()
  const returnToMatches = () => navigate('/saved?tab=matches')
  return <PageShell>
    <TopBar title="Событие" onBack={returnToMatches} />
    <PageContent className={`${pageStyles.narrow} ${styles.content}`}>
      <div className={styles.icon} aria-hidden="true"><svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" focusable="false"><circle cx="12" cy="12" r="9" /><path d="M12 7v5h4" /></svg></div>
      <h1 className={`${pageStyles.title} ${styles.title}`}>{started ? 'Мероприятие уже началось или завершилось' : 'Событие больше недоступно'}</h1>
      <p className={pageStyles.subtitle}>Этот мэтч остаётся в вашей истории</p>
      <div className={styles.summary} role="group" aria-label="Информация о событии">
        <strong>{event.title}</strong>
        <p><time dateTime={event.starts_at}>{matchedEventDateLabel(event)}</time></p>
        {event.venue_name ? <p>{event.venue_name}</p> : null}
      </div>
      <Button onClick={returnToMatches}>Вернуться к мэтчам</Button>
    </PageContent>
  </PageShell>
}
