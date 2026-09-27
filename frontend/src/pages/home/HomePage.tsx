import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { useHomeFeed } from '../../features/discovery/queries'
import type { EventCard as EventCardModel } from '../../shared/api/types'
import { eventCategoryLabel, eventImage, eventImageFallback, eventImageSrcSet } from '../../shared/lib/events'
import { BottomNav, Button, Empty, EventCard, EventImage, PageContent, PageShell, ScreenSkeleton } from '../../shared/ui/index'
import styles from '../pages.module.css'
import { track, trackClientError, trackPerformance } from '../../shared/analytics/client'
import { EventImpression } from '../../shared/analytics/EventImpression'
import { apiClient } from '../../shared/api/client'
import homeStyles from './HomePage.module.css'

type FeedSection = { type: string; title: string; items: EventCardModel[] }

export function HomePage() {
  const navigate = useNavigate()
  const bootstrap = useBootstrap()
  const feed = useHomeFeed()
  const queryClient = useQueryClient()
  const [confirmClose, setConfirmClose] = useState(false)
  const closeConfirmRef = useRef<HTMLButtonElement>(null)
  const closeDialogRef = useRef<HTMLDivElement>(null)
  const endLinkRef = useRef<HTMLButtonElement>(null)
  const close = useMutation({
    mutationFn: (roomId: string) => apiClient.closeRoom(roomId),
    onSuccess: async (room) => {
      setConfirmClose(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['home-feed'] }),
        queryClient.invalidateQueries({ queryKey: ['room', room.id] }),
      ])
    },
  })
  const acknowledge = useMutation({
    mutationFn: (roomId: string) => apiClient.acknowledgeRoomClosedNotice(roomId),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['home-feed'] }) },
  })
  const feedStartedAt = useRef<number | null>(null)
  const reportedFeedError = useRef<unknown>(null)
  const feedId = feed.data?.feed_id
  const feedResultCount = feed.data?.sections.reduce((count, section) => count + section.items.length, 0)
  useEffect(() => { feedStartedAt.current = performance.now() }, [])
  useEffect(() => {
    if (!feedId || feedResultCount === undefined) return
    track('feed_opened', { properties: { result_count: feedResultCount } })
    trackPerformance('feed_load', performance.now() - (feedStartedAt.current ?? performance.now()))
  }, [feedId, feedResultCount])
  useEffect(() => {
    if (!feed.isError || !feed.error || reportedFeedError.current === feed.error) return
    reportedFeedError.current = feed.error
    trackClientError('feed_load', feed.error)
  }, [feed.error, feed.isError])
  useEffect(() => { if (confirmClose) closeConfirmRef.current?.focus() }, [confirmClose])

  if (feed.isPending) return <ScreenSkeleton variant="home" label="Собираем вашу афишу…" />
  if (feed.isError) return <Empty title="Афиша не загрузилась" description="Проверьте соединение и попробуйте ещё раз." action={<Button onClick={() => void feed.refetch()}>Повторить</Button>} />

  const sections = feed.data.sections as FeedSection[]
  const allEvents = uniqueEvents(sections.flatMap((section) => section.items))
  const hero = allEvents[0]
  const activeRoom = feed.data.activeRoom
  const closedNotice = feed.data.roomClosedNotice

  return (
    <PageShell withBottomNav>
      <PageContent>
        <header className={styles.heroHeader}>
          <div><span className={styles.eyebrow}>Ваш город</span><h3>Москва</h3></div>
          <button className={styles.profileButton} aria-label="Настройки предпочтений" onClick={() => navigate('/preferences')}>{bootstrap.data?.user.displayName.slice(0, 1) ?? 'И'}</button>
        </header>
        {closedNotice ? <section className={homeStyles.notice} role="status"><strong>Комната завершена</strong><p>Участник {closedNotice.closed_by.display_name} завершил подбор в комнате «{closedNotice.room_name}».</p><button type="button" disabled={acknowledge.isPending} onClick={() => acknowledge.mutate(closedNotice.room_id)}>Понятно</button>{acknowledge.isError ? <span role="alert">Не удалось убрать уведомление. Повторите.</span> : null}</section> : null}
        {activeRoom ? <section className={styles.section}><div className={`${styles.sectionHead} ${styles.homeAction}`}><div><p className={styles.eyebrow}>АКТИВНАЯ КОМНАТА</p><h2>{activeRoom.name}</h2></div><div className={homeStyles.roomActions}><Button onClick={() => navigate(`/rooms/${activeRoom.id}/waiting`)}>Продолжить</Button><button ref={endLinkRef} type="button" className={homeStyles.endLink} onClick={() => setConfirmClose(true)}>Завершить подбор</button></div></div><p className={styles.subtitle}>Вернитесь к совместному выбору, не теряя прогресс.</p></section> : <section className={styles.section}><div className={`${styles.sectionHead} ${styles.homeAction}`}><div><p className={styles.eyebrow}>ВМЕСТЕ ЛЕГЧЕ</p><h2>Выберите событие вдвоём</h2></div><Button onClick={() => navigate('/rooms/new')}>Создать комнату</Button></div></section>}
        {confirmClose && activeRoom ? <div className={homeStyles.dialogBackdrop} onMouseDown={(event) => { if (event.target === event.currentTarget && !close.isPending) { setConfirmClose(false); endLinkRef.current?.focus() } }}><div ref={closeDialogRef} className={homeStyles.dialog} role="dialog" aria-modal="true" aria-labelledby="close-room-title" onKeyDown={(event) => {
          if (event.key === 'Escape' && !close.isPending) { setConfirmClose(false); endLinkRef.current?.focus() }
          if (event.key !== 'Tab') return
          const controls = Array.from(closeDialogRef.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])
          if (!controls.length) return
          const current = controls.indexOf(document.activeElement as HTMLButtonElement)
          if (event.shiftKey && current === 0) { event.preventDefault(); controls[controls.length - 1].focus() }
          if (!event.shiftKey && current === controls.length - 1) { event.preventDefault(); controls[0].focus() }
        }}><p className={styles.eyebrow}>КОМНАТА «{activeRoom.name}»</p><h2 id="close-room-title">Завершить подбор?</h2><p>Комната закроется для вас обоих. Второй участник увидит, что вы завершили подбор. После этого можно будет создать новую комнату.</p>{close.isError ? <p className={homeStyles.error} role="alert">Не удалось завершить комнату. Попробуйте ещё раз.</p> : null}<button ref={closeConfirmRef} type="button" className={homeStyles.confirmEnd} disabled={close.isPending} onClick={() => close.mutate(activeRoom.id)}>{close.isPending ? 'Завершаем…' : 'Завершить для обоих'}</button><button type="button" className={homeStyles.cancelEnd} disabled={close.isPending} onClick={() => { setConfirmClose(false); endLinkRef.current?.focus() }}>Остаться в комнате</button></div></div> : null}
        {hero ? (
          <EventImpression event={hero} listType="feed" position={1}><button type="button" className={styles.hero} onClick={() => navigate(`/events/${hero.id}`)}>
            <EventImage className={styles.heroImage} src={eventImage(hero.imageUrl, hero.category_slug, 1200)} srcSet={eventImageSrcSet(hero.imageUrl, hero.category_slug)} sizes="100vw" fallbackSrc={eventImageFallback(hero.category_slug)} alt="" width="1200" height="675" loading="eager" fetchPriority="high" />
            <span className={styles.heroCopy}><span className={styles.eyebrow}>ПОПУЛЯРНОЕ СОБЫТИЕ</span><strong>{hero.title}</strong><span>{hero.date_label} · {hero.venue_name}</span><b>{hero.price_label}</b></span>
          </button></EventImpression>
        ) : null}
        <div className={styles.sectionHead}><h2>Для вас</h2><span className={styles.eyebrow}>{allEvents.length} событий</span></div>
        <div className={styles.eventGrid}>
          {allEvents.slice(1).map((event, index) => <EventImpression key={event.id} event={event} listType="feed" position={index + 2}><EventCard className={styles.feedCard} event={{ id: event.id, title: event.title, image: eventImage(event.imageUrl, event.category_slug), eyebrow: `${eventCategoryLabel(event.category_slug)} · ${event.date_label}`, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} /></EventImpression>)}
        </div>
        <BottomNav activeId="home" items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'catalog', label: 'Афиша', icon: 'calendar' }, { id: 'saved', label: 'Моё', icon: 'saved' }]} onChange={(id) => id === 'catalog' ? navigate('/events') : id === 'saved' ? navigate('/saved') : navigate('/')} />
      </PageContent>
    </PageShell>
  )
}

function uniqueEvents(events: EventCardModel[]) {
  return [...new Map(events.map((event) => [event.id, event])).values()]
}
