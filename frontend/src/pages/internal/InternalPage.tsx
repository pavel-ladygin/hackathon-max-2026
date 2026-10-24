import { lazy, Suspense } from 'react'

const AnalyticsDashboard = lazy(() => import('./AnalyticsDashboard').then((module) => ({ default: module.AnalyticsDashboard })))
const EventSourcesPage = lazy(() => import('./EventSourcesPage').then((module) => ({ default: module.EventSourcesPage })))
const EventModerationPage = lazy(() => import('./EventModerationPage').then((module) => ({ default: module.EventModerationPage })))

export function InternalPage({ page }: { page: 'analytics' | 'sources' | 'moderation' }) {
  return (
    <Suspense fallback={<p role="status">Загрузка…</p>}>
      {page === 'analytics' ? <AnalyticsDashboard /> : page === 'sources' ? <EventSourcesPage /> : <EventModerationPage />}
    </Suspense>
  )
}
