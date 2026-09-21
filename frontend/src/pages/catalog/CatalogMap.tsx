import { useQueries } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiClient } from '../../shared/api/client'
import type { EventCard } from '../../shared/api/types'
import { Empty, Loading } from '../../shared/ui'
import styles from '../pages.module.css'
import { loadYandexMaps, type YandexMap } from './yandexMaps'

const MOSCOW_CENTER: [number, number] = [37.618423, 55.751244]

export function CatalogMap({ events }: { events: EventCard[] }) {
  const navigate = useNavigate()
  const mapNode = useRef<HTMLDivElement>(null)
  const map = useRef<YandexMap | null>(null)
  const details = useQueries({ queries: events.map((event) => ({ queryKey: ['event', event.id], queryFn: () => apiClient.getEvent(event.id), staleTime: 60_000 })) })
  const [mapError, setMapError] = useState(false)
  const apiKey = import.meta.env.VITE_YANDEX_MAPS_API_KEY

  useEffect(() => {
    if (!mapNode.current || !apiKey || details.some((query) => query.isPending)) return

    const points = details.flatMap((query) => query.data ? [query.data] : [])
    if (points.length === 0) return

    let cancelled = false
    void loadYandexMaps(apiKey).then((ymaps3) => {
      if (cancelled || !mapNode.current) return

      const { YMap, YMapDefaultFeaturesLayer, YMapDefaultSchemeLayer, YMapMarker } = ymaps3
      const nextMap = new YMap(mapNode.current, { location: { center: MOSCOW_CENTER, zoom: 11 } })
      nextMap.addChild(new YMapDefaultSchemeLayer())
      nextMap.addChild(new YMapDefaultFeaturesLayer())

      for (const event of points) {
        const marker = document.createElement('button')
        marker.type = 'button'
        marker.className = styles.mapMarker
        marker.title = event.title
        marker.setAttribute('aria-label', `Открыть событие «${event.title}»`)
        marker.textContent = '•'
        marker.addEventListener('click', () => navigate(`/events/${event.id}`))
        nextMap.addChild(new YMapMarker({ coordinates: [event.venue.longitude, event.venue.latitude] }, marker))
      }

      map.current = nextMap
    }).catch(() => {
      if (!cancelled) setMapError(true)
    })

    return () => {
      cancelled = true
      map.current?.destroy()
      map.current = null
    }
  }, [apiKey, details, navigate])

  if (details.some((query) => query.isPending)) return <Loading label="Готовим карту…" />
  const points = details.flatMap((query) => query.data ? [query.data] : [])
  if (points.length === 0 || !apiKey || mapError) {
    return <Empty title="Карта сейчас недоступна" description={!apiKey ? 'Для карты не настроен API-ключ.' : 'Переключитесь на список — все события доступны там.'} />
  }

  return <div className={styles.mapWrap} aria-label="Карта событий">
    <div ref={mapNode} className={styles.map} role="application" aria-label="Яндекс Карта событий" />
  </div>
}
