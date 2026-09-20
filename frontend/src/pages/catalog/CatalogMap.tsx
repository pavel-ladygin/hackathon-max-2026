import { useQueries } from '@tanstack/react-query'
import { CircleMarker, MapContainer, Popup, TileLayer } from 'react-leaflet'
import { useNavigate } from 'react-router-dom'
import 'leaflet/dist/leaflet.css'
import { apiClient } from '../../shared/api/client'
import type { EventCard } from '../../shared/api/types'
import { Empty, Loading } from '../../shared/ui'
import styles from '../pages.module.css'

export function CatalogMap({ events }: { events: EventCard[] }) {
  const navigate = useNavigate()
  const details = useQueries({ queries: events.map((event) => ({ queryKey: ['event', event.id], queryFn: () => apiClient.getEvent(event.id), staleTime: 60_000 })) })
  if (details.some((query) => query.isPending)) return <Loading label="Готовим карту…" />
  const points = details.flatMap((query) => query.data ? [query.data] : [])
  if (points.length === 0) return <Empty title="Карта сейчас недоступна" description="Переключитесь на список — все события доступны там." />
  return <div className={styles.mapWrap} aria-label="Карта событий">
    <MapContainer className={styles.map} center={[55.751244, 37.618423]} zoom={11} scrollWheelZoom={false}>
      <TileLayer attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>' url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png" />
      {points.map((event) => <CircleMarker key={event.id} center={[event.venue.latitude, event.venue.longitude]} radius={10} pathOptions={{ color: '#171716', fillColor: '#ff5b5b', fillOpacity: .9 }}><Popup><strong>{event.title}</strong><br />{event.venue.name}<br /><button type="button" onClick={() => navigate(`/events/${event.id}`)}>Открыть событие</button></Popup></CircleMarker>)}
    </MapContainer>
  </div>
}
