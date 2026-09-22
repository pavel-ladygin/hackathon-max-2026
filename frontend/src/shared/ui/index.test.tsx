import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { BottomNav, Button, Empty, ErrorState, FavoriteButton, HeartIcon, IconButton, Loading } from './index'

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
    render(<BottomNav activeId="catalog" items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'catalog', label: 'Афиша', icon: 'calendar' }]} onChange={onChange} />)

    expect(screen.getByRole('navigation', { name: 'Основная навигация' })).toBeVisible()
    expect(screen.getByRole('button', { name: /Афиша/ })).toHaveAttribute('aria-current', 'page')
    fireEvent.click(screen.getByRole('button', { name: /Главная/ }))
    expect(onChange).toHaveBeenCalledWith('home')
  })

  it('renders a keyboard-accessible icon and selected state', () => {
    render(<BottomNav activeId="home" items={[{ id: 'home', label: 'Главная', icon: 'home' }, { id: 'saved', label: 'Моё', icon: 'saved' }]} />)
    expect(screen.getByRole('button', { name: /Главная/ })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: /Главная/ }).querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})

describe('IconButton', () => {
  it('exposes the favorite toggle state and keeps its icon decorative', () => {
    render(<IconButton label="Убрать из сохранённых" variant="favorite" selected><HeartIcon filled /></IconButton>)
    const button = screen.getByRole('button', { name: 'Убрать из сохранённых' })

    expect(button).toHaveAttribute('aria-pressed', 'true')
    expect(button.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})

describe('animated actions', () => {
  it('exposes loading and success button states without allowing a duplicate action', () => {
    const { rerender } = render(<Button state="loading" loadingLabel="Отправляем">Создать</Button>)
    expect(screen.getByRole('button', { name: 'Отправляем' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Отправляем' })).toHaveAttribute('aria-busy', 'true')

    rerender(<Button state="success" successLabel="Готово">Создать</Button>)
    expect(screen.getByRole('button', { name: 'Готово' })).toBeDisabled()
  })

  it('keeps favorite state accessible and blocks toggles while pending', () => {
    const onToggle = vi.fn()
    render(<FavoriteButton selected pending label="Убрать из сохранённых" onToggle={onToggle} />)
    const favorite = screen.getByRole('button', { name: 'Убрать из сохранённых' })
    expect(favorite).toHaveAttribute('aria-pressed', 'true')
    expect(favorite).toHaveAttribute('aria-busy', 'true')
    expect(favorite).toBeDisabled()
    fireEvent.click(favorite)
    expect(onToggle).not.toHaveBeenCalled()
  })
})
