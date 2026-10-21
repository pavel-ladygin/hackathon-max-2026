import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CreateRoomForm } from './CreateRoomForm'
import { budgetToMinor, KAZAN_CITY_ID, MOSCOW_CITY_ID } from './public-plan'

afterEach(() => {
  vi.useRealTimers()
  cleanup()
  window.sessionStorage.clear()
})

const baseProps = (onSubmit = vi.fn()) => ({
  draftKey: 'user:one:create-room',
  pending: false,
  success: false,
  onSubmit,
})

describe('CreateRoomForm', () => {
  it('uses an explicit saved city before profile and Moscow fallback, and leaves the proposal unpublished by default', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    window.sessionStorage.setItem('user:one:create-room', JSON.stringify({ version: 1, cityId: KAZAN_CITY_ID, citySelectedExplicitly: true, name: 'Черновик', publicPlanEnabled: false, plan: { dates: [], time_slots: [], category_slugs: [], budget: '', anyTime: false } }))
    render(<CreateRoomForm {...baseProps(onSubmit)} profileCityId={MOSCOW_CITY_ID} />)

    expect(screen.getByRole('combobox', { name: /Город комнаты/ })).toHaveTextContent('Казань — город готовится')
    expect(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ })).not.toBeChecked()

    await user.click(screen.getByRole('combobox', { name: /Город комнаты/ }))
    await user.click(screen.getByRole('option', { name: 'Москва' }))
    await user.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledWith({ name: 'Черновик', city_id: MOSCOW_CITY_ID })
  })

  it('uses profile city and keeps unknown profile selection gated instead of silently substituting Moscow', () => {
  render(<CreateRoomForm {...baseProps()} profileCityId={KAZAN_CITY_ID} />)
    expect(screen.getByRole('combobox', { name: /Город комнаты/ })).toHaveTextContent('Казань — город готовится')
    expect(screen.getByRole('button', { name: 'Создать комнату' })).toBeDisabled()
    cleanup()
    window.sessionStorage.removeItem('user:one:create-room')
    render(<CreateRoomForm {...baseProps()} profileCityId="unknown-profile-city" />)
    expect(screen.getByRole('combobox', { name: /Город комнаты/ })).toHaveTextContent('Город профиля не поддерживается — выберите вручную')
    expect(screen.getByRole('button', { name: 'Создать комнату' })).toBeDisabled()
  })

  it('does not treat an autosaved fallback as an explicit choice when profile data arrives later', () => {
    const props = baseProps()
    const { unmount } = render(<CreateRoomForm {...props} profileCityId={null} />)
    expect(screen.getByRole('combobox', { name: /Город комнаты/ })).toHaveTextContent('Москва')
    unmount()
    render(<CreateRoomForm {...props} profileCityId={KAZAN_CITY_ID} />)
    expect(screen.getByRole('combobox', { name: /Город комнаты/ })).toHaveTextContent('Казань — город готовится')
    expect(screen.getByRole('button', { name: 'Создать комнату' })).toBeDisabled()
  })

  it('shows field errors and does not submit an incomplete public proposal', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<CreateRoomForm {...baseProps(onSubmit)} />)
    await user.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    await user.click(screen.getByRole('button', { name: 'Создать комнату' }))

    expect(screen.getAllByText('Добавьте хотя бы одну дату.')).toHaveLength(2)
    expect(screen.getAllByText('Выберите время или отметьте «Любое время».')).toHaveLength(2)
    expect(screen.getAllByText('Выберите хотя бы одну категорию.')).toHaveLength(2)
    expect(screen.getAllByText(/Введите сумму от 0 до 1 000 000/)).toHaveLength(2)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('submits selected dates, slots, category and exact minor-unit budget only after explicit choices', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(2030, 0, 1, 12))
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    const onSubmit = vi.fn()
    render(<CreateRoomForm {...baseProps(onSubmit)} />)
    await user.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    await user.click(screen.getByRole('button', { name: 'Другая дата' }))
    await user.click(screen.getByRole('gridcell', { name: '12 января 2030 г.' }))
    await user.click(screen.getByRole('button', { name: 'Применить' }))
    await user.click(screen.getByRole('button', { name: 'Добавить дату' }))
    await user.click(screen.getByRole('button', { name: 'Вечер' }))
    await user.click(screen.getByRole('button', { name: 'Концерты' }))
    await user.type(screen.getByRole('textbox', { name: /Бюджет на человека/ }), '2500.75')
    await user.click(screen.getByRole('button', { name: 'Создать комнату' }))

    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Куда идём в субботу?',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2030-01-12'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_075 },
    })
  })

  it('limits dates to fourteen and does not shift past selections', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(2030, 0, 1, 12))
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    const onSubmit = vi.fn()
    render(<CreateRoomForm {...baseProps(onSubmit)} />)
    await user.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    await user.click(screen.getByRole('button', { name: 'Другая дата' }))
    await user.click(screen.getByRole('button', { name: 'Предыдущий месяц' }))
    expect(screen.getByRole('gridcell', { name: '31 декабря 2029 г.' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Закрыть календарь' }))
    for (let day = 1; day <= 7; day += 1) {
      const label = new Intl.DateTimeFormat('ru-RU', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }).format(new Date(Date.UTC(2030, 0, day)))
      await user.click(screen.getByRole('button', { name: label }))
    }
    for (let day = 8; day <= 14; day += 1) {
      await user.click(screen.getByRole('button', { name: 'Другая дата' }))
      await user.click(screen.getByRole('gridcell', { name: `${day} января 2030 г.` }))
      await user.click(screen.getByRole('button', { name: 'Применить' }))
      await user.click(screen.getByRole('button', { name: 'Добавить дату' }))
    }
    const dateChoices = within(screen.getByRole('group', { name: 'Подходящие даты' }))
    expect(dateChoices.getAllByRole('button', { pressed: true })).toHaveLength(14)
    expect(dateChoices.getByRole('button', { name: 'вторник, 1 января 2030 г.' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Добавить дату' })).toBeDisabled()
    expect(budgetToMinor('1000000.01')).toBeNull()
  })

  it('retains input on failed submission and prevents same-render duplicate submit', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    const props = baseProps(onSubmit)
    const { rerender } = render(<CreateRoomForm {...props} />)
    await user.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledTimes(1)
    rerender(<CreateRoomForm {...props} error={new Error('Create failed')} />)
    expect(screen.getByLabelText('Название комнаты')).toHaveValue('Куда идём в субботу?')
    expect(screen.getByText('Не удалось создать комнату.')).toBeInTheDocument()
  })

  it('converts decimal rubles to integer kopecks without floating point rounding', () => {
    expect(budgetToMinor('0')).toBe(0)
    expect(budgetToMinor('123.45')).toBe(12_345)
    expect(budgetToMinor('1,2')).toBe(120)
    expect(budgetToMinor('1.234')).toBeNull()
    expect(budgetToMinor('1e3')).toBeNull()
  })
})
