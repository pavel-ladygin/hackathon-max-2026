import { useNavigate, useSearchParams } from 'react-router-dom'
import { useSavedEvents, useSetSavedEvent } from '../../features/discovery/queries'
import { eventCategoryLabel, eventImage } from '../../shared/lib/events'
import { BottomNav, Button, Chip, Empty, EventCard, FavoriteButton, PageContent, PageShell, ScreenSkeleton, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'

export function SavedPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = searchParams.get('tab') === 'matches' ? 'matches' : 'saved'
  const events = useSavedEvents(tab)
  const save = useSetSavedEvent()
  const items = events.data?.pages.flatMap((page) => page.items) ?? []

  return <PageShell withBottomNav>
    <TopBar title="Моё" onBack={() => navigate('/')} />
    <PageContent className={styles.narrow}>
      <div className={styles.tabs} role="tablist" aria-label="События пользователя"><Chip selected={tab === 'saved'} onClick={() => setSearchParams({ tab: 'saved' })} role="tab" aria-selected={tab === 'saved'}>Сохранённое</Chip><Chip selected={tab === 'matches'} onClick={() => setSearchParams({ tab: 'matches' })} role="tab" aria-selected={tab === 'matches'}>Мэтчи</Chip></div>
      {events.isPending ? <ScreenSkeleton variant="cards" inline label="Загружаем список…" /> : events.isError ? <Empty inline title="Список не загрузился" action={<Button onClick={() => void events.refetch()}>Повторить</Button>} /> : items.length === 0 ? <Empty inline title={tab === 'saved' ? 'Сохранённых событий пока нет' : 'Мэтчей пока нет'} description={tab === 'saved' ? 'Нажмите ♡ на понравившемся событии.' : 'Создайте комнату с другом и найдите взаимный выбор.'} action={<Button onClick={() => navigate('/events')}>Открыть афишу</Button>} /> : <><div className={styles.eventGrid}>{items.map(({ event, match }) => <div key={event.id} className={styles.catalogItem}><EventCard event={{ id: event.id, title: event.title, image: eventImage(event.imageUrl, event.category_slug), eyebrow: tab === 'matches' ? 'МЭТЧ' : `${eventCategoryLabel(event.category_slug)} · ${event.date_label}`, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} />{tab === 'saved' && <FavoriteButton className={styles.saveButton} selected pending={save.isPending} label={`Убрать «${event.title}» из сохранённых`} onToggle={() => save.mutate({ eventId: event.id, saved: false })} />}{match ? <span className={styles.matchNote}>Совпадение найдено</span> : null}</div>)}</div>{events.hasNextPage ? <Button tone="secondary" state={events.isFetchingNextPage ? 'loading' : 'idle'} loadingLabel="Загружаем…" onClick={() => void events.fetchNextPage()}>Показать ещё</Button> : null}</>}
      <BottomNav activeId="saved" items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'catalog', label: 'Афиша', icon: 'calendar' }, { id: 'saved', label: 'Моё', icon: 'saved' }]} onChange={(id) => id === 'home' ? navigate('/') : id === 'catalog' ? navigate('/events') : undefined} />
    </PageContent>
  </PageShell>
}
