import { useQueries } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiClient } from '../../shared/api/client'
import type { EventCard } from '../../shared/api/types'
import { Empty, Loading } from '../../shared/ui'
import styles from './catalogMap.module.css'
import { createEventMarkerElement } from './eventMarker'
import { loadYandexMaps, markerSizeForZoom, type YandexMap, type YandexMapUpdateEvent } from './yandexMaps'

const MOSCOW_CENTER: [number, number] = [37.618423, 55.751244]

function readZoom(event: YandexMapUpdateEvent) {
  return event.location?.zoom ?? event.camera?.zoom
}

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

      const { YMap, YMapDefaultFeaturesLayer, YMapDefaultSchemeLayer, YMapListener, YMapMarker } = ymaps3
      const nextMap = new YMap(mapNode.current, { location: { center: MOSCOW_CENTER, zoom: 11 } })
      nextMap.addChild(new YMapDefaultSchemeLayer())
      nextMap.addChild(new YMapDefaultFeaturesLayer())

      const markers: HTMLElement[] = []

      for (const [index, event] of points.entries()) {
        const { latitude, longitude } = event.venue
        if (latitude == null || longitude == null) continue

        const marker = createEventMarkerElement(event, () => {
          for (const item of markers) item.dataset.active = 'false'
          marker.dataset.active = 'true'
          navigate(`/events/${event.id}`)
        }, Math.min(index * 20, 120))

        markers.push(marker)
        nextMap.addChild(
          new YMapMarker({ coordinates: [longitude, latitude] }, marker),
        )
      }

      const updateMarkers = (update: YandexMapUpdateEvent) => {
        const zoom = readZoom(update)
        if (zoom === undefined || Number.isNaN(zoom)) return
        const size = markerSizeForZoom(zoom)
        for (const marker of markers) {
          marker.style.setProperty('--marker-size', `${size}px`)
          const image = marker.querySelector('img')
          if (image) {
            image.width = size
            image.height = size
          }
        }
      }
      nextMap.addChild(new YMapListener({ onUpdate: updateMarkers }))
      updateMarkers({ location: { zoom: 11 } })

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
  const hasMappablePoints = points.some(
    (event) =>
      event.venue.latitude != null &&
      event.venue.longitude != null,
  )
  if (!hasMappablePoints || !apiKey || mapError) {
    return <Empty title="Карта сейчас недоступна" description={!apiKey ? 'Для карты не настроен API-ключ.' : 'Переключитесь на список — все события доступны там.'} />
  }

  return <div className={styles.mapWrap} aria-label="Карта событий">
    <div ref={mapNode} className={styles.map} role="region" aria-label="Яндекс Карта событий" />
  </div>
}
