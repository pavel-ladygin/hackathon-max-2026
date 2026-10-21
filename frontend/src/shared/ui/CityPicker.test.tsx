import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CityPicker } from './CityPicker'
import { MOSCOW_CITY_ID, SAINT_PETERSBURG_CITY_ID } from '../lib/cities'

describe('CityPicker', () => {
  afterEach(() => { vi.restoreAllMocks() })

  it('searches aliases and ignores case, spaces and dashes', () => {
    render(<CityPicker cityId={MOSCOW_CITY_ID} onSelect={vi.fn()} onClose={vi.fn()} />)
    fireEvent.change(screen.getByRole('searchbox', { name: 'Найти город' }), { target: { value: 'СПб' } })
    expect(screen.getByRole('option', { name: /Санкт-Петербург/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Москва/ })).not.toBeInTheDocument()
  })

  it('shows future cities as disabled and unavailable', () => {
    render(<CityPicker cityId={MOSCOW_CITY_ID} onSelect={vi.fn()} onClose={vi.fn()} />)
    const city = screen.getByRole('option', { name: /Казань/ })
    expect(city).toBeDisabled()
    expect(city).toHaveTextContent('Скоро')
  })

  it('supports arrow, Home and End navigation through available options', () => {
    render(<CityPicker cityId={MOSCOW_CITY_ID} onSelect={vi.fn()} onClose={vi.fn()} />)
    const search = screen.getByRole('searchbox', { name: 'Найти город' })
    fireEvent.keyDown(search, { key: 'ArrowDown' })
    const moscow = screen.getByRole('option', { name: /Москва/ })
    expect(moscow).toHaveFocus()
    fireEvent.keyDown(moscow, { key: 'ArrowDown' })
    const petersburg = screen.getByRole('option', { name: /Санкт-Петербург/ })
    expect(petersburg).toHaveFocus()
    fireEvent.keyDown(petersburg, { key: 'Home' })
    expect(moscow).toHaveFocus()
    fireEvent.keyDown(moscow, { key: 'End' })
    expect(petersburg).toHaveFocus()
  })

  it('keeps Tab within the dialog while saving disables its controls', () => {
    render(<CityPicker cityId={MOSCOW_CITY_ID} onSelect={vi.fn()} onClose={vi.fn()} pending />)
    const dialog = screen.getByRole('dialog', { name: 'Выберите город' })
    fireEvent.keyDown(document, { key: 'Tab' })
    expect(dialog).toHaveFocus()
  })

  it('closes after a successful city save', async () => {
    const onSelect = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()
    render(<CityPicker cityId={MOSCOW_CITY_ID} onSelect={onSelect} onClose={onClose} />)
    fireEvent.click(screen.getByRole('option', { name: /Санкт-Петербург/ }))
    expect(onSelect).toHaveBeenCalledWith(SAINT_PETERSBURG_CITY_ID)
    await vi.waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('keeps the picker open when saving fails', async () => {
    const onClose = vi.fn()
    const onSelect = vi.fn().mockRejectedValue(new Error('network'))
    render(<CityPicker cityId={MOSCOW_CITY_ID} onSelect={onSelect} onClose={onClose} />)
    fireEvent.click(screen.getByRole('option', { name: /Санкт-Петербург/ }))
    await vi.waitFor(() => expect(onSelect).toHaveBeenCalled())
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog', { name: 'Выберите город' })).toBeInTheDocument()
  })
})
