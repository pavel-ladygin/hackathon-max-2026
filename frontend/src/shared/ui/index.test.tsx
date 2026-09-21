import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { BottomNav, Empty, ErrorState, Loading } from './index'

describe('shared status states', () => {
  it('supports embedded and full-page presentation without changing live semantics', () => {
    const { rerender } = render(<Loading label="Загружаем данные" />)
    const fullPage = screen.getByRole('status')
    const fullPageClass = fullPage.className

    rerender(<Loading inline label="Загружаем данные" />)
    expect(screen.getByRole('status').className).not.toBe(fullPageClass)

    rerender(<Empty inline title="Пока пусто" />)
    expect(screen.getByRole('heading', { name: 'Пока пусто' })).toBeVisible()

    rerender(<ErrorState inline title="Не загрузилось" />)
    expect(screen.getByRole('alert')).toHaveTextContent('Не загрузилось')
  })
})

describe('BottomNav', () => {
  it('marks the active page and reports navigation changes', () => {
    const onChange = vi.fn()
    render(<BottomNav activeId="catalog" items={[{ id: 'home', label: 'Главная', icon: '⌂' }, { id: 'catalog', label: 'Афиша', icon: '⌕' }]} onChange={onChange} />)

    expect(screen.getByRole('navigation', { name: 'Основная навигация' })).toBeVisible()
    expect(screen.getByRole('button', { name: /Афиша/ })).toHaveAttribute('aria-current', 'page')
    fireEvent.click(screen.getByRole('button', { name: /Главная/ }))
    expect(onChange).toHaveBeenCalledWith('home')
  })
})
