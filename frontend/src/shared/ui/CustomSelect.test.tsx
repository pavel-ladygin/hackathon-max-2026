import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { CustomSelect } from './CustomSelect'

describe('CustomSelect', () => {
  it('supports arrow navigation, Home and Enter while retaining trigger focus', () => {
    const onChange = vi.fn()
    render(<CustomSelect label="Город" value="msk" options={[{ value: 'msk', label: 'Москва' }, { value: 'kzn', label: 'Казань' }, { value: 'spb', label: 'Санкт-Петербург', disabled: true }]} onChange={onChange} />)
    const trigger = screen.getByRole('combobox', { name: 'Город Москва' })
    trigger.focus()
    fireEvent.keyDown(trigger, { key: 'ArrowDown' })
    expect(trigger).toHaveAttribute('aria-activedescendant')
    fireEvent.keyDown(trigger, { key: 'Home' })
    fireEvent.keyDown(trigger, { key: 'Enter' })
    expect(onChange).toHaveBeenCalledWith('msk')
    expect(trigger).toHaveFocus()
  })

  it('does not crash when opened without options', () => {
    render(<CustomSelect label="Город" value="" options={[]} onChange={() => undefined} />)
    const trigger = screen.getByRole('combobox', { name: 'Город Выберите' })
    fireEvent.keyDown(trigger, { key: 'Enter' })
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
  })
})
