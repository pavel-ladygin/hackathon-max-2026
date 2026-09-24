import { useMutation } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useEventDetail, useSetSavedEvent } from '../../features/discovery/queries'
import { apiClient } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import { eventCategoryLabel, eventHeroImage, eventImageFallback } from '../../shared/lib/events'
import { withMinimumDuration } from '../../shared/lib/async'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, Empty, EventImage, FavoriteButton, InlineNotice, PageContent, PageShell, ScreenSkeleton, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'

export function EventPage() {
  const { eventId } = useParams()
  const navigate = useNavigate()
  const event = useEventDetail(eventId)
  const save = useSetSavedEvent()
  const behaviorId = useRef(crypto.randomUUID())
  const ticket = useMutation({
    mutationFn: async () => {
      const { external_url } = await withMinimumDuration(apiClient.recordTicketClick(eventId!, { source: 'event_detail' }), 140)
      if (!await maxPlatform.openTicketLink(external_url)) throw new Error('Ticket link could not be opened')
    },
  })

  useEffect(() => {
    if (!event.data) return
    void apiClient.recordBehavior([{ client_event_id: behaviorId.current, type: 'open', occurred_at: new Date().toISOString(), event_id: event.data.id, metadata: { surface: 'event_detail' } }])
  }, [event.data])

  if (event.isPending) return <ScreenSkeleton variant="event" label="Открываем событие…" />
  if (event.isError) {
    const notFound = event.error instanceof ApiError && event.error.code === 'NOT_FOUND'
    return <Empty title={notFound ? 'Событие не найдено' : 'Не удалось загрузить событие'} description={notFound ? undefined : 'Проверьте соединение и попробуйте ещё раз.'} action={notFound ? <Button onClick={() => navigate('/')}>Вернуться в афишу</Button> : <Button state={event.isFetching ? 'loading' : 'idle'} onClick={() => void event.refetch()}>Повторить</Button>} />
  }

  const item = event.data
  return (
    <PageShell>
      <div className={styles.eventTopBar}><TopBar spacious prominentBack title="Событие" onBack={() => navigate(-1)} right={event.data ? <FavoriteButton size="action" selected={event.data.saved} pending={save.isPending} className={styles.detailSaveButton} label={event.data.saved ? 'Убрать из сохранённых' : 'Сохранить событие'} onToggle={() => save.mutate({ eventId: event.data.id, saved: !event.data.saved })} /> : null} /></div>
      <EventImage className={styles.detailHero} src={eventHeroImage(item.images, item.imageUrl, item.category_slug)} fallbackSrc={eventImageFallback(item.category_slug)} alt={item.title} width="1200" height="720" loading="eager" fetchPriority="high" />
      <PageContent className={styles.narrow}>
        <p className={styles.eyebrow}>{eventCategoryLabel(item.category_slug)}</p>
        <h1 className={styles.title}>{item.title}</h1>
        <p className={styles.subtitle}>{item.subtitle}</p>
        <dl className={styles.detailMeta} aria-label="Основная информация о событии">
          <div><dt>Когда</dt><dd>{item.date_label}</dd></div>
          <div><dt>Где</dt><dd>{item.venue_name || item.venue.address}{hasDistinctAddress(item.venue_name, item.venue.address) ? <small>{item.venue.address}</small> : null}</dd></div>
          <div><dt>Цена</dt><dd>{item.price_label}</dd></div>
        </dl>
        {item.status !== 'published' ? <InlineNotice tone="danger">{item.status === 'sold_out' ? 'Билеты на это событие закончились.' : 'Событие отменено организатором.'}</InlineNotice> : null}
        {item.dataProvenance.is_demo ? <InlineNotice>Демонстрационные данные: расписание и билетная ссылка используются только для проверки сценария.</InlineNotice> : null}
        <section className={styles.section}><h2 className={styles.sectionTitle}>О событии</h2><p className={styles.bodyCopy}>{item.description.trim() || 'Организатор пока не добавил описание'}</p></section>
        {item.reasons.length > 0 ? <div className={styles.explain}><strong>Почему вам подходит</strong>{item.reasons.map((reason) => <span key={reason.code}>✓ {reason.text}</span>)}</div> : null}
        {ticket.isError ? <InlineNotice tone="danger">Не удалось открыть билетный сервис. Можно повторить попытку.</InlineNotice> : null}
        <div className={styles.footer}><p className={styles.externalHint}>Билетный сервис откроется во внешнем окне.</p><Button aria-label="Открыть билеты во внешнем билетном сервисе" state={ticket.isPending ? 'loading' : 'idle'} loadingLabel="Открываем…" disabled={!item.ticketAvailable || item.status !== 'published'} onClick={() => ticket.mutate()}>{item.ticketAvailable && item.status === 'published' ? 'Открыть билеты' : 'Билеты недоступны'}</Button></div>
      </PageContent>
    </PageShell>
  )
}

function hasDistinctAddress(venueName: string, address: string): boolean {
  if (!address.trim()) return false
  if (!venueName.trim()) return true
  return normalizeAddress(venueName) !== normalizeAddress(address)
}

function normalizeAddress(value: string): string {
  return value.normalize('NFKC').toLocaleLowerCase('ru-RU').replace(/[ё]/g, 'е').replace(/[.,;:№#\-–—/\\]/g, ' ').replace(/\s+/g, ' ').trim()
}
