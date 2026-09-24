import { useInfiniteQuery } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiClient } from '../../shared/api/client'
import type { EventCard, CategorySlug } from '../../shared/api/types'
import { Button, Empty, ScreenSkeleton } from '../../shared/ui'
import styles from './catalogMap.module.css'
import { createEventMarkerElement } from './eventMarker'
import { MapEventPreview } from './MapEventPreview'
import { loadYandexMaps, markerSizeForZoom, type YandexCamera, type YandexMap, type YandexMapUpdateEvent, type YandexMapsApi } from './yandexMaps'
import { boundsContain, MapAreaCache, paddedViewportBounds } from './mapResults'

const MOSCOW_CENTER: [number, number] = [37.618423, 55.751244]
const MAP_IDLE_MS = 320
const MAP_PAGE_SIZE = 50

type MapFilters = {
  q?: string
  category_slugs?: CategorySlug[]
  date_from?: string
  date_to?: string
  price_max_minor?: number
  free_only?: boolean
  city_id?: string
}
type MapEvent = { kind: 'event'; id: string; longitude: number; latitude: number; event: EventCard }
type MapCluster = { kind: 'cluster'; id: string; longitude: number; latitude: number; west: number; south: number; east: number; north: number; count: number }
type MapItem = MapEvent | MapCluster
type LocatedEvent = EventCard & { latitude?: number | null; longitude?: number | null }
type MarkerRecord = { child: unknown; element: HTMLElement; item: MapItem }

function readCamera(event: YandexMapUpdateEvent): YandexCamera | null {
  const location = event.location ?? event.camera
  const center = location?.center
  const zoom = location?.zoom
  if (!center || zoom === undefined || !Number.isFinite(zoom)) return null
  return { center, zoom }
}

function clusterElement(cluster: MapCluster, onOpen: () => void) {
  const element = document.createElement('button')
  element.type = 'button'
  element.className = styles.clusterMarker
  element.setAttribute('aria-label', `Показать ${cluster.count} событий`)
  element.textContent = String(cluster.count)
  element.addEventListener('click', onOpen)
  return element
}

function appendMapItem(api: YandexMapsApi, map: YandexMap, item: MapItem, onSelect: (event: EventCard) => void, onCluster: (cluster: MapCluster) => void, zoom: number): MarkerRecord {
  const element = item.kind === 'event'
    ? createEventMarkerElement(item.event, () => onSelect(item.event))
    : clusterElement(item, () => onCluster(item))
  if (item.kind === 'event') {
    const size = markerSizeForZoom(zoom)
    element.style.setProperty('--marker-size', `${size}px`)
    const image = element.querySelector('img')
    if (image) { image.width = size; image.height = size }
  }
  const child = new api.YMapMarker({ coordinates: [item.longitude, item.latitude] }, element)
  map.addChild(child)
  return { child, element, item }
}

function mapItemSignature(item: MapItem) {
  return JSON.stringify(item)
}

export function CatalogMap({ filters, initialCenter }: { filters: MapFilters; initialCenter?: [number, number] }) {
  const filtersKey = JSON.stringify(filters)
  const navigate = useNavigate()
  const mapNode = useRef<HTMLDivElement>(null)
  const map = useRef<YandexMap | null>(null)
  const mapApi = useRef<YandexMapsApi | null>(null)
  const markers = useRef(new Map<string, MarkerRecord>())
  const cache = useRef(new MapAreaCache<MapItem[]>(20, 5 * 60_000))
  const sequence = useRef(0)
  const pendingArea = useRef<string | null>(null)
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const centerLng = initialCenter?.[0]
  const centerLat = initialCenter?.[1]
  const initial = useMemo(() => centerLng === undefined || centerLat === undefined ? MOSCOW_CENTER : [centerLng, centerLat] as [number, number], [centerLat, centerLng])
  const [camera, setCamera] = useState<YandexCamera>({ center: initial, zoom: 11 })
  const cameraRef = useRef<YandexCamera>({ center: initial, zoom: 11 })
  const [viewportSize, setViewportSize] = useState({ width: 700, height: 500 })
  const [items, setItems] = useState<MapItem[]>([])
  const [itemsFilterKey, setItemsFilterKey] = useState(filtersKey)
  const [mapError, setMapError] = useState(false)
  const [mapReady, setMapReady] = useState(false)
  const [areaLoading, setAreaLoading] = useState(false)
  const [areaError, setAreaError] = useState(false)
  const [loadedAreaKey, setLoadedAreaKey] = useState<string | null>(null)
  const [selectedEventId, setSelectedEventId] = useState<string | null>(null)
  const [selectedEvent, setSelectedEvent] = useState<EventCard | null>(null)
  const [selectedEventFilterKey, setSelectedEventFilterKey] = useState(filtersKey)
  const [selectedCluster, setSelectedCluster] = useState<MapCluster | null>(null)
  const [selectedClusterFilterKey, setSelectedClusterFilterKey] = useState(filtersKey)
  const apiKey = import.meta.env.VITE_YANDEX_MAPS_API_KEY
  const integerZoom = Math.max(0, Math.min(22, Math.round(camera.zoom)))
  const cacheKey = `${filtersKey}\u0000${integerZoom}`
  const visibleItems = useMemo(() => itemsFilterKey === filtersKey ? items : [], [filtersKey, items, itemsFilterKey])
  const visibleSelectedEvent = selectedEventFilterKey === filtersKey ? selectedEvent : null
  const visibleCluster = selectedClusterFilterKey === filtersKey ? selectedCluster : null

  const selectEvent = useCallback((event: EventCard | null) => {
    for (const marker of markers.current.values()) {
      if (marker.item.kind !== 'event') continue
      const active = marker.item.id === event?.id
      marker.element.dataset.active = String(active)
      marker.element.setAttribute('aria-pressed', String(active))
    }
    setSelectedEventId(event?.id ?? null)
    setSelectedEvent(event)
    setSelectedEventFilterKey(filtersKey)
    setSelectedCluster(null)
  }, [filtersKey])

  const group = useInfiniteQuery({
    queryKey: ['event-search', 'map-cluster', filtersKey, selectedCluster?.west, selectedCluster?.south, selectedCluster?.east, selectedCluster?.north],
    enabled: Boolean(visibleCluster),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => apiClient.searchEvents({ ...filters, west: visibleCluster!.west, south: visibleCluster!.south, east: visibleCluster!.east, north: visibleCluster!.north, limit: MAP_PAGE_SIZE, cursor: pageParam }),
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  })
  const groupEvents = useMemo(() => group.data?.pages.flatMap((page) => page.items) ?? [], [group.data])

  const requestArea = useCallback(async (nextCamera: YandexCamera, size = viewportSize) => {
    if (!mapReady) return
    const zoom = Math.max(0, Math.min(22, Math.round(nextCamera.zoom)))
    const visibleBounds = paddedViewportBounds(nextCamera.center, zoom, size.width, size.height, 0)
    const key = `${filtersKey}\u0000${zoom}`
    const cached = cache.current.get(key, visibleBounds)
    if (cached) {
      sequence.current += 1
      pendingArea.current = null
      setItems(cached)
      setItemsFilterKey(filtersKey)
      setLoadedAreaKey(key)
      setAreaError(false)
      setAreaLoading(false)
      return
    }
    const padded = paddedViewportBounds(nextCamera.center, zoom, size.width, size.height, 0.25)
    const requestSignature = `${key}\u0000${JSON.stringify(padded)}`
    if (pendingArea.current === requestSignature) return
    pendingArea.current = requestSignature
    const requestId = ++sequence.current
    setAreaLoading(true)
    setAreaError(false)
    try {
      const result = await apiClient.getMapEvents({ ...filters, ...padded, zoom })
      if (requestId !== sequence.current) return
      cache.current.set(key, padded, result)
      setItems(result)
      setItemsFilterKey(filtersKey)
      setLoadedAreaKey(key)
      setAreaError(false)
    } catch {
      if (requestId === sequence.current) setAreaError(true)
    } finally {
      if (pendingArea.current === requestSignature) pendingArea.current = null
      if (requestId === sequence.current) setAreaLoading(false)
    }
  }, [filters, filtersKey, mapReady, viewportSize])
  const requestAreaRef = useRef(requestArea)
  useEffect(() => { requestAreaRef.current = requestArea }, [requestArea])

  const openCluster = useCallback((cluster: MapCluster) => {
    if (camera.zoom >= 21.5) { setSelectedCluster(cluster); setSelectedClusterFilterKey(filtersKey); return }
    const sameArea = boundsContain(cluster, paddedViewportBounds(camera.center, Math.round(camera.zoom), viewportSize.width, viewportSize.height, 0))
    if (sameArea && camera.zoom >= 20) { setSelectedCluster(cluster); setSelectedClusterFilterKey(filtersKey); return }
    map.current?.setLocation?.({ bounds: [[cluster.west, cluster.south], [cluster.east, cluster.north]] })
    if (!map.current?.setLocation) {
      const next = { center: [cluster.longitude, cluster.latitude] as [number, number], zoom: Math.min(22, camera.zoom + 2) }
      cameraRef.current = next
      setCamera(next)
      void requestArea(next)
    }
  }, [camera, filtersKey, requestArea, viewportSize])
  const openClusterRef = useRef(openCluster)
  useEffect(() => { openClusterRef.current = openCluster }, [openCluster])

  useEffect(() => {
    if (!mapNode.current || !apiKey) return
    let cancelled = false
    let resizeObserver: ResizeObserver | null = null
    const markerRegistry = markers.current
    void loadYandexMaps(apiKey).then((ymaps3) => {
      if (cancelled || !mapNode.current) return
      const { YMap, YMapDefaultFeaturesLayer, YMapDefaultSchemeLayer, YMapListener } = ymaps3
      const nextMap = new YMap(mapNode.current, { location: { center: initial, zoom: 11 } })
      const element = mapNode.current
      resizeObserver = new ResizeObserver(([entry]) => setViewportSize({ width: entry.contentRect.width, height: entry.contentRect.height }))
      resizeObserver.observe(element)
      nextMap.addChild(new YMapDefaultSchemeLayer())
      nextMap.addChild(new YMapDefaultFeaturesLayer())
      nextMap.addChild(new YMapListener({ onUpdate: (event: YandexMapUpdateEvent) => {
        const nextCamera = readCamera(event)
        if (!nextCamera) return
        if (idleTimer.current) clearTimeout(idleTimer.current)
        idleTimer.current = setTimeout(() => {
          cameraRef.current = nextCamera
          setCamera(nextCamera)
          requestAreaRef.current(nextCamera)
        }, MAP_IDLE_MS)
      } }))
      mapApi.current = ymaps3
      map.current = nextMap
      setCamera({ center: initial, zoom: 11 })
      setMapReady(true)
    }).catch(() => { if (!cancelled) setMapError(true) })
    return () => {
      cancelled = true
      resizeObserver?.disconnect()
      if (idleTimer.current) clearTimeout(idleTimer.current)
      map.current?.destroy()
      map.current = null
      mapApi.current = null
      setMapReady(false)
      markerRegistry.clear()
    }
  }, [apiKey, initial])

  useEffect(() => {
    if (!mapReady) return
    cache.current.clear()
    sequence.current += 1
    const resetTimer = setTimeout(() => {
      setSelectedCluster(null)
      selectEvent(null)
    }, 0)
    return () => clearTimeout(resetTimer)
  }, [filtersKey, mapReady, selectEvent])

  useEffect(() => {
    if (!mapReady || !map.current || !mapApi.current) return
    const nextIds = new Set(visibleItems.map((item) => `${item.kind}:${item.id}`))
    for (const [id, marker] of markers.current) {
      if (nextIds.has(id)) continue
      map.current.removeChild(marker.child)
      markers.current.delete(id)
    }
    for (const item of visibleItems) {
      const key = `${item.kind}:${item.id}`
      const current = markers.current.get(key)
      if (current && mapItemSignature(current.item) === mapItemSignature(item)) continue
      if (current) map.current.removeChild(current.child)
      const marker = appendMapItem(mapApi.current, map.current, item, selectEvent, (cluster) => openClusterRef.current(cluster), integerZoom)
      if (item.kind === 'event') {
        marker.element.dataset.active = String(item.id === selectedEventId)
        marker.element.setAttribute('aria-pressed', String(item.id === selectedEventId))
      }
      markers.current.set(key, marker)
    }
  }, [integerZoom, mapReady, openCluster, selectEvent, selectedEventId, visibleItems])

  useEffect(() => {
    const size = markerSizeForZoom(integerZoom)
    for (const marker of markers.current.values()) {
      if (marker.item.kind !== 'event') continue
      marker.element.style.setProperty('--marker-size', `${size}px`)
      const image = marker.element.querySelector('img')
      if (image) { image.width = size; image.height = size }
    }
  }, [integerZoom])

  useEffect(() => {
    if (!mapReady) return
    const currentCamera = cameraRef.current
    const zoom = Math.max(0, Math.min(22, Math.round(currentCamera.zoom)))
    const key = `${filtersKey}\u0000${zoom}`
    const next = paddedViewportBounds(currentCamera.center, zoom, viewportSize.width, viewportSize.height, 0)
    const currentIsCovered = cache.current.get(key, next) !== undefined
    if (!currentIsCovered) {
      if (idleTimer.current) clearTimeout(idleTimer.current)
      idleTimer.current = setTimeout(() => requestAreaRef.current(cameraRef.current), MAP_IDLE_MS)
    }
  }, [filtersKey, mapReady, viewportSize])

  if (!apiKey || mapError) return <Empty title="Карта сейчас недоступна" description={!apiKey ? 'Для карты не настроен API-ключ.' : 'Переключитесь на список — события доступны там.'} />

  const locatedEvents = visibleItems.filter((item): item is MapEvent => item.kind === 'event').map((item) => item.event as LocatedEvent)
  const hasAnyCoordinates = locatedEvents.some((event) => event.latitude != null && event.longitude != null)
  const noResults = loadedAreaKey === cacheKey && !areaLoading && !areaError && visibleItems.length === 0
  return <div className={styles.mapWrap} aria-label="Карта событий">
    <div ref={mapNode} className={styles.map} role="region" aria-label="Яндекс Карта событий" />
    {!mapReady ? <ScreenSkeleton variant="map" inline label="Загружаем карту…" /> : null}
    {areaError ? <div className={`${styles.mapMessage} ${styles.mapMessageInteractive}`}><Empty inline title="События не загрузились" description="Проверьте соединение и повторите поиск." action={<Button onClick={() => void requestArea(camera)}>Повторить</Button>} /></div> : null}
    {noResults ? <div className={`${styles.mapMessage} ${styles.mapMessagePassive}`}><Empty inline title="В этой области событий не найдено" description="Переместите карту или измените фильтры." /></div> : null}
    {!areaLoading && locatedEvents.length > 0 && !hasAnyCoordinates ? <div className={`${styles.mapMessage} ${styles.mapMessagePassive}`}><Empty inline title="Для этих событий нет координат" description="Попробуйте изменить область карты или переключиться на список." /></div> : null}
    {visibleSelectedEvent ? <MapEventPreview event={visibleSelectedEvent} onClose={() => selectEvent(null)} onDetails={() => navigate(`/events/${visibleSelectedEvent.id}`)} /> : null}
    {areaLoading ? <span className={styles.mapLoading} role="status">Загружаем события…</span> : null}
    {visibleCluster ? <section className={styles.clusterList} aria-label="События в группе">
      <button className={styles.clusterListClose} type="button" aria-label="Закрыть список событий" onClick={() => setSelectedCluster(null)}>×</button>
      <h2>События рядом ({visibleCluster.count})</h2>
      {group.isPending ? <p>Загружаем события…</p> : groupEvents.map((event) => <button key={event.id} type="button" className={styles.clusterEvent} onClick={() => selectEvent(event)}>{event.title}<span>{event.date_label}</span></button>)}
      {group.isError ? <Button onClick={() => void group.refetch()}>Повторить</Button> : null}
      {group.hasNextPage ? <Button disabled={group.isFetchingNextPage} onClick={() => void group.fetchNextPage()}>{group.isFetchingNextPage ? 'Загружаем…' : 'Показать ещё'}</Button> : null}
    </section> : null}
  </div>
}
