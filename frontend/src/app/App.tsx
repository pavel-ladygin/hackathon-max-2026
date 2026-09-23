import { lazy, Suspense, useEffect, useRef } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useBootstrap } from '../features/auth/useBootstrap'
import { OpenInMaxPage } from '../pages/system/OpenInMaxPage'
import { Button, ErrorState, ScreenSkeleton } from '../shared/ui/index'
import { maxPlatform } from '../shared/platform/max/adapter'
import { AppProviders } from './providers'
import { skeletonVariant } from './skeletonVariant'

const HomePage = lazy(() => import('../pages/home/HomePage').then((m) => ({ default: m.HomePage })))
const EventPage = lazy(() => import('../pages/event/EventPage').then((m) => ({ default: m.EventPage })))
const OnboardingPage = lazy(() => import('../pages/onboarding/OnboardingPage').then((m) => ({ default: m.OnboardingPage })))
const CatalogPage = lazy(() => import('../pages/catalog/CatalogPage').then((m) => ({ default: m.CatalogPage })))
const SavedPage = lazy(() => import('../pages/saved/SavedPage').then((m) => ({ default: m.SavedPage })))
const PreferencesPage = lazy(() => import('../pages/preferences/PreferencesPage').then((m) => ({ default: m.PreferencesPage })))
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
  const bootstrap = useBootstrap()
  const inviteContext = bootstrap.data?.inviteContext
  const inviteToken = location.pathname.match(/^\/join\/([^/]+)$/)?.[1]
  if (import.meta.env.PROD && !maxPlatform.isMax) return <OpenInMaxPage startParam={inviteToken ? decodeInviteToken(inviteToken) : null} />
  if (bootstrap.isPending) return <ScreenSkeleton variant={skeletonVariant(location.pathname)} label="Знакомимся с вами…" />
  if (bootstrap.isError) return <ErrorState title="Не удалось открыть приложение" description="Проверьте соединение и повторите запуск." action={<Button onClick={() => void bootstrap.refetch()}>Повторить</Button>} />

  const isOnboarding = location.pathname.startsWith('/onboarding')
  if (bootstrap.data.onboardingState === 'new' && location.pathname === '/') {
    return <Navigate to="/onboarding/interests" replace />
  }
  if (bootstrap.data.onboardingState === 'complete' && isOnboarding) {
    return <Navigate to="/" replace />
  }
  if (location.pathname === '/' && inviteContext?.token && !inviteContext.already_joined) {
    return <Navigate to={`/join/${encodeURIComponent(inviteContext.token)}`} replace />
  }

  return (
    <Suspense fallback={<ScreenSkeleton variant={skeletonVariant(location.pathname)} label="Открываем экран…" />}>
    <Routes>
      <Route path="/open-in-max" element={<OpenInMaxPage />} />
      <Route path="/onboarding/:step" element={<OnboardingPage />} />
      <Route path="/" element={<HomePage />} />
      <Route path="/events" element={<CatalogPage />} />
      <Route path="/catalog" element={<Navigate to="/events" replace />} />
      <Route path="/events/:eventId" element={<EventPage />} />
      <Route path="/saved" element={<SavedPage />} />
      <Route path="/preferences" element={<PreferencesPage />} />
      <Route path="/rooms/new" element={<NewRoomPage />} />
      <Route path="/rooms/:roomId/invite" element={<InvitePage />} />
      <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} />
      <Route path="/join/:inviteToken" element={<JoinPage />} />
      <Route path="/rooms/*" element={<RoomUnavailablePage />} />
      <Route path="/join/*" element={<RoomUnavailablePage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
    </Suspense>
  )
}

function decodeInviteToken(token: string) {
  try { return decodeURIComponent(token) } catch { return token }
}

export function App() {
  return <AppProviders><MaxBridgeReady /><AppRoutes /></AppProviders>
}
