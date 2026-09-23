import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { InvitePage } from './RoomPages'

const platform = vi.hoisted(() => ({ shareInvite: vi.fn(), copyText: vi.fn() }))

vi.mock('../../shared/platform/max/adapter', () => ({ maxPlatform: platform }))
vi.mock('../../features/rooms/queries', () => ({
  useRoom: () => ({ isPending: false, isError: false, data: {
    name: 'Планы на вечер', participants: [{ id: 'creator' }],
    invite: { url: 'https://example.test/join/abc', max_deep_link: 'https://max.ru/example?startapp=abc' },
  } }),
  useRoomEvents: vi.fn(),
}))

function renderInvite() {
  return render(<MemoryRouter initialEntries={['/rooms/room-1/invite']}><Routes><Route path="/rooms/:roomId/invite" element={<InvitePage />} /></Routes></MemoryRouter>)
}

describe('InvitePage feedback', () => {
  beforeEach(() => {
    platform.shareInvite.mockReset()
    platform.copyText.mockReset()
  })

  it('shows a fallback action when sharing fails', async () => {
    platform.shareInvite.mockResolvedValue(false)
    renderInvite()
    fireEvent.click(screen.getByRole('button', { name: 'Пригласить через MAX' }))

    expect(await screen.findByText(/Не удалось отправить приглашение/)).toBeVisible()
    expect(screen.getByRole('button', { name: 'Скопировать ссылку' })).toBeEnabled()
    expect(screen.queryByText('✓ Приглашение отправлено')).not.toBeInTheDocument()
  })

  it('reports copying separately from sending', async () => {
    platform.copyText.mockResolvedValue(true)
    renderInvite()
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать ссылку' }))

    await waitFor(() => expect(platform.copyText).toHaveBeenCalledWith('https://example.test/join/abc'))
    expect(await screen.findByText('✓ Ссылка скопирована')).toBeVisible()
    expect(screen.queryByText('✓ Приглашение отправлено')).not.toBeInTheDocument()
  })
})
