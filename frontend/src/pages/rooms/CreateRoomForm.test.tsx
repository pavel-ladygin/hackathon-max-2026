import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CreateRoomForm } from './CreateRoomForm'
import { budgetToMinor, KAZAN_CITY_ID, MOSCOW_CITY_ID } from './public-plan'

afterEach(() => {
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

    expect(screen.getByRole('combobox')).toHaveValue(KAZAN_CITY_ID)
    expect(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ })).not.toBeChecked()

    await user.selectOptions(screen.getByRole('combobox'), MOSCOW_CITY_ID)
    await user.click(screen.getByRole('button', { name: 'Создать комнату' }))
    expect(onSubmit).toHaveBeenCalledWith({ name: 'Черновик', city_id: MOSCOW_CITY_ID })
  })

  it('uses profile city and keeps unknown profile selection gated instead of silently substituting Moscow', () => {
  render(<CreateRoomForm {...baseProps()} profileCityId={KAZAN_CITY_ID} />)
    expect(screen.getByRole('combobox')).toHaveValue(KAZAN_CITY_ID)
    expect(screen.getByRole('button', { name: 'Создать комнату' })).toBeDisabled()
    cleanup()
    window.sessionStorage.removeItem('user:one:create-room')
    render(<CreateRoomForm {...baseProps()} profileCityId="unknown-profile-city" />)
    expect(screen.getByRole('combobox')).toHaveValue('unknown-profile-city')
    expect(screen.getByRole('button', { name: 'Создать комнату' })).toBeDisabled()
  })

  it('does not treat an autosaved fallback as an explicit choice when profile data arrives later', () => {
    const props = baseProps()
    const { unmount } = render(<CreateRoomForm {...props} profileCityId={null} />)
    expect(screen.getByRole('combobox')).toHaveValue(MOSCOW_CITY_ID)
    unmount()
    render(<CreateRoomForm {...props} profileCityId={KAZAN_CITY_ID} />)
    expect(screen.getByRole('combobox')).toHaveValue(KAZAN_CITY_ID)
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
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<CreateRoomForm {...baseProps(onSubmit)} />)
    await user.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    fireEvent.change(screen.getByLabelText('Новая дата публичного предложения'), { target: { value: '2030-01-12' } })
    await user.click(screen.getByRole('button', { name: 'Добавить дату' }))
    await user.click(screen.getByLabelText('Вечер'))
    await user.click(screen.getByLabelText('Концерты'))
    await user.type(screen.getByRole('spinbutton'), '2500.75')
    await user.click(screen.getByRole('button', { name: 'Создать комнату' }))

    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Куда идём в субботу?',
      city_id: MOSCOW_CITY_ID,
      public_plan: { dates: ['2030-01-12'], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 250_075 },
    })
  })

  it('limits dates to fourteen and does not shift past selections', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<CreateRoomForm {...baseProps(onSubmit)} />)
    await user.click(screen.getByRole('checkbox', { name: /Добавить публичное предложение/ }))
    const date = screen.getByLabelText('Новая дата публичного предложения')
    fireEvent.change(date, { target: { value: '2000-01-01' } })
    await user.click(screen.getByRole('button', { name: 'Добавить дату' }))
    expect(screen.getAllByText('Выберите корректную будущую дату без повторов.')).toHaveLength(2)
    expect(screen.queryByRole('button', { name: /Удалить дату/ })).not.toBeInTheDocument()

    for (let day = 1; day <= 14; day += 1) {
      const value = `2030-01-${String(day).padStart(2, '0')}`
      fireEvent.change(date, { target: { value } })
      await user.click(screen.getByRole('button', { name: 'Добавить дату' }))
    }
    const dateList = screen.getAllByRole('list').find((list) => within(list).queryAllByRole('listitem').length > 0)
    expect(dateList).toBeDefined()
    expect(within(dateList!).getAllByRole('listitem')).toHaveLength(14)
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
