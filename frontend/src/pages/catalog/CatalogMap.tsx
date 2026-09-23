import { useInfiniteQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiClient } from '../../shared/api/client'
import type { CategorySlug, EventCard } from '../../shared/api/types'
import { Button, Empty, ScreenSkeleton } from '../../shared/ui'
import styles from './catalogMap.module.css'
import { createEventMarkerElement } from './eventMarker'
import { loadYandexMaps, markerSizeForZoom, radiusForViewport, type YandexCamera, type YandexMap, type YandexMapUpdateEvent, type YandexMapsApi } from './yandexMaps'
import { collectMapEvents, shouldFetchMapPage } from './mapResults'
import { useDebouncedValue } from './useDebouncedValue'

const MOSCOW_CENTER: [number, number] = [37.618423, 55.751244]
const MAP_PAGE_SIZE = 50
const MAP_EVENT_CAP = 150

type MapFilters = {
  q?: string
  category_slugs?: CategorySlug[]
  date_from?: string
  date_to?: string
  price_max_minor?: number
  free_only?: boolean
}

type LocatedEvent = EventCard & { latitude?: number | null; longitude?: number | null }

function readCamera(event: YandexMapUpdateEvent): YandexCamera | null {
  const location = event.location ?? event.camera
  const center = location?.center
  const zoom = location?.zoom
  if (!center || zoom === undefined || !Number.isFinite(zoom)) return null
  return { center, zoom }
}

function appendMarker(mapApi: YandexMapsApi, map: YandexMap, event: LocatedEvent, index: number, navigate: (path: string) => void) {
  const { latitude, longitude } = event
  if (latitude == null || longitude == null || !Number.isFinite(latitude) || !Number.isFinite(longitude)) return null
  const marker = createEventMarkerElement(event, () => {
    marker.dataset.active = 'true'
    navigate(`/events/${event.id}`)
  }, Math.min(index * 20, 120))
  const child = new mapApi.YMapMarker({ coordinates: [longitude, latitude] }, marker)
  map.addChild(child)
  return { child, element: marker }
}

export function CatalogMap({ filters, initialCenter }: { filters: MapFilters; initialCenter?: [number, number] }) {
  const navigate = useNavigate()
  const mapNode = useRef<HTMLDivElement>(null)
  const map = useRef<YandexMap | null>(null)
  const mapApi = useRef<YandexMapsApi | null>(null)
  const markers = useRef(new Map<string, { child: unknown; element: HTMLElement } & { event: LocatedEvent }>())
  const centerLng = initialCenter?.[0]
  const centerLat = initialCenter?.[1]
  const initial = useMemo(() => centerLng === undefined || centerLat === undefined ? MOSCOW_CENTER : [centerLng, centerLat] as [number, number], [centerLat, centerLng])
  const [camera, setCamera] = useState<YandexCamera>({ center: initial, zoom: 11 })
  const [viewportSize, setViewportSize] = useState({ width: 700, height: 500 })
  const stableCamera = useDebouncedValue(camera, 350)
  const [mapError, setMapError] = useState(false)
  const [mapReady, setMapReady] = useState(false)
  const apiKey = import.meta.env.VITE_YANDEX_MAPS_API_KEY
  const params = useMemo(() => ({
    ...filters,
    lat: stableCamera.center[1],
    lng: stableCamera.center[0],
    distance_m: radiusForViewport(stableCamera.zoom, viewportSize.width, viewportSize.height),
    limit: MAP_PAGE_SIZE,
  }), [filters, stableCamera, viewportSize])
  const results = useInfiniteQuery({
    queryKey: ['event-search', 'map', params],
    queryFn: ({ pageParam }) => apiClient.searchEvents({ ...params, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  })
  const locatedEvents = useMemo(() => collectMapEvents(results.data?.pages as Array<{ items: LocatedEvent[] }> | undefined), [results.data])
  const fetchNextPage = results.fetchNextPage
  const hasNextPage = results.hasNextPage
  const isFetching = results.isFetching
  const isPending = results.isPending

  useEffect(() => {
    if (!shouldFetchMapPage(locatedEvents.length, MAP_EVENT_CAP, Boolean(hasNextPage), isFetching || isPending)) return
    void fetchNextPage()
  }, [fetchNextPage, hasNextPage, isFetching, isPending, locatedEvents.length])

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
      resizeObserver = new ResizeObserver(([entry]) => {
        setViewportSize({ width: entry.contentRect.width, height: entry.contentRect.height })
      })
      resizeObserver.observe(element)
      nextMap.addChild(new YMapDefaultSchemeLayer())
      nextMap.addChild(new YMapDefaultFeaturesLayer())
      nextMap.addChild(new YMapListener({ onUpdate: (event) => {
        const nextCamera = readCamera(event)
        if (!nextCamera) return
        setCamera((current) => current.center[0] === nextCamera.center[0] && current.center[1] === nextCamera.center[1] && current.zoom === nextCamera.zoom ? current : nextCamera)
        const size = markerSizeForZoom(nextCamera.zoom)
        for (const { element } of markerRegistry.values()) {
          element.style.setProperty('--marker-size', `${size}px`)
          const image = element.querySelector('img')
          if (image) { image.width = size; image.height = size }
        }
      } }))
      mapApi.current = ymaps3
      map.current = nextMap
      setCamera({ center: initial, zoom: 11 })
      setMapReady(true)
    }).catch(() => { if (!cancelled) setMapError(true) })
    return () => {
      cancelled = true
      resizeObserver?.disconnect()
      map.current?.destroy()
      map.current = null
      mapApi.current = null
      setMapReady(false)
      markerRegistry.clear()
    }
  }, [apiKey, initial])

  useEffect(() => {
    if (!mapReady || !map.current || !mapApi.current) return
    const nextIds = new Set(locatedEvents.map((event) => event.id))
    for (const [id, marker] of markers.current) {
      if (nextIds.has(id)) continue
      map.current.removeChild(marker.child)
      markers.current.delete(id)
    }
    locatedEvents.forEach((event, index) => {
      const existing = markers.current.get(event.id)
      if (existing) {
        if (existing.event.latitude === event.latitude && existing.event.longitude === event.longitude) return
        map.current?.removeChild(existing.child)
        markers.current.delete(event.id)
      }
      const marker = appendMarker(mapApi.current!, map.current!, event, index, navigate)
      if (marker) markers.current.set(event.id, { ...marker, event })
    })
  }, [locatedEvents, mapReady, navigate])

  if (!apiKey || mapError) return <Empty title="Карта сейчас недоступна" description={!apiKey ? 'Для карты не настроен API-ключ.' : 'Переключитесь на список — события доступны там.'} />

  const hasAnyCoordinates = locatedEvents.some((event) => event.latitude != null && event.longitude != null)
  const noResults = !results.isPending && !results.isFetchingNextPage && !results.hasNextPage && locatedEvents.length === 0
  return <div className={styles.mapWrap} aria-label="Карта событий">
    <div ref={mapNode} className={styles.map} role="region" aria-label="Яндекс Карта событий" />
    {results.isPending ? <ScreenSkeleton variant="map" inline label="Ищем события рядом…" /> : null}
    {results.isError ? <div className={`${styles.mapMessage} ${styles.mapMessageInteractive}`}><Empty inline title="События не загрузились" description="Проверьте соединение и повторите поиск." action={<Button onClick={() => void results.refetch()}>Повторить</Button>} /></div> : null}
    {noResults ? <div className={`${styles.mapMessage} ${styles.mapMessagePassive}`}><Empty inline title="В этой области событий не найдено" description="Переместите карту или измените фильтры." /></div> : null}
    {!results.isPending && locatedEvents.length > 0 && !hasAnyCoordinates ? <div className={`${styles.mapMessage} ${styles.mapMessagePassive}`}><Empty inline title="Для этих событий нет координат" description="Попробуйте изменить область карты или переключиться на список." /></div> : null}
    {locatedEvents.length >= MAP_EVENT_CAP && results.hasNextPage ? <span className={styles.mapLoading} role="status">Показаны первые {MAP_EVENT_CAP}. Приблизьте карту или переместите её, чтобы увидеть остальные.</span> : null}
    {results.isFetchingNextPage ? <span className={styles.mapLoading} role="status">Загружаем события…</span> : null}
  </div>
}
