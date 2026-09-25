import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiClient } from '../../shared/api/client'
import type { EventCard, CategorySlug } from '../../shared/api/types'
import { Button, Empty, ScreenSkeleton } from '../../shared/ui'
import styles from './catalogMap.module.css'
import { createEventMarkerElement } from './eventMarker'
import { MapEventPreview } from './MapEventPreview'
import { loadYandexMaps, markerSizeForZoom, type YandexCamera, type YandexMap, type YandexMapUpdateEvent, type YandexMapsApi } from './yandexMaps'
import { MapAreaCache, paddedViewportBounds } from './mapResults'

const MOSCOW_CENTER: [number, number] = [37.618423, 55.751244]
const MAP_IDLE_MS = 320
const MAX_OVERLAP_DIAMETER = 72
const SPIDER_RADIUS = 80
const EXPANDED_CONTROL_SIZE = 44
const EXPANDED_CONTROL_GAP = 12
const EXPANDED_NEXT_OFFSET = SPIDER_RADIUS + MAX_OVERLAP_DIAMETER / 2 + EXPANDED_CONTROL_SIZE / 2 + EXPANDED_CONTROL_GAP

type MapFilters = {
  q?: string
  category_slugs?: CategorySlug[]
  date_from?: string
  date_to?: string
  price_max_minor?: number
  free_only?: boolean
  city_id?: string
  lat?: number
  lng?: number
  distance_m?: number
}
type MapEvent = { kind: 'event'; id: string; longitude: number; latitude: number; event: EventCard }
type MapCluster = { kind: 'cluster'; id: string; longitude: number; latitude: number; west: number; south: number; east: number; north: number; count: number; members: MapEvent[] }
type MapItem = MapEvent | MapCluster
type LocatedEvent = EventCard & { latitude?: number | null; longitude?: number | null }
type MarkerRecord = { child: unknown; element: HTMLElement; item: MapItem }
type OverlapRecord = { child: unknown; closeChild: unknown; element: HTMLElement; members: MarkerRecord[] }
const SPIDER_PAGE_SIZE = 4

function wrappedPixelDelta(delta: number, world: number) {
  return ((delta + world / 2) % world + world) % world - world / 2
}

function mapPoint(longitude: number, latitude: number, zoom: number): [number, number] {
  const scale = 256 * 2 ** zoom
  const x = (longitude + 180) / 360 * scale
  const sin = Math.sin(Math.max(-85.0511, Math.min(85.0511, latitude)) * Math.PI / 180)
  const y = (0.5 - Math.log((1 + sin) / (1 - sin)) / (4 * Math.PI)) * scale
  return [x, y]
}

function overlappingGroups(events: MarkerRecord[], zoom: number): MarkerRecord[][] {
  const world = 256 * 2 ** zoom
  const points = events.map((marker) => ({ marker, point: mapPoint(marker.item.longitude, marker.item.latitude, zoom) }))
  const groups: typeof points[] = []
  for (const candidate of points) {
    const group = groups.find((members) => members.every(({ point }) =>
      Math.abs(wrappedPixelDelta(point[0] - candidate.point[0], world)) < MAX_OVERLAP_DIAMETER &&
      Math.abs(point[1] - candidate.point[1]) < MAX_OVERLAP_DIAMETER))
    if (group) group.push(candidate)
    else groups.push([candidate])
  }
  return groups.filter((group) => group.length > 1).map((group) => group.map(({ marker }) => marker))
}

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

function overlapElement(count: number, onToggle: () => void) {
  const element = document.createElement('button')
  element.type = 'button'
  element.className = styles.clusterMarker
  element.setAttribute('aria-label', `Показать ${count} событий в этой точке`)
  element.textContent = String(count)
  element.addEventListener('click', onToggle)
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

export function CatalogMap({ filters, initialCenter, userLocation }: { filters: MapFilters; initialCenter?: [number, number]; userLocation?: { latitude: number; longitude: number } }) {
  const filtersKey = JSON.stringify(filters)
  const navigate = useNavigate()
  const mapNode = useRef<HTMLDivElement>(null)
  const map = useRef<YandexMap | null>(null)
  const mapApi = useRef<YandexMapsApi | null>(null)
  const markers = useRef(new Map<string, MarkerRecord>())
  const overlaps = useRef(new Map<string, OverlapRecord>())
  const cache = useRef(new MapAreaCache<MapItem[]>(20, 5 * 60_000))
  const sequence = useRef(0)
  const pendingArea = useRef<string | null>(null)
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const centerLng = initialCenter?.[0]
  const centerLat = initialCenter?.[1]
  const userLatitude = userLocation?.latitude
  const userLongitude = userLocation?.longitude
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
  const [expandedCluster, setExpandedCluster] = useState<{ id: string; page: number } | null>(null)
  const [selectedEvent, setSelectedEvent] = useState<EventCard | null>(null)
  const [selectedEventFilterKey, setSelectedEventFilterKey] = useState(filtersKey)
  const apiKey = import.meta.env.VITE_YANDEX_MAPS_API_KEY
  const integerZoom = Math.max(0, Math.min(22, Math.round(camera.zoom)))
  const cacheKey = `${filtersKey}\u0000${integerZoom}`
  const visibleItems = useMemo(() => itemsFilterKey === filtersKey ? items : [], [filtersKey, items, itemsFilterKey])
  const visibleSelectedEvent = selectedEventFilterKey === filtersKey ? selectedEvent : null

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
  }, [filtersKey])

  const requestArea = useCallback(async (nextCamera: YandexCamera, size = viewportSize, force = false) => {
    if (!mapReady) return
    const zoom = Math.max(0, Math.min(22, Math.round(nextCamera.zoom)))
    const visibleBounds = paddedViewportBounds(nextCamera.center, zoom, size.width, size.height, 0)
    const key = `${filtersKey}\u0000${zoom}`
    if (force) cache.current.clear()
    const cached = force ? undefined : cache.current.get(key, visibleBounds)
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
    const requestSignature = `${key}\u0000${JSON.stringify(padded)}${force ? `\u0000force:${sequence.current + 1}` : ''}`
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
    setExpandedCluster({ id: cluster.id, page: 0 })
  }, [])
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
      selectEvent(null)
      setExpandedCluster(null)
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
    if (!mapReady || !map.current || !mapApi.current || userLatitude === undefined || userLongitude === undefined) return
    const element = document.createElement('div')
    element.className = styles.userLocation
    element.setAttribute('role', 'img')
    element.setAttribute('aria-label', 'Моё местоположение')
    const child = new mapApi.current.YMapMarker({ coordinates: [userLongitude, userLatitude] }, element)
    map.current.addChild(child)
    return () => { map.current?.removeChild(child) }
  }, [mapReady, userLatitude, userLongitude])

  useEffect(() => {
    if (!mapReady || !map.current || !mapApi.current) return
    for (const marker of markers.current.values()) {
      if (marker.item.kind === 'cluster') marker.element.style.display = marker.item.id === expandedCluster?.id ? 'none' : ''
    }
    const cluster = visibleItems.find((item): item is MapCluster => item.kind === 'cluster' && item.id === expandedCluster?.id)
    if (!cluster || !expandedCluster || cluster.members.length === 0) return
    const pageCount = Math.ceil(cluster.members.length / SPIDER_PAGE_SIZE)
    const page = Math.min(expandedCluster.page, pageCount - 1)
    const members = cluster.members.slice(page * SPIDER_PAGE_SIZE, (page + 1) * SPIDER_PAGE_SIZE)
    const root = document.createElement('div')
    root.className = styles.expandedCluster
    root.setAttribute('role', 'group')
    root.setAttribute('aria-label', `Кластер: ${cluster.count} событий`)
    const world = 256 * 2 ** integerZoom
    const anchor = mapPoint(cluster.longitude, cluster.latitude, integerZoom)
    const cameraPoint = mapPoint(cameraRef.current.center[0], cameraRef.current.center[1], integerZoom)
    const screenX = viewportSize.width / 2 + wrappedPixelDelta(anchor[0] - cameraPoint[0], world)
    const screenY = viewportSize.height / 2 + anchor[1] - cameraPoint[1]
    const markerMargin = SPIDER_RADIUS + MAX_OVERLAP_DIAMETER / 2 + EXPANDED_CONTROL_GAP
    const bottomMargin = pageCount > 1 ? EXPANDED_NEXT_OFFSET + EXPANDED_CONTROL_SIZE / 2 + EXPANDED_CONTROL_GAP : markerMargin
    const shiftX = Math.max(markerMargin, Math.min(viewportSize.width - markerMargin, screenX)) - screenX
    const shiftY = Math.max(markerMargin, Math.min(viewportSize.height - bottomMargin, screenY)) - screenY
    for (const [index, member] of members.entries()) {
      const button = createEventMarkerElement(member.event, () => selectEvent(member.event))
      const angle = 2 * Math.PI * index / members.length - Math.PI / 2
      const size = markerSizeForZoom(integerZoom)
      button.style.setProperty('--marker-size', `${size}px`)
      button.style.left = `${shiftX + Math.cos(angle) * SPIDER_RADIUS}px`
      button.style.top = `${shiftY + Math.sin(angle) * SPIDER_RADIUS}px`
      button.dataset.active = String(member.id === selectedEventId)
      button.setAttribute('aria-pressed', String(member.id === selectedEventId))
      const image = button.querySelector('img')
      if (image) { image.width = size; image.height = size }
      root.append(button)
    }
    const close = document.createElement('button')
    close.type = 'button'
    close.className = styles.expandedClose
    close.setAttribute('aria-label', 'Закрыть кластер')
    close.textContent = '×'
    close.style.left = `${shiftX}px`
    close.style.top = `${shiftY}px`
    close.addEventListener('click', () => setExpandedCluster(null))
    root.append(close)
    if (pageCount > 1) {
      const next = document.createElement('button')
      next.type = 'button'
      next.className = styles.expandedNext
      next.setAttribute('aria-label', `Следующие события, страница ${page + 1} из ${pageCount}`)
      next.textContent = `${page + 1}/${pageCount} ›`
      next.style.left = `${shiftX}px`
      next.style.top = `${shiftY + EXPANDED_NEXT_OFFSET}px`
      next.addEventListener('click', () => setExpandedCluster({ id: cluster.id, page: (page + 1) % pageCount }))
      root.append(next)
    }
    const child = new mapApi.current.YMapMarker({ coordinates: [cluster.longitude, cluster.latitude] }, root)
    map.current.addChild(child)
    return () => { map.current?.removeChild(child) }
  }, [expandedCluster, integerZoom, mapReady, selectEvent, selectedEventId, viewportSize, visibleItems])

  useEffect(() => {
    if (!mapReady || !map.current || !mapApi.current) return
    const activeOverlaps = overlaps.current
    for (const record of activeOverlaps.values()) {
      map.current.removeChild(record.child)
      map.current.removeChild(record.closeChild)
      for (const member of record.members) {
        member.element.style.display = ''
        member.element.style.position = ''
        member.element.style.left = ''
        member.element.style.top = ''
      }
    }
    activeOverlaps.clear()
    if (integerZoom < 22) return
    const eventMarkers = [...markers.current.values()].filter((marker) => marker.item.kind === 'event')
    for (const [index, members] of overlappingGroups(eventMarkers, integerZoom).entries()) {
      const id = members.map((member) => member.item.id).sort().join(':')
      const first = members[0].item
      const world = 256 * 2 ** integerZoom
      const anchor = mapPoint(first.longitude, first.latitude, integerZoom)
      const cameraPoint = mapPoint(cameraRef.current.center[0], cameraRef.current.center[1], integerZoom)
      const screenX = viewportSize.width / 2 + wrappedPixelDelta(anchor[0] - cameraPoint[0], world)
      const screenY = viewportSize.height / 2 + anchor[1] - cameraPoint[1]
      const margin = SPIDER_RADIUS + MAX_OVERLAP_DIAMETER / 2 + 8
      const shiftX = Math.max(margin, Math.min(viewportSize.width - margin, screenX)) - screenX
      const shiftY = Math.max(margin, Math.min(viewportSize.height - margin, screenY)) - screenY
      let page = 0
      const close = document.createElement('button')
      close.type = 'button'
      close.className = styles.overlapClose
      close.setAttribute('aria-label', 'Закрыть кластер')
      close.textContent = '×'
      close.style.display = 'none'
      const element = overlapElement(members.length, () => {
        const pageCount = Math.ceil(members.length / SPIDER_PAGE_SIZE)
        page = (page + 1) % (pageCount + 1)
        const pageStart = page === 0 ? -1 : (page - 1) * SPIDER_PAGE_SIZE
        element.textContent = page === 0 ? String(members.length) : `${page}/${pageCount}`
        close.style.display = page === 0 ? 'none' : 'grid'
        element.setAttribute('aria-label', page === 0
          ? `Показать первые ${Math.min(SPIDER_PAGE_SIZE, members.length)} из ${members.length} событий`
          : page === pageCount
            ? `Показать последние события, ${members.length} всего`
            : `Показать события ${pageStart + 1}–${Math.min(pageStart + SPIDER_PAGE_SIZE, members.length)} из ${members.length}`)
        for (const [memberIndex, member] of members.entries()) {
          const visible = page > 0 && memberIndex >= pageStart && memberIndex < pageStart + SPIDER_PAGE_SIZE
          member.element.style.display = visible ? 'grid' : 'none'
          member.element.style.position = 'relative'
          const pageIndex = memberIndex - pageStart
          const pageCount = Math.min(SPIDER_PAGE_SIZE, members.length - pageStart)
          const angle = 2 * Math.PI * pageIndex / pageCount - Math.PI / 2
          const point = mapPoint(member.item.longitude, member.item.latitude, integerZoom)
          const originX = wrappedPixelDelta(point[0] - anchor[0], world)
          const originY = point[1] - anchor[1]
          member.element.style.left = visible ? `${shiftX + Math.cos(angle) * SPIDER_RADIUS - originX}px` : '0px'
          member.element.style.top = visible ? `${shiftY + Math.sin(angle) * SPIDER_RADIUS - originY}px` : '0px'
        }
      })
      close.addEventListener('click', () => {
        page = 0
        element.textContent = String(members.length)
        element.setAttribute('aria-label', `Показать ${members.length} событий в этой точке`)
        close.style.display = 'none'
        for (const member of members) member.element.style.display = 'none'
      })
      for (const member of members) member.element.style.display = 'none'
      const child = new mapApi.current.YMapMarker({ coordinates: [first.longitude, first.latitude] }, element)
      const closeChild = new mapApi.current.YMapMarker({ coordinates: [first.longitude, first.latitude] }, close)
      const record: OverlapRecord = { child, closeChild, element, members }
      map.current.addChild(child)
      map.current.addChild(closeChild)
      activeOverlaps.set(`${id}:${index}`, record)
    }
    return () => {
      for (const record of activeOverlaps.values()) {
        map.current?.removeChild(record.child)
        map.current?.removeChild(record.closeChild)
        for (const member of record.members) {
          member.element.style.display = ''
          member.element.style.position = ''
          member.element.style.left = ''
          member.element.style.top = ''
        }
      }
      activeOverlaps.clear()
    }
  }, [integerZoom, itemsFilterKey, mapReady, viewportSize, visibleItems])

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
  </div>
}
