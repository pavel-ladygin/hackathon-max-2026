import { useNavigate } from 'react-router-dom'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { useHomeFeed } from '../../features/discovery/queries'
import type { EventCard as EventCardModel } from '../../shared/api/types'
import { BottomNav, Button, Empty, EventCard, Loading, PageContent, PageShell } from '../../shared/ui/index'
import styles from '../pages.module.css'

type FeedSection = { type: string; title: string; items: EventCardModel[] }

export function HomePage() {
  const navigate = useNavigate()
  const bootstrap = useBootstrap()
  const feed = useHomeFeed()

  if (feed.isPending) return <Loading label="Собираем вашу афишу…" />
  if (feed.isError) return <Empty title="Афиша не загрузилась" description="Проверьте соединение и попробуйте ещё раз." action={<Button onClick={() => void feed.refetch()}>Повторить</Button>} />

  const sections = feed.data.sections as FeedSection[]
  const allEvents = uniqueEvents(sections.flatMap((section) => section.items))
  const hero = allEvents[0]

  return (
    <PageShell>
      <PageContent>
        <header className={styles.heroHeader}>
          <div><span className={styles.eyebrow}>Ваш город</span><h3>Москва</h3></div>
          <button className={styles.profileButton} aria-label="Настройки предпочтений" onClick={() => navigate('/preferences')}>{bootstrap.data?.user.displayName.slice(0, 1) ?? 'И'}</button>
        </header>
        {hero ? (
          <button className={styles.hero} onClick={() => navigate(`/events/${hero.id}`)}>
            <img className={styles.heroImage} src={hero.imageUrl ?? '/events/concert-singer.png'} alt="" />
            <span className={styles.heroCopy}><span className={styles.eyebrow}>ПОПУЛЯРНОЕ СОБЫТИЕ</span><strong>{hero.title}</strong><span>{hero.date_label} · {hero.venue_name}</span><b>{hero.price_label}</b></span>
          </button>
        ) : null}
        <div className={styles.sectionHead}><h2>Для вас</h2><span className={styles.eyebrow}>{allEvents.length} событий</span></div>
        <div className={styles.eventGrid}>
          {allEvents.slice(1).map((event) => <EventCard key={event.id} event={{ id: event.id, title: event.title, image: event.imageUrl ?? '/events/concert-singer.png', eyebrow: `${categoryLabel(event.category_slug)} · ${event.date_label}`, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} />)}
        </div>
        <BottomNav activeId="home" items={[{ id: 'home', label: 'Главная', icon: '⌂' }, { id: 'catalog', label: 'Афиша', icon: '⌕' }, { id: 'saved', label: 'Моё', icon: '♡' }]} onChange={(id) => id === 'catalog' ? navigate('/events') : id === 'saved' ? navigate('/saved') : navigate('/')} />
      </PageContent>
    </PageShell>
  )
}

function uniqueEvents(events: EventCardModel[]) {
  return [...new Map(events.map((event) => [event.id, event])).values()]
}

function categoryLabel(category: string) {
  return ({ concerts: 'Концерт', cinema: 'Кино', theatre: 'Театр', standup: 'Стендап', exhibitions: 'Выставка', food: 'Еда' } as Record<string, string>)[category] ?? 'Событие'
}
