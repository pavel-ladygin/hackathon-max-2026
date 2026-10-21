import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { RoomDateChoices } from './RoomDateChoices'

describe('RoomDateChoices', () => {
  it('keeps removal available at fourteen dates and rejects a fifteenth selection', () => {
    const selected = Array.from({ length: 14 }, (_, index) => `2099-01-${String(index + 1).padStart(2, '0')}`)
    const onToggle = vi.fn()
    const onLimitReached = vi.fn()
    render(<RoomDateChoices id="dates" options={[[selected[0], 'Selected'], ['2099-01-15', 'Another']]} selected={selected} onToggle={onToggle} onLimitReached={onLimitReached} date="" onDateChange={() => undefined} onAddDate={() => undefined} />)
    fireEvent.click(screen.getByRole('button', { name: 'Selected' }))
    fireEvent.click(screen.getByRole('button', { name: 'Another' }))
    expect(onToggle).toHaveBeenCalledTimes(1)
    expect(onToggle).toHaveBeenCalledWith(selected[0])
    expect(onLimitReached).toHaveBeenCalledOnce()
  })
})
