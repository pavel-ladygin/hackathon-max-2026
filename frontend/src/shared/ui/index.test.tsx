import { act, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { BottomNav, Button, Empty, ErrorState, EventImage, FavoriteButton, HeartIcon, IconButton, Loading, ScreenSkeleton } from './index'

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

  it('restarts the icon animation on every click, including repeated clicks on the active item', () => {
    const onChange = vi.fn()
    render(<BottomNav activeId="home" items={[{ id: 'home', label: 'Главная', icon: 'home' }]} onChange={onChange} />)
    const button = screen.getByRole('button', { name: 'Главная' })
    const firstIcon = button.querySelector('span')

    fireEvent.click(button)
    const secondIcon = button.querySelector('span')
    expect(secondIcon).not.toBe(firstIcon)
    expect(button.className).toContain('navClick_home')

    fireEvent.click(button)
    expect(button.querySelector('span')).not.toBe(secondIcon)
    expect(onChange).toHaveBeenCalledTimes(2)
  })

  it('carries one click animation across a route remount without animating on initial render', () => {
    const items = [{ id: 'home', label: 'Главная', icon: 'home' as const }, { id: 'catalog', label: 'Афиша', icon: 'calendar' as const }]
    function RouteNav() {
      const [activeId, setActiveId] = useState('home')
      return <BottomNav key={activeId} activeId={activeId} items={items} onChange={setActiveId} />
    }

    render(<RouteNav />)
    expect(screen.getByRole('button', { name: 'Главная' }).className).not.toContain('navClick_home')
    fireEvent.click(screen.getByRole('button', { name: 'Афиша' }))

    expect(screen.getByRole('button', { name: 'Афиша' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Афиша' }).className).toContain('navClick_calendar')
    const firstDestinationIcon = screen.getByRole('button', { name: 'Афиша' }).querySelector('span')
    fireEvent.click(screen.getByRole('button', { name: 'Афиша' }))
    expect(screen.getByRole('button', { name: 'Афиша' }).querySelector('span')).not.toBe(firstDestinationIcon)
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

describe('EventImage', () => {
  it('shows a tiny Timepad preview before the full card image', () => {
    const id = '6167e34b-0b30-4a3a-8d5a-3f9bc0cabe26'
    const fullSrc = `https://ucare.timepad.ru/${id}/-/preview/256x256/`
    const preloads: FakeImage[] = []
    class FakeImage {
      src = ''
      currentSrc = fullSrc
      onload: (() => void) | null = null
      onerror: (() => void) | null = null
      constructor() { preloads.push(this) }
    }
    vi.stubGlobal('Image', FakeImage)
    try {
      render(<EventImage src={fullSrc} alt="Poster" />)
      const image = screen.getByRole('img', { name: 'Poster' })
      expect(image).toHaveAttribute('src', `https://ucare.timepad.ru/${id}/-/preview/64x64/`)
      expect(preloads[0]?.src).toBe(fullSrc)
      fireEvent.load(image)
      expect(image).toHaveAttribute('src', `https://ucare.timepad.ru/${id}/-/preview/64x64/`)
      act(() => preloads[0]?.onload?.())
      expect(image).toHaveAttribute('src', fullSrc)
    } finally {
      vi.unstubAllGlobals()
    }
  })
  it('shows a shimmer until the current image loads', () => {
    render(<EventImage src="https://images.example/poster.jpg" alt="Poster" />)
    const image = screen.getByRole('img', { name: 'Poster' })
    expect(image.className).toContain('eventImageLoading')
    fireEvent.load(image)
    expect(image.className).not.toContain('eventImageLoading')
  })

  it('retries a failed image with its category fallback and stops if that also fails', () => {
    render(<EventImage src="https://images.example/poster.jpg" fallbackSrc="/events/jazz-comedy.png" alt="Jazz concert poster" />)
    const image = screen.getByRole('img', { name: 'Jazz concert poster' })

    fireEvent.error(image)
    expect(image).toHaveAttribute('src', '/events/jazz-comedy.png')

    fireEvent.error(image)
    expect(screen.getByRole('img', { name: 'Jazz concert poster' }).tagName).toBe('SPAN')
    expect(document.querySelector('img')).not.toBeInTheDocument()
  })
})

describe('ScreenSkeleton', () => {
  it('renders a labelled full-page skeleton by default and supports inline variants', () => {
    const { rerender } = render(<ScreenSkeleton variant="home" />)
    const fullPage = screen.getByRole('status', { name: 'Загрузка содержимого' })
    expect(fullPage).toHaveAttribute('aria-busy', 'true')
    expect(fullPage.className).toContain('screenSkeletonPage')
    expect(fullPage.querySelectorAll('span').length).toBeGreaterThan(5)

    rerender(<ScreenSkeleton variant="map" inline label="Загрузка карты" />)
    expect(screen.getByRole('status', { name: 'Загрузка карты' }).className).toContain('screenSkeletonInline')
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
