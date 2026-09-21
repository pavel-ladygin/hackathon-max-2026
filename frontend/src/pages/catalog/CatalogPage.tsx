import { lazy, Suspense, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useEventSearch, useSetSavedEvent } from '../../features/discovery/queries'
import type { CategorySlug } from '../../shared/api/types'
import { eventCategoryLabel, eventImage } from '../../shared/lib/events'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { BottomNav, Button, Chip, ChipGroup, Empty, EventCard, InlineNotice, Loading, PageContent, PageShell, Skeleton, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'
import { useDebouncedValue } from './useDebouncedValue'

const categories: Array<{ slug: CategorySlug; label: string }> = [
  { slug: 'concerts', label: 'Концерты' }, { slug: 'cinema', label: 'Кино' }, { slug: 'theatre', label: 'Театр' },
  { slug: 'standup', label: 'Стендап' }, { slug: 'exhibitions', label: 'Выставки' }, { slug: 'food', label: 'Еда' },
]
const CatalogMap = lazy(() => import('./CatalogMap').then((module) => ({ default: module.CatalogMap })))

export function CatalogPage() {
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const debouncedQuery = useDebouncedValue(query)
  const [selected, setSelected] = useState<CategorySlug[]>([])
  const [freeOnly, setFreeOnly] = useState(false)
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [price, setPrice] = useState(10_000)
  const [position, setPosition] = useState<{ lat: number; lng: number } | null>(null)
  const [view, setView] = useState<'list' | 'map'>('list')
  const [locationDenied, setLocationDenied] = useState(false)
  const [advancedFiltersOpen, setAdvancedFiltersOpen] = useState(false)
  const normalizedQuery = debouncedQuery.trim()
  const params = useMemo(() => ({ q: normalizedQuery || undefined, category_slugs: selected.length ? selected : undefined, date_from: dateFrom || undefined, date_to: dateTo || undefined, price_max_minor: price < 10_000 ? price * 100 : undefined, free_only: freeOnly || undefined, distance_m: position ? 10_000 : undefined, lat: position?.lat, lng: position?.lng, limit: 24 }), [dateFrom, dateTo, freeOnly, normalizedQuery, position, price, selected])
  const results = useEventSearch(params)
  const save = useSetSavedEvent()
  const events = results.data?.pages.flatMap((page) => page.items) ?? []
  const totalEstimate = results.data?.pages[0]?.totalEstimate ?? 0
  const activeFilterCount = selected.length + (freeOnly ? 1 : 0) + (dateFrom ? 1 : 0) + (dateTo ? 1 : 0) + (price < 10_000 ? 1 : 0) + (position ? 1 : 0)
  const isRefreshing = results.isFetching && !results.isFetchingNextPage && !results.isPending && events.length > 0
  const resetFilters = () => {
    setSelected([])
    setFreeOnly(false)
    setDateFrom('')
    setDateTo('')
    setPrice(10_000)
    setPosition(null)
    setLocationDenied(false)
  }

  return <PageShell withBottomNav>
    <TopBar title="Афиша" onBack={() => navigate('/')} />
    <PageContent>
      <label className={styles.searchLabel} htmlFor="catalog-search">Найти событие</label>
      <input id="catalog-search" className={styles.searchInput} type="search" maxLength={120} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Название, место или жанр" />
      {isRefreshing ? <InlineNotice>Обновляем результаты…</InlineNotice> : null}
      <div className={styles.quickFilters} aria-label="Быстрые фильтры">
        <Chip selected={freeOnly} onClick={() => setFreeOnly((value) => !value)}>Бесплатно</Chip>
        <Chip selected={Boolean(position)} onClick={async () => { const geo = await maxPlatform.requestLocation(); if (geo) { setPosition({ lat: geo.lat, lng: geo.lng }); setLocationDenied(false) } else setLocationDenied(true) }}>{position ? 'Рядом · 10 км' : 'Найти рядом'}</Chip>
        <Chip selected={advancedFiltersOpen || activeFilterCount > 0} aria-expanded={advancedFiltersOpen} aria-controls="catalog-advanced-filters" onClick={() => setAdvancedFiltersOpen((value) => !value)}>Фильтры{activeFilterCount ? ` · ${activeFilterCount}` : ''}</Chip>
      </div>
      {advancedFiltersOpen ? <section id="catalog-advanced-filters" className={styles.advancedFilters} aria-label="Все фильтры">
        <ChipGroup label="Категории" className={styles.catalogChips}>{categories.map((category) => <Chip key={category.slug} selected={selected.includes(category.slug)} onClick={() => setSelected((current) => current.includes(category.slug) ? current.filter((item) => item !== category.slug) : [...current, category.slug])}>{category.label}</Chip>)}</ChipGroup>
        <div className={styles.filterRow}><label className={styles.fieldLabel}>С<input className={styles.input} type="date" value={dateFrom} max={dateTo || undefined} onChange={(event) => setDateFrom(event.target.value)} /></label><label className={styles.fieldLabel}>По<input className={styles.input} type="date" value={dateTo} min={dateFrom || undefined} onChange={(event) => setDateTo(event.target.value)} /></label></div>
        <label className={styles.fieldLabel}>Бюджет · до {price.toLocaleString('ru-RU')} ₽<input className={styles.range} type="range" min="0" max="10000" step="500" value={price} onChange={(event) => setPrice(Number(event.target.value))} /></label>
        {activeFilterCount ? <div className={styles.filterActions}><Button tone="ghost" onClick={resetFilters}>Очистить фильтры</Button></div> : null}
      </section> : null}
      {locationDenied ? <p className={styles.error} role="status">Геолокация недоступна. Остальные фильтры продолжают работать.</p> : null}
      <div className={styles.viewSwitch}><Chip selected={view === 'list'} onClick={() => setView('list')}>Список</Chip><Chip selected={view === 'map'} onClick={() => setView('map')}>Карта</Chip></div>
      {results.isPending ? <div className={styles.catalogSkeleton} aria-label="Загрузка событий">{[0, 1, 2].map((item) => <Skeleton key={item} className={styles.skeletonCard} label="Загрузка" />)}</div> : results.isError ? <Empty inline title="Поиск недоступен" description="Проверьте соединение и попробуйте ещё раз." action={<Button onClick={() => void results.refetch()}>Повторить</Button>} /> : events.length === 0 ? <Empty inline title="Ничего не нашли" description="Попробуйте убрать фильтр или изменить запрос." /> : <>
        <div className={styles.sectionHead}><h2>События</h2><span className={styles.eyebrow}>{totalEstimate} найдено</span></div>
        {view === 'map' ? <Suspense fallback={<Loading inline label="Загружаем карту…" />}><CatalogMap events={events} /></Suspense> : <div className={styles.eventGrid}>{events.map((event) => <div key={event.id} className={styles.catalogItem}><EventCard event={{ id: event.id, title: event.title, image: eventImage(event.imageUrl, event.category_slug), eyebrow: `${eventCategoryLabel(event.category_slug)} · ${event.date_label}`, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} /><button type="button" className={styles.saveButton} aria-label={event.saved ? `Убрать «${event.title}» из сохранённых` : `Сохранить «${event.title}»`} aria-pressed={event.saved} disabled={save.isPending} onClick={() => save.mutate({ eventId: event.id, saved: !event.saved })}>{event.saved ? '♥' : '♡'}</button></div>)}</div>}
        {results.hasNextPage ? <Button tone="secondary" disabled={results.isFetchingNextPage} onClick={() => void results.fetchNextPage()}>{results.isFetchingNextPage ? 'Загружаем…' : 'Показать ещё'}</Button> : null}
      </>}
      <BottomNav activeId="catalog" items={[{ id: 'home', label: 'Главная', icon: '⌂' }, { id: 'catalog', label: 'Афиша', icon: '⌕' }, { id: 'saved', label: 'Моё', icon: '♡' }]} onChange={(id) => id === 'home' ? navigate('/') : id === 'saved' ? navigate('/saved') : undefined} />
    </PageContent>
  </PageShell>
}
