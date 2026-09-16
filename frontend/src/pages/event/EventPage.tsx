import { useMutation } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useEventDetail } from '../../features/discovery/queries'
import { apiClient } from '../../shared/api/client'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, Empty, Loading, PageContent, PageShell, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'

export function EventPage() {
  const { eventId } = useParams()
  const navigate = useNavigate()
  const event = useEventDetail(eventId)
  const behaviorId = useRef(crypto.randomUUID())
  const ticket = useMutation({
    mutationFn: () => apiClient.recordTicketClick(eventId!, { source: 'event_detail' }),
    onSuccess: ({ external_url }) => void maxPlatform.openExternalLink(external_url),
  })

  useEffect(() => {
    if (!event.data) return
    void apiClient.recordBehavior([{ client_event_id: behaviorId.current, type: 'open', occurred_at: new Date().toISOString(), event_id: event.data.id, metadata: { surface: 'event_detail' } }])
  }, [event.data])

  if (event.isPending) return <Loading label="Открываем событие…" />
  if (event.isError) return <Empty title="Событие не найдено" action={<Button onClick={() => navigate('/')}>Вернуться в афишу</Button>} />

  const item = event.data
  return (
    <PageShell>
      <TopBar title="Событие" onBack={() => navigate(-1)} />
      <img className={styles.detailHero} src={item.imageUrl ?? '/events/concert-singer.png'} alt={item.title} />
      <PageContent className={styles.narrow}>
        <p className={styles.eyebrow}>{item.category_slug}</p>
        <h1 className={styles.title}>{item.title}</h1>
        <p className={styles.subtitle}>{item.subtitle}</p>
        <div className={styles.detailMeta}><span>◷ {item.date_label}</span><span>⌖ {item.venue_name}<small> · {item.venue.address}</small></span><span>₽ {item.price_label}</span></div>
        <section className={styles.section}><h2 className={styles.sectionTitle}>О событии</h2><p className={styles.bodyCopy}>{item.description}</p></section>
        <div className={styles.explain}><strong>Почему вам подходит</strong>{item.reasons.map((reason) => <span key={reason.code}>✓ {reason.text}</span>)}</div>
        {ticket.isError ? <p className={styles.error}>Не удалось открыть билетный сервис.</p> : null}
        <div className={styles.footer}><Button disabled={!item.ticketAvailable || ticket.isPending} onClick={() => ticket.mutate()}>{ticket.isPending ? 'Открываем…' : 'К билетам'}</Button></div>
      </PageContent>
    </PageShell>
  )
}
