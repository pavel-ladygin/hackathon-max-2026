import { useNavigate } from 'react-router-dom'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { useHomeFeed } from '../../features/discovery/queries'
import type { EventCard as EventCardModel } from '../../shared/api/types'
import { eventCategoryLabel, eventImage, eventImageFallback } from '../../shared/lib/events'
import { BottomNav, Button, Empty, EventCard, EventImage, PageContent, PageShell, ScreenSkeleton } from '../../shared/ui/index'
import styles from '../pages.module.css'

type FeedSection = { type: string; title: string; items: EventCardModel[] }

export function HomePage() {
  const navigate = useNavigate()
  const bootstrap = useBootstrap()
  const feed = useHomeFeed()

  if (feed.isPending) return <ScreenSkeleton variant="home" label="Собираем вашу афишу…" />
  if (feed.isError) return <Empty title="Афиша не загрузилась" description="Проверьте соединение и попробуйте ещё раз." action={<Button onClick={() => void feed.refetch()}>Повторить</Button>} />

  const sections = feed.data.sections as FeedSection[]
  const allEvents = uniqueEvents(sections.flatMap((section) => section.items))
  const hero = allEvents[0]
  const activeRoom = feed.data.activeRoom

  return (
    <PageShell withBottomNav>
      <PageContent>
        <header className={styles.heroHeader}>
          <div><span className={styles.eyebrow}>Ваш город</span><h3>Москва</h3></div>
          <button className={styles.profileButton} aria-label="Настройки предпочтений" onClick={() => navigate('/preferences')}>{bootstrap.data?.user.displayName.slice(0, 1) ?? 'И'}</button>
        </header>
        {activeRoom ? <section className={styles.section}><div className={`${styles.sectionHead} ${styles.homeAction}`}><div><p className={styles.eyebrow}>АКТИВНАЯ КОМНАТА</p><h2>{activeRoom.name}</h2></div><Button onClick={() => navigate(`/rooms/${activeRoom.id}/waiting`)}>Продолжить</Button></div><p className={styles.subtitle}>Вернитесь к совместному выбору, не теряя прогресс.</p></section> : <section className={styles.section}><div className={`${styles.sectionHead} ${styles.homeAction}`}><div><p className={styles.eyebrow}>ВМЕСТЕ ЛЕГЧЕ</p><h2>Выберите событие вдвоём</h2></div><Button onClick={() => navigate('/rooms/new')}>Создать комнату</Button></div></section>}
        {hero ? (
          <button type="button" className={styles.hero} onClick={() => navigate(`/events/${hero.id}`)}>
            <EventImage className={styles.heroImage} src={eventImage(hero.imageUrl, hero.category_slug)} fallbackSrc={eventImageFallback(hero.category_slug)} alt="" width="1200" height="675" loading="eager" fetchPriority="high" />
            <span className={styles.heroCopy}><span className={styles.eyebrow}>ПОПУЛЯРНОЕ СОБЫТИЕ</span><strong>{hero.title}</strong><span>{hero.date_label} · {hero.venue_name}</span><b>{hero.price_label}</b></span>
          </button>
        ) : null}
        <div className={styles.sectionHead}><h2>Для вас</h2><span className={styles.eyebrow}>{allEvents.length} событий</span></div>
        <div className={styles.eventGrid}>
          {allEvents.slice(1).map((event) => <EventCard key={event.id} event={{ id: event.id, title: event.title, image: eventImage(event.imageUrl, event.category_slug), eyebrow: `${eventCategoryLabel(event.category_slug)} · ${event.date_label}`, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} />)}
        </div>
        <BottomNav activeId="home" items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'catalog', label: 'Афиша', icon: 'calendar' }, { id: 'saved', label: 'Моё', icon: 'saved' }]} onChange={(id) => id === 'catalog' ? navigate('/events') : id === 'saved' ? navigate('/saved') : navigate('/')} />
      </PageContent>
    </PageShell>
  )
}

function uniqueEvents(events: EventCardModel[]) {
  return [...new Map(events.map((event) => [event.id, event])).values()]
}
