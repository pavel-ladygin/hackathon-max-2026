import { lazy, Suspense, startTransition, useEffect, useMemo, useRef, useState } from 'react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useNavigate } from 'react-router-dom'
import { useEventSearch, useSetSavedEvent } from '../../features/discovery/queries'
import type { CategorySlug } from '../../shared/api/types'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import { eventCategoryLabel, eventImage, eventImageFallback } from '../../shared/lib/events'
import { BottomNav, Button, Chip, ChipGroup, Empty, EventCard, FavoriteButton, PageContent, PageShell, ScreenSkeleton, TopBar } from '../../shared/ui/index'
import { DatePicker } from '../../shared/ui/date-picker'
import styles from '../pages.module.css'
import catalogStyles from './catalogMap.module.css'
import { useDebouncedValue } from './useDebouncedValue'
import { track } from '../../shared/analytics/client'
import { EventImpression } from '../../shared/analytics/EventImpression'

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
  const [view, setView] = useState<'list' | 'map'>('list')
  const [advancedFiltersOpen, setAdvancedFiltersOpen] = useState(false)
  const trackedSearch = useRef('')
  const reduceMotion = useReducedMotion()
  const normalizedQuery = debouncedQuery.trim()
  const params = useMemo(() => ({ q: normalizedQuery || undefined, category_slugs: selected.length ? selected : undefined, date_from: dateFrom || undefined, date_to: dateTo || undefined, price_max_minor: price < 10_000 ? price * 100 : undefined, free_only: freeOnly || undefined, limit: 24 }), [dateFrom, dateTo, freeOnly, normalizedQuery, price, selected])
  const mapFilters = useMemo(() => ({ q: normalizedQuery || undefined, category_slugs: selected.length ? selected : undefined, date_from: dateFrom || undefined, date_to: dateTo || undefined, price_max_minor: price < 10_000 ? price * 100 : undefined, free_only: freeOnly || undefined }), [dateFrom, dateTo, freeOnly, normalizedQuery, price, selected])
  const results = useEventSearch(params, view === 'list')
  const save = useSetSavedEvent()
  const events = results.data?.pages.flatMap((page) => page.items) ?? []
  const totalEstimate = results.data?.pages[0]?.totalEstimate ?? 0
  const activeFilterCount = selected.length + (freeOnly ? 1 : 0) + (dateFrom ? 1 : 0) + (dateTo ? 1 : 0) + (price < 10_000 ? 1 : 0)
  const filterSnapshot = JSON.stringify({ selected, freeOnly, dateFrom, dateTo, price })
  const previousFilters = useRef(filterSnapshot)
  const isRefreshing = results.isFetching && !results.isFetchingNextPage && !results.isPending && events.length > 0
  const resetFilters = () => {
    track('filters_reset', { properties: { previous_active_filter_count: activeFilterCount } })
    setSelected([])
    setFreeOnly(false)
    setDateFrom('')
    setDateTo('')
    setPrice(10_000)
  }

  useEffect(() => {
    if (!results.data || !normalizedQuery || trackedSearch.current === normalizedQuery) return
    trackedSearch.current = normalizedQuery
    track('search_performed', { properties: { query_length: normalizedQuery.length, result_count: totalEstimate } })
  }, [normalizedQuery, results.data, totalEstimate])

  useEffect(() => {
    if (previousFilters.current === filterSnapshot) return
    previousFilters.current = filterSnapshot
    track('filters_applied', { properties: {
      categories: selected,
      ...(price < 10_000 ? { budget_max: price } : {}),
      ...(dateFrom ? { date_from: dateFrom } : {}),
      ...(dateTo ? { date_to: dateTo } : {}),
      result_count: totalEstimate,
    } })
  }, [activeFilterCount, dateFrom, dateTo, filterSnapshot, freeOnly, price, selected, totalEstimate])

  useEffect(() => {
    if (view === 'map') track('map_opened')
  }, [view])

  return <PageShell withBottomNav>
    <TopBar title="Афиша" onBack={() => navigate('/')} />
    <PageContent>
      <label className={styles.searchLabel} htmlFor="catalog-search">Найти событие</label>
      <div className={styles.searchField}>
        <input id="catalog-search" className={styles.searchInput} type="search" name="catalog-search" autoComplete="off" maxLength={120} value={query} onChange={(event) => { trackedSearch.current = ''; setQuery(event.target.value) }} placeholder="Название, место или жанр" />
        {query ? <button type="button" className={styles.searchClear} aria-label="Очистить поиск" onClick={() => setQuery('')}><svg viewBox="0 0 24 24" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="2.3" strokeLinecap="round"><path d="M5 5l14 14M19 5L5 19" /></svg></button> : null}
      </div>
      <div className={styles.catalogControls}>
        <div className={styles.quickFilters} aria-label="Быстрые фильтры">
          <Chip selected={freeOnly} onClick={() => setFreeOnly((value) => !value)}>Бесплатно</Chip>
          <Chip selected={advancedFiltersOpen || activeFilterCount > 0} aria-expanded={advancedFiltersOpen} aria-controls="catalog-advanced-filters" onClick={() => setAdvancedFiltersOpen((value) => { if (!value) track('filters_opened'); return !value })}>Фильтры{activeFilterCount ? ` · ${activeFilterCount}` : ''}</Chip>
        </div>
        <div className={`${styles.refreshOverlay} ${isRefreshing ? styles.refreshOverlayVisible : ''}`} role="status" aria-live="polite" aria-hidden={!isRefreshing}>
          <span className={styles.refreshDot} aria-hidden="true" />
          <span>Обновляем результаты…</span>
        </div>
      </div>
      <AnimatePresence initial={false}>
        {advancedFiltersOpen ? <motion.section id="catalog-advanced-filters" className={styles.advancedFilters} aria-label="Все фильтры" initial={{ opacity: 0, height: 0, y: reduceMotion ? 0 : -6 }} animate={{ opacity: 1, height: 'auto', y: 0 }} exit={{ opacity: 0, height: 0, y: reduceMotion ? 0 : -6 }} transition={{ duration: reduceMotion ? 0 : .22, ease: 'easeOut' }} style={{ overflow: 'hidden' }}>
          <ChipGroup label="Категории" className={styles.catalogChips}>{CATEGORY_OPTIONS.map((category) => <Chip key={category.slug} selected={selected.includes(category.slug)} onClick={() => setSelected((current) => current.includes(category.slug) ? current.filter((item) => item !== category.slug) : [...current, category.slug])}>{category.label}</Chip>)}</ChipGroup>
          <div className={styles.filterRow}>
            <DatePicker id="catalog-date-from" label="С" value={dateFrom} max={dateTo || undefined} onChange={(value) => setDateFrom(value ?? '')} />
            <DatePicker id="catalog-date-to" label="По" value={dateTo} min={dateFrom || undefined} onChange={(value) => setDateTo(value ?? '')} />
          </div>
          <label className={styles.fieldLabel}>Бюджет · до {price.toLocaleString('ru-RU')} ₽<input className={styles.range} type="range" name="price-maximum" min="0" max="10000" step="500" value={price} onChange={(event) => setPrice(Number(event.target.value))} /></label>
          <div className={`${styles.filterActions} ${activeFilterCount ? '' : styles.filterActionsInactive}`} aria-hidden={!activeFilterCount}>
            <Button tone="ghost" disabled={!activeFilterCount} tabIndex={activeFilterCount ? 0 : -1} onClick={resetFilters}>Очистить фильтры</Button>
          </div>
        </motion.section> : null}
      </AnimatePresence>
      <div className={catalogStyles.segmented} role="tablist" aria-label="Вид событий">
        {(['list', 'map'] as const).map((nextView) => <button key={nextView} type="button" role="tab" aria-selected={view === nextView} className={catalogStyles.segmentedItem} onClick={() => startTransition(() => setView(nextView))}>
          {view === nextView ? <motion.span layoutId="catalog-view-indicator" className={catalogStyles.segmentedIndicator} transition={{ duration: reduceMotion ? 0 : .2, ease: 'easeOut' }} /> : null}
          <span className={catalogStyles.segmentedLabel}>{nextView === 'list' ? 'Список' : 'Карта'}</span>
        </button>)}
      </div>
      <div className={styles.catalogResults}>{view === 'map' ? <>
        <div className={styles.sectionHead}><h2>События на карте</h2></div>
        <Suspense fallback={<ScreenSkeleton variant="map" inline label="Загружаем карту…" />}><CatalogMap filters={mapFilters} /></Suspense>
      </> : results.isPending ? <ScreenSkeleton variant="cards" inline label="Загружаем события…" /> : results.isError ? <Empty inline title="Поиск недоступен" description="Проверьте соединение и попробуйте ещё раз." action={<Button onClick={() => void results.refetch()}>Повторить</Button>} /> : events.length === 0 ? <Empty inline title="Ничего не нашли" description="Попробуйте убрать фильтр или изменить запрос." /> : <>
        <div className={styles.sectionHead}><h2>События</h2><span className={styles.eyebrow}>{totalEstimate} найдено</span></div>
        <AnimatePresence mode="wait" initial={false}>
          <motion.div key="list" initial={{ opacity: 0, y: reduceMotion ? 0 : 20 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: 0 }} transition={{ duration: reduceMotion ? 0 : .2, ease: 'easeOut' }}><div className={styles.eventGrid}>{events.map((event, index) => <EventImpression key={event.id} event={event} listType="search" position={index + 1} className={styles.catalogItem}><EventCard className={styles.eventCardWithSave} event={{ id: event.id, title: event.title, image: eventImage(event.imageUrl, event.category_slug), fallbackImage: eventImageFallback(event.category_slug), eyebrow: `${eventCategoryLabel(event.category_slug)} · ${event.date_label}`, meta: `${event.venue_name} · ${event.price_label}` }} onClick={() => navigate(`/events/${event.id}`)} /><FavoriteButton size="card" className={styles.saveButton} selected={event.saved} pending={save.isPending} label={event.saved ? `Убрать «${event.title}» из сохранённых` : `Сохранить «${event.title}»`} onToggle={() => save.mutate({ eventId: event.id, saved: !event.saved })} /></EventImpression>)}</div></motion.div>
        </AnimatePresence>
        {results.hasNextPage ? <Button className={styles.showMore} tone="secondary" disabled={results.isFetchingNextPage} onClick={() => void results.fetchNextPage()}>{results.isFetchingNextPage ? 'Загружаем…' : 'Показать ещё'}</Button> : null}
      </>}</div>
      <BottomNav activeId="catalog" items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'catalog', label: 'Афиша', icon: 'calendar' }, { id: 'saved', label: 'Моё', icon: 'saved' }]} onChange={(id) => id === 'home' ? navigate('/') : id === 'saved' ? navigate('/saved') : undefined} />
    </PageContent>
  </PageShell>
}
