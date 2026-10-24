import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MOSCOW_CITY_ID, SAINT_PETERSBURG_CITY_ID } from '../../shared/lib/cities'
import { OnboardingPage } from './OnboardingPage'

const mocks = vi.hoisted(() => ({ replacePreferences: vi.fn() }))
vi.mock('../../shared/api/client', () => ({ apiClient: { replacePreferences: mocks.replacePreferences } }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn() }))
vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: {
  user: { cityId: MOSCOW_CITY_ID },
  preferences: { cityId: MOSCOW_CITY_ID, interestSlugs: ['cinema', 'food'], budgetMaxMinor: 350_000, usualDayTypes: ['weekend'], usualTimeSlots: ['evening'] },
} }) }))

function renderProfile() {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/onboarding/profile']}><Routes>
    <Route path="/onboarding/:step" element={<OnboardingPage />} />
    <Route path="/" element={<p>Главная страница</p>} />
  </Routes></MemoryRouter></QueryClientProvider>)
}

describe('onboarding city selection', () => {
  beforeEach(() => { mocks.replacePreferences.mockReset(); mocks.replacePreferences.mockResolvedValue({}) })

  it('saves the chosen city together with the current onboarding preferences', async () => {
    renderProfile()
    fireEvent.change(screen.getByRole('slider'), { target: { value: '5000' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ваш город' }))
    expect(screen.getByRole('option', { name: /Казань/ })).toBeDisabled()
    fireEvent.click(screen.getByRole('option', { name: /Санкт-Петербург/ }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Ваш город' })).toHaveTextContent('Санкт-Петербург')
    expect(mocks.replacePreferences).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Сформировать подборку' }))
    await waitFor(() => expect(mocks.replacePreferences).toHaveBeenCalledWith({
      city_id: SAINT_PETERSBURG_CITY_ID, interest_slugs: ['cinema', 'food'], budget_max_minor: 500_000,
      usual_day_types: ['weekend'], usual_time_slots: ['evening'],
    }))
    await waitFor(() => expect(screen.getByText('Главная страница')).toBeVisible())
  })

  it('keeps the city unchanged when the picker is dismissed', () => {
    renderProfile()
    fireEvent.click(screen.getByRole('button', { name: 'Ваш город' }))
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть выбор города' }))
    expect(screen.getByRole('button', { name: 'Ваш город' })).toHaveTextContent('Москва')
    expect(mocks.replacePreferences).not.toHaveBeenCalled()
  })

  it('retains the chosen city and form values after a failed save for retry', async () => {
    mocks.replacePreferences.mockRejectedValueOnce(new Error('network'))
    renderProfile()
    fireEvent.click(screen.getByRole('button', { name: 'Ваш город' }))
    fireEvent.click(screen.getByRole('option', { name: /Санкт-Петербург/ }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: 'Сформировать подборку' }))
    await waitFor(() => expect(screen.getByText('Не удалось сохранить настройки. Попробуйте ещё раз.')).toBeVisible())
    expect(screen.getByRole('button', { name: 'Ваш город' })).toHaveTextContent('Санкт-Петербург')
    fireEvent.click(screen.getByRole('button', { name: 'Сформировать подборку' }))
    await waitFor(() => expect(mocks.replacePreferences).toHaveBeenCalledTimes(2))
    expect(mocks.replacePreferences.mock.calls[1][0].city_id).toBe(SAINT_PETERSBURG_CITY_ID)
  })
})
