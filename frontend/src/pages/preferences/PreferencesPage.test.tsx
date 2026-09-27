import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PreferencesPage } from './PreferencesPage'

const mocks = vi.hoisted(() => ({
  bootstrap: { data: undefined as any, isPending: false, isError: false, refetch: vi.fn() },
  updateNotifications: vi.fn(),
  replacePreferences: vi.fn(),
}))

vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => mocks.bootstrap }))
vi.mock('../../shared/api/client', () => ({ apiClient: { updateNotificationPreferences: mocks.updateNotifications, replacePreferences: mocks.replacePreferences } }))

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={queryClient}><MemoryRouter><PreferencesPage /></MemoryRouter></QueryClientProvider>)
}

describe('PreferencesPage daily notifications', () => {
  beforeEach(() => {
    mocks.bootstrap = {
      data: {
        dailyNotificationsEnabled: false,
        user: { cityId: null },
        preferences: { interestSlugs: ['concerts'], budgetMaxMinor: 350_000, usualDayTypes: ['weekend'], usualTimeSlots: ['evening'] },
      },
      isPending: false, isError: false, refetch: vi.fn(),
    }
    mocks.updateNotifications.mockReset()
    mocks.updateNotifications.mockResolvedValue({ daily_notifications_enabled: true })
    mocks.replacePreferences.mockReset()
  })

  it('loads the saved state and immediately sends toggle changes to the separate API', async () => {
    renderPage()
    const toggle = screen.getByRole('switch', { name: /ежедневные идеи от бота/i })

    expect(toggle).not.toBeChecked()
    fireEvent.click(toggle)

    await waitFor(() => expect(mocks.updateNotifications).toHaveBeenCalledWith({ daily_notifications_enabled: true }))
    await waitFor(() => expect(toggle).toBeChecked())
    expect(screen.queryByText('Настройка сохранена.')).not.toBeInTheDocument()
    expect(mocks.replacePreferences).not.toHaveBeenCalled()
  })

  it('reverts the toggle and shows an error if the update fails', async () => {
    mocks.updateNotifications.mockRejectedValue(new Error('offline'))
    renderPage()
    const toggle = screen.getByRole('switch', { name: /ежедневные идеи от бота/i })
    fireEvent.click(toggle)

    expect(await screen.findByRole('alert')).toHaveTextContent('Не удалось изменить уведомления')
    expect(toggle).not.toBeChecked()
  })
})
