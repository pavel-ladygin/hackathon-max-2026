import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { KAZAN_CITY_ID, MOSCOW_CITY_ID } from './public-plan'
import { PlanSummary } from './PlanSummary'

describe('PlanSummary', () => {
  it('shows city and timezone even for a legacy room without a plan', () => {
    render(<PlanSummary cityId={MOSCOW_CITY_ID} />)
    expect(screen.getByText('Москва')).toBeInTheDocument()
    expect(screen.getByText('Часовой пояс: Europe/Moscow')).toBeInTheDocument()
    expect(screen.getByText('Детали встречи можно уточнить у пригласившего.')).toBeInTheDocument()
    expect(screen.queryByText(/задаётся при создании комнаты и не меняется/)).not.toBeInTheDocument()
  })

  it('renders the public plan details for the selected city', () => {
    render(<PlanSummary cityId={KAZAN_CITY_ID} plan={{ dates: ['2030-01-12'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_000 }} />)
    expect(screen.getByText('Казань')).toBeInTheDocument()
    expect(screen.getByText(/суббота, 12 января 2030 г\./)).toBeInTheDocument()
    expect(screen.getByText('Вечер')).toBeInTheDocument()
    expect(screen.getByText('Концерты')).toBeInTheDocument()
    expect(screen.getByText(/2\s500\s₽/)).toBeInTheDocument()
    expect(screen.getByText(/задаётся при создании комнаты и не меняется/)).toBeInTheDocument()
  })

  it('does not invent city or timezone details for unknown cities', () => {
    render(<PlanSummary cityId="unknown" plan={null} />)
    expect(screen.getByText('Город уточняется')).toBeInTheDocument()
    expect(screen.getByText('Часовой пояс пока неизвестен')).toBeInTheDocument()
  })
})
