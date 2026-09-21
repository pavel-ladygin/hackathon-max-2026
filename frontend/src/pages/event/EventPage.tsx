import { useMutation } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useEventDetail, useSetSavedEvent } from '../../features/discovery/queries'
import { apiClient } from '../../shared/api/client'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, Empty, InlineNotice, Loading, PageContent, PageShell, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'

export function EventPage() {
  const { eventId } = useParams()
  const navigate = useNavigate()
  const event = useEventDetail(eventId)
  const save = useSetSavedEvent()
  const behaviorId = useRef(crypto.randomUUID())
  const ticket = useMutation({
    mutationFn: () => apiClient.recordTicketClick(eventId!, { source: 'event_detail' }),
    onSuccess: ({ external_url }) => void maxPlatform.openTicketLink(external_url),
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
      <TopBar title="Событие" onBack={() => navigate(-1)} right={event.data ? <button type="button" className={styles.saveButton} aria-label={event.data.saved ? 'Убрать из сохранённых' : 'Сохранить событие'} aria-pressed={event.data.saved} disabled={save.isPending} onClick={() => save.mutate({ eventId: event.data.id, saved: !event.data.saved })}>{event.data.saved ? '♥' : '♡'}</button> : null} />
      <img className={styles.detailHero} src={item.imageUrl ?? '/events/concert-singer.png'} alt={item.title} />
      <PageContent className={styles.narrow}>
        <p className={styles.eyebrow}>{item.category_slug}</p>
        <h1 className={styles.title}>{item.title}</h1>
        <p className={styles.subtitle}>{item.subtitle}</p>
        {item.dataProvenance.is_demo ? <InlineNotice>Демонстрационные данные: расписание и билетная ссылка используются только для проверки сценария.</InlineNotice> : null}
        {item.status !== 'published' ? <InlineNotice tone="danger">{item.status === 'sold_out' ? 'Билеты на это событие закончились.' : 'Событие отменено организатором.'}</InlineNotice> : null}
        <div className={styles.detailMeta}><span>◷ {item.date_label}</span><span>⌖ {item.venue_name}<small> · {item.venue.address}</small></span><span>₽ {item.price_label}</span></div>
        <section className={styles.section}><h2 className={styles.sectionTitle}>О событии</h2><p className={styles.bodyCopy}>{item.description}</p></section>
        <div className={styles.explain}><strong>Почему вам подходит</strong>{item.reasons.map((reason) => <span key={reason.code}>✓ {reason.text}</span>)}</div>
        {ticket.isError ? <InlineNotice tone="danger">Не удалось открыть билетный сервис. Можно повторить попытку.</InlineNotice> : null}
        <div className={styles.footer}><Button disabled={!item.ticketAvailable || item.status !== 'published' || ticket.isPending} onClick={() => ticket.mutate()}>{ticket.isPending ? 'Открываем…' : item.ticketAvailable && item.status === 'published' ? 'К билетам' : 'Билеты недоступны'}</Button></div>
      </PageContent>
    </PageShell>
  )
}
