import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../shared/api/errors'
import { EventSubmissionPage } from './EventSubmissionPages'

const mocks = vi.hoisted(() => ({ create: vi.fn(), options: vi.fn() }))
vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: { user: { id: 'user-1', cityId: 'city-1' } }, isPending: false, isError: false, refetch: vi.fn() }) }))
vi.mock('../../shared/api/client', () => ({ apiClient: { createEventSubmission: mocks.create, getEventSubmissionOptions: mocks.options } }))

function renderForm() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/events/suggest']}><EventSubmissionPage /></MemoryRouter></QueryClientProvider>)
}

describe('EventSubmissionPage', () => {
  beforeEach(() => {
    mocks.create.mockReset().mockResolvedValue({ id: 'submission-1', status: 'pending', created_at: '2099-01-01T00:00:00Z' })
    mocks.options.mockReset().mockResolvedValue({ cities: [{ id: 'city-1', name: 'Лос-Анджелес', timezone: 'America/Los_Angeles' }], categories: [{ slug: 'concerts', label: 'Концерты' }] })
  })

  it('previews city-local time without timezone shifting and submits exact kopecks', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText('Название'), { target: { value: 'Ночной джаз' } })
    fireEvent.change(screen.getByLabelText('Описание'), { target: { value: 'Живой джазовый концерт на крыше.' } })
    fireEvent.change(screen.getByLabelText('Город'), { target: { value: 'city-1' } })
    fireEvent.change(screen.getByLabelText('Место'), { target: { value: 'Blue Room' } })
    fireEvent.change(screen.getByLabelText('Адрес'), { target: { value: 'Main Street, 10' } })
    fireEvent.change(screen.getByLabelText('Начало'), { target: { value: '2099-06-01T10:00' } })
    fireEvent.change(screen.getByLabelText('Категория'), { target: { value: 'concerts' } })
    fireEvent.click(screen.getByLabelText('Есть цена'))
    fireEvent.change(screen.getByLabelText('Цена от, ₽'), { target: { value: '12.34' } })
    fireEvent.change(screen.getByLabelText(/Ссылка на событие или билеты/), { target: { value: 'https://tickets.example/show' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))

    expect(await screen.findByText(/01\.06\.2099, 10:00 · America\/Los_Angeles/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
    expect(mocks.create.mock.calls[0][0]).toMatchObject({ starts_at_local: '2099-06-01T10:00', price_mode: 'paid', price_from_minor: 1234, ticket_url: 'https://tickets.example/show' })
    expect(mocks.create.mock.calls[0][1]).toMatch(/^[0-9a-f-]{36}$/i)
    expect(await screen.findByRole('heading', { name: 'Отправлено на модерацию' })).toBeInTheDocument()
  })

  it('rejects a non-HTTPS poster URL before showing the preview', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText('Название'), { target: { value: 'Ночной джаз' } })
    fireEvent.change(screen.getByLabelText('Описание'), { target: { value: 'Живой джазовый концерт на крыше.' } })
    fireEvent.change(screen.getByLabelText('Город'), { target: { value: 'city-1' } })
    fireEvent.change(screen.getByLabelText('Место'), { target: { value: 'Blue Room' } })
    fireEvent.change(screen.getByLabelText('Адрес'), { target: { value: 'Main Street, 10' } })
    fireEvent.change(screen.getByLabelText('Начало'), { target: { value: '2099-06-01T10:00' } })
    fireEvent.change(screen.getByLabelText('Категория'), { target: { value: 'concerts' } })
    fireEvent.change(screen.getByLabelText(/Ссылка на афишу · необязательно/), { target: { value: 'http://unsafe.example/poster.jpg' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))

    expect(await screen.findByText('Укажите ссылку на афишу с HTTPS.')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Проверьте детали' })).not.toBeInTheDocument()
    expect(mocks.create).not.toHaveBeenCalled()
  })

  it('preserves form values and reuses the idempotency key when retrying after a network failure', async () => {
    mocks.create.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({ id: 'submission-1', status: 'pending', created_at: '2099-01-01T00:00:00Z' })
    renderForm()
    fireEvent.change(await screen.findByLabelText('Название'), { target: { value: 'Ночной джаз' } })
    fireEvent.change(screen.getByLabelText('Описание'), { target: { value: 'Живой джазовый концерт на крыше.' } })
    fireEvent.change(screen.getByLabelText('Город'), { target: { value: 'city-1' } })
    fireEvent.change(screen.getByLabelText('Место'), { target: { value: 'Blue Room' } })
    fireEvent.change(screen.getByLabelText('Адрес'), { target: { value: 'Main Street, 10' } })
    fireEvent.change(screen.getByLabelText('Начало'), { target: { value: '2099-06-01T10:00' } })
    fireEvent.change(screen.getByLabelText('Категория'), { target: { value: 'concerts' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))

    expect(await screen.findByText(/Все введённые данные сохранены\./)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Ночной джаз' })).toBeInTheDocument()
    expect(screen.getByText('Живой джазовый концерт на крыше.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(2))
    expect(mocks.create.mock.calls[0][1]).toBe(mocks.create.mock.calls[1][1])
    expect(mocks.create.mock.calls[0][0]).toEqual(mocks.create.mock.calls[1][0])
    expect(await screen.findByRole('heading', { name: 'Отправлено на модерацию' })).toBeInTheDocument()
  })

  it('maps price_to_minor validation errors to the price range field in preview', async () => {
    mocks.create.mockRejectedValueOnce(new ApiError(400, { error: { code: 'VALIDATION_FAILED', details: { field: 'price_to_minor', reason: 'Максимум превышает допустимую цену.' } } }))
    renderForm()
    fireEvent.change(await screen.findByLabelText('Название'), { target: { value: 'Ночной джаз' } })
    fireEvent.change(screen.getByLabelText('Описание'), { target: { value: 'Живой джазовый концерт на крыше.' } })
    fireEvent.change(screen.getByLabelText('Город'), { target: { value: 'city-1' } })
    fireEvent.change(screen.getByLabelText('Место'), { target: { value: 'Blue Room' } })
    fireEvent.change(screen.getByLabelText('Адрес'), { target: { value: 'Main Street, 10' } })
    fireEvent.change(screen.getByLabelText('Начало'), { target: { value: '2099-06-01T10:00' } })
    fireEvent.change(screen.getByLabelText('Категория'), { target: { value: 'concerts' } })
    fireEvent.click(screen.getByLabelText('Есть цена'))
    fireEvent.change(screen.getByLabelText('Цена от, ₽'), { target: { value: '10' } })
    fireEvent.change(screen.getByLabelText('Цена до, ₽ · необязательно'), { target: { value: '20' } })
    fireEvent.change(screen.getByLabelText(/Ссылка на событие или билеты/), { target: { value: 'https://tickets.example/show' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))

    expect(await screen.findByText('Цена до: Максимум превышает допустимую цену.')).toBeInTheDocument()
  })
})
