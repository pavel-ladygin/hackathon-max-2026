import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CreateRoomForm } from './CreateRoomForm'
import { KAZAN_CITY_ID, MOSCOW_CITY_ID } from './public-plan'

const DRAFT_KEY = 'user:presets:create-room'

function seedDraft(plan: Partial<{
  dates: string[]
  time_slots: string[]
  category_slugs: string[]
  budget: string
  anyTime: boolean
}> = {}, cityId = MOSCOW_CITY_ID) {
  window.sessionStorage.setItem(DRAFT_KEY, JSON.stringify({
    version: 1,
    cityId,
    citySelectedExplicitly: true,
    name: 'Проверка предложения',
    publicPlanEnabled: true,
    plan: { dates: [], time_slots: [], category_slugs: [], budget: '', anyTime: false, ...plan },
  }))
}

function renderForm(onSubmit = vi.fn(), props: Partial<React.ComponentProps<typeof CreateRoomForm>> = {}) {
  return render(<CreateRoomForm draftKey={DRAFT_KEY} pending={false} success={false} onSubmit={onSubmit} {...props} />)
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  window.sessionStorage.clear()
})

describe('CreateRoomForm public proposal presets', () => {
  it('applies tonight to the current city date and evening only, preserves manual choices, and waits for submit', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-24T19:00:00+03:00'))
    seedDraft({ dates: ['2026-10-30'], time_slots: ['morning'], category_slugs: ['concerts'], budget: '2400', anyTime: true })
    const onSubmit = vi.fn()
    renderForm(onSubmit)

    fireEvent.click(screen.getByRole('button', { name: 'Сегодня вечером' }))

    expect(screen.getByRole('button', { name: 'Создать комнату' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText(/Бюджет на человека/)).toHaveValue('2400')
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Проверка предложения',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2026-10-24'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 240_000 },
    })
  })

  it('uses the remaining weekend dates on Sunday and keeps unrelated draft fields', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-25T12:00:00+03:00'))
    seedDraft({ dates: ['2026-10-28'], time_slots: ['night'], category_slugs: ['theatre'], budget: '850' })
    const onSubmit = vi.fn()
    renderForm(onSubmit)

    fireEvent.click(screen.getByRole('button', { name: 'Выходные' }))

    expect(screen.getByRole('button', { name: 'Театр' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText(/Бюджет на человека/)).toHaveValue('850')
    // Applying a preset only edits the draft; it must never submit the form.
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Проверка предложения',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2026-10-25'], time_slots: ['night'], category_slugs: ['theatre'], budget_max_minor: 85_000 },
    })
  })

  it('caps a higher budget at 1,000 ₽ and preserves a lower budget', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-20T12:00:00+03:00'))
    seedDraft({ dates: ['2026-10-21'], time_slots: ['day'], category_slugs: ['concerts'], budget: '2500' })
    const { unmount } = renderForm()

    fireEvent.click(screen.getByRole('button', { name: 'Недорого' }))
    expect(screen.getByLabelText(/Бюджет на человека/)).toHaveValue('1000')
    unmount()
    seedDraft({ dates: ['2026-10-21'], time_slots: ['day'], category_slugs: ['concerts'], budget: '750' })
    renderForm()
    fireEvent.click(screen.getByRole('button', { name: 'Недорого' }))
    expect(screen.getByLabelText(/Бюджет на человека/)).toHaveValue('750')
  })

  it('disables presets for a city without a ready catalog and does not replace it with Moscow', () => {
    seedDraft({}, KAZAN_CITY_ID)
    renderForm()

    expect(screen.getByRole('combobox').textContent).toContain('Казань — город готовится')
    for (const label of ['Сегодня вечером', 'Выходные', 'Недорого']) {
      expect(screen.getByRole('button', { name: label })).toBeDisabled()
    }
  })

  it('disables presets while creating or after success, and for an unknown profile city', () => {
    seedDraft()
    const { rerender, unmount } = renderForm(vi.fn(), { pending: true })
    for (const label of ['Сегодня вечером', 'Выходные', 'Недорого']) {
      expect(screen.getByRole('button', { name: label })).toBeDisabled()
    }

    rerender(<CreateRoomForm draftKey={DRAFT_KEY} pending={false} success={true} onSubmit={vi.fn()} />)
    for (const label of ['Сегодня вечером', 'Выходные', 'Недорого']) {
      expect(screen.getByRole('button', { name: label })).toBeDisabled()
    }
    unmount()

    window.sessionStorage.setItem(DRAFT_KEY, JSON.stringify({ version: 1, cityId: 'unknown-city', citySelectedExplicitly: false, name: 'Черновик', publicPlanEnabled: true, plan: { dates: [], time_slots: [], category_slugs: [], budget: '', anyTime: false } }))
    renderForm(vi.fn(), { profileCityId: 'unknown-city' })
    expect(screen.getByRole('combobox').textContent).toContain('Город профиля не поддерживается')
    for (const label of ['Сегодня вечером', 'Выходные', 'Недорого']) {
      expect(screen.getByRole('button', { name: label })).toBeDisabled()
    }
  })

  it('disables tonight once the local evening slot has ended', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-20T22:00:00+03:00'))
    seedDraft()
    renderForm()

    expect(screen.getByRole('button', { name: 'Сегодня вечером' })).toBeDisabled()
  })

  it('rechecks the local evening cutoff at click time without changing a stale draft', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-20T21:59:00+03:00'))
    seedDraft({ dates: ['2026-10-22'], time_slots: ['morning'], category_slugs: ['concerts'], budget: '1200' })
    const onSubmit = vi.fn()
    renderForm(onSubmit)
    const tonight = screen.getByRole('button', { name: 'Сегодня вечером' })
    expect(tonight).toBeEnabled()

    vi.setSystemTime(new Date('2026-10-20T22:00:00+03:00'))
    fireEvent.click(tonight)

    expect(screen.getByRole('button', { name: 'Утро' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText(/Бюджет на человека/)).toHaveValue('1200')
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Проверка предложения',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2026-10-22'], time_slots: ['morning'], category_slugs: ['concerts'], budget_max_minor: 120_000 },
    })
  })

  it('restores an applied preset from the saved draft and submits only on explicit confirmation', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-20T19:00:00+03:00'))
    seedDraft({ dates: ['2026-10-23'], time_slots: ['morning'], category_slugs: ['theatre'], budget: '800' })
    const firstSubmit = vi.fn()
    const firstRender = renderForm(firstSubmit)
    fireEvent.click(screen.getByRole('button', { name: 'Сегодня вечером' }))
    expect(firstSubmit).not.toHaveBeenCalled()
    firstRender.unmount()

    const onSubmit = vi.fn()
    renderForm(onSubmit)
    expect(screen.getByRole('button', { name: 'Концерты' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: 'Театр' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText(/Бюджет на человека/)).toHaveValue('800')
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Проверка предложения',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2026-10-20'], time_slots: ['evening'], category_slugs: ['theatre'], budget_max_minor: 80_000 },
    })
  })
})
