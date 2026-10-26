import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { RoomDateChoices } from './RoomDateChoices'

describe('RoomDateChoices', () => {
  it('keeps saved dates chronological without mutating their source order', () => {
    const selected = ['2099-01-14', '2099-01-12', '2099-01-13']
    Object.freeze(selected)
    const onChange = vi.fn()
    const { rerender } = render(<RoomDateChoices id="dates" selected={selected} onChange={onChange} />)
    const labels = () => screen.getAllByRole('button', { name: /^Удалить/ }).map(button => button.textContent)
    expect(labels()).toEqual(['Пн, 12 янв.×', 'Вт, 13 янв.×', 'Ср, 14 янв.×'])
    fireEvent.click(screen.getByRole('button', { name: 'Удалить Вт, 13 янв.' }))
    expect(onChange).toHaveBeenCalledWith(['2099-01-14', '2099-01-12'])
    rerender(<RoomDateChoices id="dates" selected={['2099-01-13', '2099-01-14', '2099-01-12']} onChange={onChange} />)
    expect(labels()).toEqual(['Пн, 12 янв.×', 'Вт, 13 янв.×', 'Ср, 14 янв.×'])
    expect(selected).toEqual(['2099-01-14', '2099-01-12', '2099-01-13'])
  })

  it('shows selected dates as removable black chips and removes on the whole-chip click', () => {
    const selected = ['2099-01-12']
    const onChange = vi.fn()
    render(<RoomDateChoices id="dates" selected={selected} onChange={onChange} />)

    expect(screen.getByText('Пн, 12 янв.')).toBeVisible()
    const chip = screen.getByRole('button', { name: 'Удалить Пн, 12 янв.' })
    expect(chip).toHaveTextContent('×')
    fireEvent.click(chip)
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('disables date removal and calendar selection while disabled', () => {
    render(<RoomDateChoices id="dates" selected={['2099-01-12']} onChange={() => undefined} disabled />)
    expect(screen.getByRole('button', { name: 'Удалить Пн, 12 янв.' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Выбор дат' })).toBeDisabled()
  })

  it('renders selected dates in a wrapping chip row', () => {
    const selected = ['2099-01-12', '2099-01-13']
    render(<RoomDateChoices id="dates" selected={selected} onChange={() => undefined} />)
    const firstChip = screen.getByRole('button', { name: 'Удалить Пн, 12 янв.' })
    const secondChip = screen.getByRole('button', { name: 'Удалить Вт, 13 янв.' })
    expect(firstChip.parentElement?.className).toMatch(/chips/)
    expect(secondChip.parentElement).toBe(firstChip.parentElement)
  })

  it('associates validation messages with the date picker', () => {
    render(<RoomDateChoices id="dates" selected={[]} onChange={() => undefined} error="Выберите дату" description="До 14 дат" />)
    expect(screen.getByRole('alert')).toHaveTextContent('Выберите дату')
    expect(screen.getByRole('button', { name: 'Выбор дат' })).toHaveAttribute('aria-describedby', 'dates-error')
  })
})
