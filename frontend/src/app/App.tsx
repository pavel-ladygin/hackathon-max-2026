import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useBootstrap } from '../features/auth/useBootstrap'
import { OpenInMaxPage } from '../pages/system/OpenInMaxPage'
import { Button, DemoBadge, ErrorState, Loading } from '../shared/ui/index'
import { maxPlatform } from '../shared/platform/max/adapter'
import { AppProviders } from './providers'
import { MockScenarioPanel } from '../mocks/MockScenarioPanel'

const HomePage = lazy(() => import('../pages/home/HomePage').then((m) => ({ default: m.HomePage })))
const EventPage = lazy(() => import('../pages/event/EventPage').then((m) => ({ default: m.EventPage })))
const OnboardingPage = lazy(() => import('../pages/onboarding/OnboardingPage').then((m) => ({ default: m.OnboardingPage })))
const CatalogPage = lazy(() => import('../pages/catalog/CatalogPage').then((m) => ({ default: m.CatalogPage })))
const SavedPage = lazy(() => import('../pages/saved/SavedPage').then((m) => ({ default: m.SavedPage })))
const PreferencesPage = lazy(() => import('../pages/preferences/PreferencesPage').then((m) => ({ default: m.PreferencesPage })))
const roomPages = () => import('../pages/rooms/RoomPages')
const NewRoomPage = lazy(() => roomPages().then((m) => ({ default: m.NewRoomPage })))
const InvitePage = lazy(() => roomPages().then((m) => ({ default: m.InvitePage })))
const JoinPage = lazy(() => roomPages().then((m) => ({ default: m.JoinPage })))
const RoomFlowPage = lazy(() => roomPages().then((m) => ({ default: m.RoomFlowPage })))

function AppRoutes() {
  const location = useLocation()
  const bootstrap = useBootstrap()
  const mockMode = (import.meta.env.VITE_API_MODE ?? 'mock') === 'mock'

  if (import.meta.env.PROD && !mockMode && !maxPlatform.isMax) return <OpenInMaxPage />
  if (bootstrap.isPending) return <Loading label="Знакомимся с вами…" />
  if (bootstrap.isError) return <ErrorState title="Не удалось открыть приложение" description="Проверьте соединение и повторите запуск." action={<Button onClick={() => void bootstrap.refetch()}>Повторить</Button>} />

  const isOnboarding = location.pathname.startsWith('/onboarding')
  if (bootstrap.data.onboardingState === 'new' && location.pathname === '/') {
    return <Navigate to="/onboarding/interests" replace />
  }
  if (bootstrap.data.onboardingState === 'complete' && bootstrap.data.inviteContext && location.pathname === '/') {
    return <Navigate to={`/join/${bootstrap.data.inviteContext.token}`} replace />
  }
  if (bootstrap.data.onboardingState === 'complete' && isOnboarding) {
    return <Navigate to="/" replace />
  }

  return (
    <Suspense fallback={<Loading label="Открываем экран…" />}>
    {mockMode ? <DemoBadge /> : null}
    {mockMode && import.meta.env.DEV ? <MockScenarioPanel /> : null}
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
      <Route path="/join/:inviteToken" element={<JoinPage />} />
      <Route path="/rooms/:roomId" element={<RoomFlowPage />} />
      <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
    </Suspense>
  )
}

export function App() {
  return <AppProviders><AppRoutes /></AppProviders>
}
