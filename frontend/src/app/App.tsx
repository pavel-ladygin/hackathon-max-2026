import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useBootstrap } from '../features/auth/useBootstrap'
import { EventPage } from '../pages/event/EventPage'
import { HomePage } from '../pages/home/HomePage'
import { OnboardingPage } from '../pages/onboarding/OnboardingPage'
import { InvitePage, JoinPage, NewRoomPage, RoomFlowPage } from '../pages/rooms/RoomPages'
import { OpenInMaxPage } from '../pages/system/OpenInMaxPage'
import { Loading } from '../shared/ui/index'
import { maxPlatform } from '../shared/platform/max/adapter'
import { AppProviders } from './providers'

function AppRoutes() {
  const location = useLocation()
  const bootstrap = useBootstrap()
  const mockMode = (import.meta.env.VITE_API_MODE ?? 'mock') === 'mock'

  if (import.meta.env.PROD && !mockMode && !maxPlatform.isMax) return <OpenInMaxPage />
  if (bootstrap.isPending) return <Loading label="Знакомимся с вами…" />
  if (bootstrap.isError) return <OpenInMaxPage error={import.meta.env.DEV ? `Ошибка bootstrap: ${String(bootstrap.error)}` : 'Не удалось открыть приложение. Проверьте соединение и попробуйте снова.'} />

  const isOnboarding = location.pathname.startsWith('/onboarding')
  if (bootstrap.data.onboardingState === 'new' && location.pathname === '/') {
    return <Navigate to="/onboarding/interests" replace />
  }
  if (bootstrap.data.onboardingState === 'complete' && isOnboarding) {
    return <Navigate to="/" replace />
  }

  return (
    <Routes>
      <Route path="/open-in-max" element={<OpenInMaxPage />} />
      <Route path="/onboarding/:step" element={<OnboardingPage />} />
      <Route path="/" element={<HomePage />} />
      <Route path="/events/:eventId" element={<EventPage />} />
      <Route path="/rooms/new" element={<NewRoomPage />} />
      <Route path="/rooms/:roomId/invite" element={<InvitePage />} />
      <Route path="/join/:inviteToken" element={<JoinPage />} />
      <Route path="/rooms/:roomId/:roomScreen" element={<RoomFlowPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export function App() {
  return <AppProviders><AppRoutes /></AppProviders>
}
