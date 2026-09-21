import { useNavigate, useSearchParams } from 'react-router-dom'
import { useSavedEvents, useSetSavedEvent } from '../../features/discovery/queries'
import { Button, Chip, Empty, EventCard, Loading, PageContent, PageShell, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'

export function SavedPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = searchParams.get('tab') === 'matches' ? 'matches' : 'saved'
  const events = useSavedEvents(tab)
  const save = useSetSavedEvent()
  const items = events.data?.pages.flatMap((page) => page.items) ?? []

  return <PageShell>
    <TopBar title="Моё" onBack={() => navigate('/')} />
    <PageContent className={styles.narrow}>
      <div className={styles.tabs} role="tablist" aria-label="События пользователя"><Chip selected={tab === 'saved'} onClick={() => setSearchParams({ tab: 'saved' })} role="tab" aria-selected={tab === 'saved'}>Сохранённое</Chip><Chip selected={tab === 'matches'} onClick={() => setSearchParams({ tab: 'matches' })} role="tab" aria-selected={tab === 'matches'}>Мэтчи</Chip></div>
      {events.isPending ? <Loading label="Загружаем список…" /> : events.isError ? <Empty title="Список не загрузился" action={<Button onClick={() => void events.refetch()}>Повторить</Button>} /> : items.length === 0 ? <Empty title={tab === 'saved' ? 'Сохранённых событий пока нет' : 'Мэтчей пока нет'} description={tab === 'saved' ? 'Нажмите ♡ на понравившемся событии.' : 'Создайте комнату с другом и найдите взаимный выбор.'} action={<Button onClick={() => navigate('/catalog')}>Открыть афишу</Button>} /> : <><div className={styles.eventGrid}>{items.map(({ event, match }) => <div key={event.id} className={styles.catalogItem}><EventCard event={{ id: event.id, title: event.title, image: event.imageUrl ?? '/events/concert-singer.png', eyebrow: tab === 'matches' ? 'МЭТЧ' : event.date_label, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} />{tab === 'saved' && <button type="button" className={styles.saveButton} aria-label={`Убрать «${event.title}» из сохранённых`} onClick={() => save.mutate({ eventId: event.id, saved: false })}>♥</button>}{match ? <span className={styles.matchNote}>Совпадение найдено</span> : null}</div>)}</div>{events.hasNextPage ? <Button tone="secondary" disabled={events.isFetchingNextPage} onClick={() => void events.fetchNextPage()}>{events.isFetchingNextPage ? 'Загружаем…' : 'Показать ещё'}</Button> : null}</>}
    </PageContent>
  </PageShell>
}
