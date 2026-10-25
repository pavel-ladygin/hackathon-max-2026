import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode, useEffect, type ReactNode } from 'react'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { apiClient } from '../shared/api/client'
import { App } from './App'

type Bootstrap = Awaited<ReturnType<typeof apiClient.bootstrap>>
const state = vi.hoisted(() => ({ path: '/', data: null as Bootstrap | null, revision: 0, paths: [] as string[] }))
vi.mock('../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: state.data, launchRevision: state.revision, isPending: state.data === null, isError: false }) }))
vi.mock('../shared/platform/max/adapter', () => ({ maxPlatform: { isMax: true, ready: vi.fn() } }))
vi.mock('../shared/analytics/client', () => ({ activateAnalytics: vi.fn(), trackAppOpen: vi.fn(), trackClientError: vi.fn(), trackPerformance: vi.fn() }))
vi.mock('./providers', () => ({ AppProviders: ({ children }: { children: ReactNode }) => <MemoryRouter initialEntries={[state.path]}><RouteProbe />{children}</MemoryRouter> }))
vi.mock('../pages/home/HomePage', () => ({ HomePage: () => <p>Home</p> }))
vi.mock('../pages/catalog/CatalogPage', () => ({ CatalogPage: () => <p>Catalog</p> }))
vi.mock('../pages/onboarding/OnboardingPage', () => ({ OnboardingPage: () => <p>Onboarding</p> }))
vi.mock('../pages/rooms/RoomPages', () => ({ NewRoomPage: () => <p>New room</p>, InvitePage: () => <p>Invite</p>, JoinPage: () => <p>Join</p>, RoomFlowPage: () => <p>Room</p> }))

function RouteProbe() {
  const location = useLocation()
  const navigate = useNavigate()
  useEffect(() => { state.paths.push(location.pathname) }, [location.key, location.pathname])
  return <><output data-testid="route">{location.pathname}</output><button onClick={() => navigate('/events')}>Test catalog</button><button onClick={() => navigate('/onboarding/profile')}>Test onboarding</button></>
}

function bootstrap(token: string | null = null, alreadyJoined = false): Bootstrap {
  return {
    accessToken: 'test-session', user: { id: 'user-1', displayName: 'Анна', avatarUrl: null, cityId: null, locale: 'ru' },
    onboardingState: 'complete', preferences: null, dailyNotificationsEnabled: false, sharedEventId: null,
    inviteContext: token ? { token, room_id: 'room-a', room_name: 'Встреча', inviter: { id: 'creator', display_name: 'Павел', avatar_url: null, role: 'creator', intent_ready: true }, expires_at: '2099-01-01T00:00:00Z', already_joined: alreadyJoined, status: 'joinable' } : null,
  }
}

async function expectRoute(path: string) {
  await waitFor(() => expect(screen.getByTestId('route').textContent).toBe(path))
}

describe('BUG-02 centralized launch routing', () => {
  beforeEach(() => { state.path = '/events'; state.data = bootstrap(); state.revision = 0; state.paths = [] })
  afterEach(cleanup)

  it('opens the original invite after onboarding completes', async () => {
    state.path = '/onboarding/profile'
    state.data = { ...bootstrap('invite-a'), onboardingState: 'new' }
    const view = render(<App />)
    await expectRoute('/onboarding/profile')
    state.data = bootstrap('invite-a')
    view.rerender(<App />)
    await expectRoute('/join/invite-a')
    state.data = { ...state.data }
    view.rerender(<App />)
    expect(state.paths.filter(path => path === '/join/invite-a')).toHaveLength(1)
  })

  it('finishes onboarding without an invite at home', async () => {
    state.path = '/onboarding/profile'
    state.data = { ...bootstrap(), onboardingState: 'new' }
    const view = render(<App />)
    state.data = bootstrap()
    view.rerender(<App />)
    await expectRoute('/')
  })

  it('preserves a completed user invitation on a cold onboarding route in StrictMode', async () => {
    state.path = '/onboarding/profile'
    state.data = null
    const view = render(<StrictMode><App /></StrictMode>)
    state.data = bootstrap('invite-a')
    view.rerender(<StrictMode><App /></StrictMode>)
    await expectRoute('/join/invite-a')
  })

  it('redirects a completed user who revisits onboarding after consuming the launch', async () => {
    render(<App />)
    await expectRoute('/events')
    fireEvent.click(screen.getByRole('button', { name: 'Test onboarding' }))
    await expectRoute('/')
  })

  it('restores a member on a genuinely new warm launch', async () => {
    const view = render(<App />)
    await expectRoute('/events')
    state.revision = 1
    state.data = bootstrap('invite-a', true)
    view.rerender(<App />)
    await expectRoute('/rooms/room-a/waiting')
  })

  it('preserves the current room screen on a cold member reload', async () => {
    state.path = '/rooms/room-a/vote'
    state.data = bootstrap('invite-a', true)
    const view = render(<App />)
    await expectRoute('/rooms/room-a/vote')
    state.data = { ...state.data }
    view.rerender(<App />)
    await expectRoute('/rooms/room-a/vote')
  })

  it('keeps the current screen when a member reopens the same room', async () => {
    state.path = '/rooms/room-a/intent'
    const view = render(<App />)
    state.revision = 1
    state.data = bootstrap('invite-a', true)
    view.rerender(<App />)
    await expectRoute('/rooms/room-a/intent')
  })

  it('does not reopen an old invitation on an ordinary bootstrap refresh', async () => {
    state.data = bootstrap('invite-a')
    const view = render(<App />)
    await expectRoute('/join/invite-a')
    fireEvent.click(screen.getByRole('button', { name: 'Test catalog' }))
    await expectRoute('/events')
    state.data = { ...state.data }
    view.rerender(<App />)
    await expectRoute('/events')
    expect(state.paths.filter(path => path === '/join/invite-a')).toHaveLength(1)
  })
})
