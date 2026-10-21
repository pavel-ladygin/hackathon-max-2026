import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../shared/api/errors'
import { EventSubmissionPage } from './EventSubmissionPages'

const mocks = vi.hoisted(() => ({ create: vi.fn(), upload: vi.fn(), options: vi.fn() }))
vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: { user: { id: 'user-1', cityId: 'city-1' } }, isPending: false, isError: false, refetch: vi.fn() }) }))
vi.mock('../../shared/api/client', () => ({ apiClient: { createEventSubmission: mocks.create, uploadEventSubmissionImage: mocks.upload, getEventSubmissionOptions: mocks.options } }))

function renderForm() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/events/suggest']}><EventSubmissionPage /></MemoryRouter></QueryClientProvider>)
}

function getPosterInput() { return document.querySelector<HTMLInputElement>('input[type="file"][accept="image/jpeg,image/png,image/webp"]')! }

function futureDate(days = 10) { const date = new Date(); date.setDate(date.getDate() + days); return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function dateLabel(value: string) { const [year, month, day] = value.split('-').map(Number); return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' }).format(new Date(year, month - 1, day)) }
async function chooseDate(label: string, date: string) {
  fireEvent.click(screen.getByRole('button', { name: label }))
  const dialog = await screen.findByRole('dialog', { name: 'Выбор даты' })
  const target = dateLabel(date)
  while (within(dialog).queryByRole('gridcell', { name: target }) === null) fireEvent.click(within(dialog).getByRole('button', { name: 'Следующий месяц' }))
  fireEvent.click(within(dialog).getByRole('gridcell', { name: target }))
  fireEvent.click(within(dialog).getByRole('button', { name: 'Применить' }))
}
function chooseOption(label: string, option: string) { fireEvent.click(screen.getByRole('combobox', { name: new RegExp(label) })); fireEvent.click(screen.getByRole('option', { name: option })) }
async function fillRequiredForm() {
  fireEvent.change(await screen.findByLabelText('Название'), { target: { value: 'Ночной джаз' } })
  fireEvent.change(screen.getByLabelText('Описание'), { target: { value: 'Живой джазовый концерт на крыше.' } })
  chooseOption('Город', 'Лос-Анджелес')
  fireEvent.change(screen.getByLabelText('Место'), { target: { value: 'Blue Room' } })
  fireEvent.change(screen.getByLabelText('Адрес'), { target: { value: 'Main Street, 10' } })
  const start = futureDate()
  await chooseDate('Начало', start)
  fireEvent.change(screen.getByLabelText('Время начала, ЧЧ:ММ'), { target: { value: '10:00' } })
  chooseOption('Категория', 'Концерты')
  fireEvent.change(screen.getByLabelText(/Ссылка на событие или билеты/), { target: { value: 'https://tickets.example/show' } })
  return start
}

describe('EventSubmissionPage', () => {
  beforeEach(() => {
    mocks.create.mockReset().mockResolvedValue({ id: 'submission-1', status: 'pending', created_at: '2099-01-01T00:00:00Z' })
    mocks.upload.mockReset().mockResolvedValue({ image_id: 'image-1' })
    mocks.options.mockReset().mockResolvedValue({ cities: [{ id: 'city-1', name: 'Лос-Анджелес', timezone: 'America/Los_Angeles' }], categories: [{ slug: 'concerts', label: 'Концерты' }] })
  })

  it('previews city-local time without timezone shifting and submits exact kopecks', async () => {
    renderForm()
    const start = await fillRequiredForm()
    fireEvent.click(screen.getByRole('button', { name: 'Есть цена' }))
    fireEvent.change(screen.getByLabelText('Цена от, ₽'), { target: { value: '12.34' } })
    fireEvent.change(screen.getByLabelText(/Ссылка на событие или билеты/), { target: { value: 'https://tickets.example/show' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))

    expect(await screen.findByText(new RegExp(`10:00 · America/Los_Angeles`))).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
    expect(mocks.create.mock.calls[0][0]).toMatchObject({ starts_at_local: `${start}T10:00`, price_mode: 'paid', price_from_minor: 1234, ticket_url: 'https://tickets.example/show' })
    expect(mocks.create.mock.calls[0][1]).toMatch(/^[0-9a-f-]{36}$/i)
    expect(await screen.findByRole('heading', { name: 'Отправлено на модерацию' })).toBeInTheDocument()
  })

  it('uploads a selected poster, previews it and submits its image id', async () => {
    renderForm()
    const start = await fillRequiredForm()
    const input = getPosterInput()
    const click = vi.spyOn(HTMLInputElement.prototype, 'click').mockImplementation(() => {})
    expect(screen.queryByRole('button', { name: /^Выбрать файл$/ })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Выбрать файл афиши' }))
    expect(click).toHaveBeenCalledOnce()
    click.mockRestore()
    const file = new File(['image bytes'], 'poster.png', { type: 'image/png' })
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => expect(mocks.upload).toHaveBeenCalledWith(file, expect.objectContaining({ signal: expect.any(AbortSignal) })))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    expect(await screen.findByRole('heading', { name: 'Проверьте детали' })).toBeInTheDocument()
    expect(screen.getByAltText('Афиша: Ночной джаз')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
    expect(mocks.create.mock.calls[0][0]).toMatchObject({ starts_at_local: `${start}T10:00`, image_id: 'image-1' })
  })

  it('preserves form values and reuses the idempotency key when retrying after a network failure', async () => {
    mocks.create.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({ id: 'submission-1', status: 'pending', created_at: '2099-01-01T00:00:00Z' })
    renderForm()
    await fillRequiredForm()
    fireEvent.change(getPosterInput(), { target: { files: [new File(['image bytes'], 'poster.png', { type: 'image/png' })] } })
    await screen.findByText('Афиша загружена')
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))

    expect(await screen.findByText(/Все введённые данные сохранены\./)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Ночной джаз' })).toBeInTheDocument()
    expect(screen.getByText('Живой джазовый концерт на крыше.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(2))
    expect(mocks.create.mock.calls[0][1]).toBe(mocks.create.mock.calls[1][1])
    expect(mocks.create.mock.calls[0][0]).toEqual(mocks.create.mock.calls[1][0])
    expect(mocks.create.mock.calls[1][0]).toHaveProperty('image_id', 'image-1')
    expect(mocks.upload).toHaveBeenCalledOnce()
    expect(await screen.findByRole('heading', { name: 'Отправлено на модерацию' })).toBeInTheDocument()
  })

  it('allows removing a failed optional poster and continuing without an image', async () => {
    mocks.upload.mockRejectedValueOnce(new Error('storage unavailable'))
    renderForm()
    await fillRequiredForm()
    const file = new File(['image bytes'], 'poster.png', { type: 'image/png' })
    fireEvent.change(getPosterInput(), { target: { files: [file] } })
    expect(await screen.findByText('Не удалось загрузить афишу. Попробуйте ещё раз.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Удалить файл' }))
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
    expect(mocks.create.mock.calls[0][0]).not.toHaveProperty('image_id')
    expect(mocks.upload).toHaveBeenCalledOnce()
  })

  it('lets the author upload again when the server rejects an expired image id', async () => {
    mocks.create.mockRejectedValueOnce(new ApiError(422, { error: { code: 'VALIDATION_FAILED', details: { field: 'image_id', reason: 'Загрузите афишу заново.' } } }))
    mocks.upload.mockResolvedValueOnce({ image_id: 'expired-image' }).mockResolvedValueOnce({ image_id: 'fresh-image' })
    renderForm()
    await fillRequiredForm()
    fireEvent.change(getPosterInput(), { target: { files: [new File(['image bytes'], 'poster.png', { type: 'image/png' })] } })
    await screen.findByText('Афиша загружена')
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))
    expect(await screen.findByText('Загрузите афишу заново.')).toBeInTheDocument()
    expect(screen.getByLabelText('Название')).toHaveValue('Ночной джаз')
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    await screen.findByText('Афиша загружена')
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(2))
    expect(mocks.create.mock.calls[1][0]).toHaveProperty('image_id', 'fresh-image')
  })

  it('maps price_to_minor validation errors to the price range field in preview', async () => {
    mocks.create.mockRejectedValueOnce(new ApiError(400, { error: { code: 'VALIDATION_FAILED', details: { field: 'price_to_minor', reason: 'Максимум превышает допустимую цену.' } } }))
    renderForm()
    await fillRequiredForm()
    fireEvent.click(screen.getByRole('button', { name: 'Есть цена' }))
    fireEvent.change(screen.getByLabelText('Цена от, ₽'), { target: { value: '10' } })
    fireEvent.change(screen.getByLabelText('Цена до, ₽'), { target: { value: '20' } })
    fireEvent.change(screen.getByLabelText(/Ссылка на событие или билеты/), { target: { value: 'https://tickets.example/show' } })
    fireEvent.click(screen.getByRole('button', { name: 'Проверить перед отправкой' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Отправить на модерацию' }))

    expect(await screen.findByText('Цена до: Максимум превышает допустимую цену.')).toBeInTheDocument()
  })
})
