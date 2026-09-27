import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { HomePage } from './HomePage'

const mocks = vi.hoisted(() => ({
  feed: { data: undefined as any, isPending: false, isError: false, error: undefined as unknown, refetch: vi.fn() },
  closeRoom: vi.fn(),
  acknowledgeNotice: vi.fn(),
}))

vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: { user: { displayName: 'Ирина' } } }) }))
vi.mock('../../features/discovery/queries', () => ({ useHomeFeed: () => mocks.feed }))
vi.mock('../../shared/api/client', () => ({ apiClient: {
  closeRoom: mocks.closeRoom,
  acknowledgeRoomClosedNotice: mocks.acknowledgeNotice,
} }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackClientError: vi.fn(), trackPerformance: vi.fn() }))

const activeRoom = { id: 'room-42', name: 'Куда идём в субботу?' }

function renderHome() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={queryClient}><MemoryRouter><HomePage /></MemoryRouter></QueryClientProvider>)
}

describe('HomePage room closure actions', () => {
  beforeEach(() => {
    mocks.feed = {
      data: { feed_id: 'feed-1', sections: [], activeRoom, roomClosedNotice: null },
      isPending: false, isError: false, error: undefined, refetch: vi.fn(),
    }
    mocks.closeRoom.mockReset()
    mocks.closeRoom.mockResolvedValue({ id: activeRoom.id })
    mocks.acknowledgeNotice.mockReset()
    mocks.acknowledgeNotice.mockResolvedValue(undefined)
  })

  it('lets the user cancel room closure without calling the API', () => {
    renderHome()

    fireEvent.click(screen.getByRole('button', { name: 'Завершить подбор' }))
    expect(screen.getByRole('dialog', { name: 'Завершить подбор?' })).toBeVisible()
    expect(screen.getByText(/Комната закроется для вас обоих/)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Остаться в комнате' }))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(mocks.closeRoom).not.toHaveBeenCalled()
  })

  it('closes the active room through the API after explicit confirmation', async () => {
    renderHome()

    fireEvent.click(screen.getByRole('button', { name: 'Завершить подбор' }))
    fireEvent.click(screen.getByRole('button', { name: 'Завершить для обоих' }))

    await waitFor(() => expect(mocks.closeRoom).toHaveBeenCalledWith(activeRoom.id))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('acknowledges the room-closed notice for its room', async () => {
    mocks.feed.data.roomClosedNotice = {
      room_id: activeRoom.id,
      room_name: activeRoom.name,
      closed_by: { display_name: 'Павел' },
    }
    renderHome()

    expect(screen.getByRole('status')).toHaveTextContent('Участник Павел завершил подбор')
    fireEvent.click(screen.getByRole('button', { name: 'Понятно' }))

    await waitFor(() => expect(mocks.acknowledgeNotice).toHaveBeenCalledWith(activeRoom.id))
  })
})
