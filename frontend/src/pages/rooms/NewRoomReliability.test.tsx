import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { ApiError } from '../../shared/api/errors'
import { NewRoomPage } from './RoomPages'

const mocks = vi.hoisted(() => ({ createRoom: vi.fn() }))
vi.mock('../../shared/api/client', () => ({ apiClient: { createRoom: mocks.createRoom } }))
vi.mock('../../features/auth/useBootstrap', () => ({ useBootstrap: () => ({ data: { user: { id: 'creator' } }, isPending: false }) }))
vi.mock('../../shared/analytics/client', () => ({ track: vi.fn(), trackFailure: vi.fn() }))
vi.mock('./CreateRoomForm', () => ({ CreateRoomForm: ({ pending, onSubmit }: { pending: boolean; onSubmit: (value: { name: string; city_id: string }) => void }) => <>
  <button disabled={pending} onClick={() => onSubmit({ name: 'Первый план', city_id: 'city' })}>Создать</button>
  <button disabled={pending} onClick={() => onSubmit({ name: 'Другой план', city_id: 'city' })}>Изменить и создать</button>
</> }))

function view() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return { ...render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/rooms/new']}><Routes><Route path="/rooms/new" element={<NewRoomPage />} /><Route path="/" element={<h1>Главная</h1>} /></Routes></MemoryRouter></QueryClientProvider>), client }
}

beforeEach(() => { mocks.createRoom.mockReset() })

it('reuses the idempotency key after an ambiguous failure and changes it only for a changed request', async () => {
  mocks.createRoom.mockRejectedValue(new ApiError(0))
  view()
  expect(mocks.createRoom).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Создать' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Создать' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: 'Создать' }))
  await waitFor(() => expect(mocks.createRoom).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Изменить и создать' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: 'Изменить и создать' }))
  await waitFor(() => expect(mocks.createRoom).toHaveBeenCalledTimes(3))
  const [first, retry, changed] = mocks.createRoom.mock.calls
  expect(first[0]).toEqual(retry[0])
  expect(first[1]).toEqual(retry[1])
  expect(first[1]).toEqual(expect.any(String))
  expect(changed[1]).not.toEqual(first[1])
})

it('offers a home exit for ACTIVE_ROOM_EXISTS without making up an active room address', async () => {
  mocks.createRoom.mockRejectedValue(new ApiError(409, { error: { code: 'ACTIVE_ROOM_EXISTS' } }))
  view()
  fireEvent.click(screen.getByRole('button', { name: 'Создать' }))
  fireEvent.click(await screen.findByRole('button', { name: 'На главную' }))
  expect(await screen.findByRole('heading', { name: 'Главная' })).toBeVisible()
  expect(mocks.createRoom).toHaveBeenCalledTimes(1)
})

it('keeps a successfully created room without navigating back after leaving for home', async () => {
  let finishCreate!: (value: { room: { id: string } }) => void
  mocks.createRoom.mockReturnValue(new Promise((resolve) => { finishCreate = resolve }))
  const { client } = view()
  fireEvent.click(screen.getByRole('button', { name: 'Создать' }))
  await waitFor(() => expect(mocks.createRoom).toHaveBeenCalledTimes(1))
  fireEvent.click(screen.getByRole('button', { name: 'Назад' }))
  expect(screen.getByRole('heading', { name: 'Главная' })).toBeVisible()

  await act(async () => { finishCreate({ room: { id: 'created-room' } }) })
  await waitFor(() => expect(client.getMutationCache().getAll()[0].state.status).toBe('success'))
  expect(screen.getByRole('heading', { name: 'Главная' })).toBeVisible()
  expect(client.getQueryData(['room', 'created-room'])).toEqual({ id: 'created-room' })
  expect(mocks.createRoom).toHaveBeenCalledTimes(1)
})
