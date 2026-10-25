import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useBootstrap } from '../features/auth/useBootstrap'
import { OpenInMaxPage } from '../pages/system/OpenInMaxPage'
import { BottomNav, Button, ErrorState, ScreenSkeleton } from '../shared/ui/index'
import { maxPlatform } from '../shared/platform/max/adapter'
import { AppProviders } from './providers'
import { skeletonVariant } from './skeletonVariant'
import { activateAnalytics, trackAppOpen, trackClientError, trackPerformance } from '../shared/analytics/client'
import { getOpenInMaxStartParam } from './sharedEventLink'

const HomePage = lazy(() => import('../pages/home/HomePage').then((m) => ({ default: m.HomePage })))
const EventPage = lazy(() => import('../pages/event/EventPage').then((m) => ({ default: m.EventPage })))
const OnboardingPage = lazy(() => import('../pages/onboarding/OnboardingPage').then((m) => ({ default: m.OnboardingPage })))
const CatalogPage = lazy(() => import('../pages/catalog/CatalogPage').then((m) => ({ default: m.CatalogPage })))
const SavedPage = lazy(() => import('../pages/saved/SavedPage').then((m) => ({ default: m.SavedPage })))
const PreferencesPage = lazy(() => import('../pages/preferences/PreferencesPage').then((m) => ({ default: m.PreferencesPage })))
const EventSubmissionPage = lazy(() => import('../pages/submissions/EventSubmissionPages').then((m) => ({ default: m.EventSubmissionPage })))
const MyEventSubmissionsPage = lazy(() => import('../pages/submissions/EventSubmissionPages').then((m) => ({ default: m.MyEventSubmissionsPage })))
const RoomUnavailablePage = lazy(() => import('../pages/rooms/RoomUnavailablePage').then((m) => ({ default: m.RoomUnavailablePage })))
const NewRoomPage = lazy(() => import('../pages/rooms/RoomPages').then((m) => ({ default: m.NewRoomPage })))
const InvitePage = lazy(() => import('../pages/rooms/RoomPages').then((m) => ({ default: m.InvitePage })))
const JoinPage = lazy(() => import('../pages/rooms/RoomPages').then((m) => ({ default: m.JoinPage })))
const RoomFlowPage = lazy(() => import('../pages/rooms/RoomPages').then((m) => ({ default: m.RoomFlowPage })))

function MaxBridgeReady() {
  const called = useRef(false)
  useEffect(() => {
    if (called.current) return
    called.current = true
    maxPlatform.ready()
  }, [])
  return null
}

function AppRoutes() {
  const location = useLocation()
  const navigate = useNavigate()
  const appOpened = useRef(false)
  const [sharedEventDismissed, setSharedEventDismissed] = useState(false)
  const bootStartedAt = useRef<number | null>(null)
  const bootFailure = useRef<unknown>(null)
  const consumedLaunchRevision = useRef<number | null>(null)
  const pendingLaunchPath = useRef<{ revision: number; from: string } | null>(null)
  const bootstrap = useBootstrap()
  const sharedEventId = bootstrap.data?.sharedEventId
  useEffect(() => { bootStartedAt.current = performance.now() }, [])
  useEffect(() => {
    if (bootstrap.isError && !bootFailure.current) bootFailure.current = bootstrap.error
    if (!bootstrap.data || appOpened.current) return
    appOpened.current = true
    activateAnalytics(bootstrap.data.user.id)
    const path = location.pathname
    const entryPoint = path.startsWith('/join/') ? 'room_invite' : path.startsWith('/rooms/') ? 'room' : path.startsWith('/events/') ? 'deep_link' : path === '/' || path === '/events' ? 'feed' : path === '/saved' ? 'saved' : 'home'
    trackAppOpen(entryPoint)
    trackPerformance('app_boot', performance.now() - (bootStartedAt.current ?? performance.now()))
    if (bootFailure.current) trackClientError('app_boot', bootFailure.current)
    bootFailure.current = null
  }, [bootstrap.data, bootstrap.error, bootstrap.isError, location.pathname])
  useEffect(() => {
    if (!bootstrap.data || bootstrap.data.onboardingState !== 'complete') return
    // StrictMode can repeat this effect before navigation commits. Do not let
    // the onboarding fallback replace the room selected by the same launch.
    if (pendingLaunchPath.current?.revision === bootstrap.launchRevision && pendingLaunchPath.current.from === location.pathname) return
    pendingLaunchPath.current = null
    if (consumedLaunchRevision.current !== bootstrap.launchRevision) {
      consumedLaunchRevision.current = bootstrap.launchRevision
      const invite = bootstrap.data.inviteContext
      if (invite?.token && !invite.already_joined) {
        const target = `/join/${encodeURIComponent(invite.token)}`
        if (location.pathname !== target) {
          pendingLaunchPath.current = { revision: bootstrap.launchRevision, from: location.pathname }
          navigate(target, { replace: true })
        }
        return
      }
      if (invite?.already_joined) {
        const roomPath = `/rooms/${encodeURIComponent(invite.room_id)}`
        if (location.pathname !== roomPath && !location.pathname.startsWith(`${roomPath}/`)) {
          pendingLaunchPath.current = { revision: bootstrap.launchRevision, from: location.pathname }
          navigate(`${roomPath}/waiting`, { replace: true })
        }
        return
      }
    }
    if (location.pathname.startsWith('/onboarding')) navigate('/', { replace: true })
  }, [bootstrap.data, bootstrap.launchRevision, location.pathname, navigate])
  if (import.meta.env.PROD && !maxPlatform.isMax) return <OpenInMaxPage startParam={getOpenInMaxStartParam(location.pathname)} />
  if (bootstrap.isPending) return <ScreenSkeleton variant={skeletonVariant(location.pathname)} label="Знакомимся с вами…" />
  if (bootstrap.isError) return <ErrorState title="Не удалось открыть приложение" description="Проверьте соединение и повторите запуск." action={<Button onClick={() => void bootstrap.refetch()}>Повторить</Button>} />

  if (bootstrap.data.onboardingState === 'new' && location.pathname === '/') {
    return <Navigate to="/onboarding/interests" replace />
  }
  if (location.pathname === '/' && sharedEventId && !sharedEventDismissed) {
    return <Navigate to={`/events/${encodeURIComponent(sharedEventId)}`} replace state={{ sharedEventEntry: true }} />
  }

  return (
    <>
    <Suspense fallback={<ScreenSkeleton variant={skeletonVariant(location.pathname)} label="Открываем экран…" />}>
    <Routes>
      <Route path="/open-in-max" element={<OpenInMaxPage />} />
      <Route path="/onboarding/:step" element={<OnboardingPage />} />
      <Route path="/" element={<HomePage />} />
      <Route path="/events" element={<CatalogPage />} />
      <Route path="/catalog" element={<Navigate to="/events" replace />} />
      <Route path="/events/:eventId" element={<EventPage onLeaveSharedEvent={() => setSharedEventDismissed(true)} />} />
      <Route path="/saved" element={<SavedPage />} />
      <Route path="/preferences" element={<PreferencesPage />} />
      <Route path="/events/suggest" element={<EventSubmissionPage />} />
      <Route path="/my/event-submissions" element={<MyEventSubmissionsPage />} />
      <Route path="/rooms/new" element={<NewRoomPage />} />
      <Route path="/rooms/:roomId/invite" element={<InvitePage />} />
      <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} />
      <Route path="/join/:inviteToken" element={<JoinPage />} />
      <Route path="/rooms/*" element={<RoomUnavailablePage />} />
      <Route path="/join/*" element={<RoomUnavailablePage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
    </Suspense>
    {['/', '/events', '/saved'].includes(location.pathname) && <BottomNav activeId={location.pathname === '/' ? 'home' : location.pathname === '/events' ? 'catalog' : 'saved'} items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'catalog', label: 'Афиша', icon: 'calendar' }, { id: 'saved', label: 'Избранное', icon: 'saved' }]} onChange={(id) => navigate(id === 'home' ? '/' : id === 'catalog' ? '/events' : '/saved')} />}
    </>
  )
}

export function App() {
  return <AppProviders><MaxBridgeReady /><AppRoutes /></AppProviders>
}
