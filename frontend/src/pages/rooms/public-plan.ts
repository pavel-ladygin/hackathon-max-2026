import { z } from 'zod'
import { CATEGORY_REGISTRY } from '../../shared/api/categories'
import type { PublicPlan, RoomSnapshot } from '../../shared/api/types'

export const MOSCOW_CITY_ID = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
export const KAZAN_CITY_ID = '3d3c83de-691b-510e-85e5-79685c4012ee'

export const ROOM_CITIES = [
  { id: MOSCOW_CITY_ID, name: 'Москва', timezone: 'Europe/Moscow', ready: true },
  { id: KAZAN_CITY_ID, name: 'Казань', timezone: 'Europe/Moscow', ready: false },
] as const

export const TIME_SLOT_OPTIONS = [
  { value: 'morning', label: 'Утро' },
  { value: 'day', label: 'День' },
  { value: 'evening', label: 'Вечер' },
  { value: 'night', label: 'Ночь' },
] as const satisfies ReadonlyArray<{ value: PublicPlan['time_slots'][number]; label: string }>

const datePattern = /^\d{4}-\d{2}-\d{2}$/

export function isCanonicalDate(date: string): boolean {
  if (!datePattern.test(date)) return false
  const [year, month, day] = date.split('-').map(Number)
  const parsed = new Date(0)
  parsed.setUTCHours(0, 0, 0, 0)
  parsed.setUTCFullYear(year, month - 1, day)
  return parsed.getUTCFullYear() === year && parsed.getUTCMonth() === month - 1 && parsed.getUTCDate() === day
}

export function cityToday(timezone: string, now = new Date()): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now)
  const value = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value ?? ''
  return `${value('year')}-${value('month')}-${value('day')}`
}

export function roomCity(id?: string | null) {
  return ROOM_CITIES.find((city) => city.id === id)
}

export function budgetToMinor(value: string): number | null {
  const normalized = value.trim().replace(',', '.')
  if (!/^(?:\d+)(?:\.\d{1,2})?$/.test(normalized)) return null
  const [rubles, kopecks = ''] = normalized.split('.')
  const minor = Number(rubles) * 100 + Number(kopecks.padEnd(2, '0'))
  return Number.isSafeInteger(minor) && minor <= 100_000_000 ? minor : null
}

/** Explicit saved choice wins; then use the profile; default only when neither exists. */
export function resolveCreateCity(draftCityId?: string | null, profileCityId?: string | null): string {
  return draftCityId || profileCityId || MOSCOW_CITY_ID
}

export const publicPlanSchema = z.object({
  dates: z.array(z.string().refine(isCanonicalDate, 'Введите корректную дату')).min(1).max(14)
    .refine((dates) => new Set(dates).size === dates.length, 'Не повторяйте даты'),
  time_slots: z.array(z.enum(['morning', 'day', 'evening', 'night']))
    .max(4)
    .refine((values) => new Set(values).size === values.length, 'Не повторяйте время'),
  category_slugs: z.array(z.enum(Object.keys(CATEGORY_REGISTRY) as [keyof typeof CATEGORY_REGISTRY, ...(keyof typeof CATEGORY_REGISTRY)[]]))
    .min(1, 'Выберите хотя бы одну категорию')
    .max(11)
    .refine((values) => new Set(values).size === values.length, 'Не повторяйте категории'),
  budget_max_minor: z.number().int().min(0).max(100_000_000),
}).strict() satisfies z.ZodType<PublicPlan>

export const categoryLabel = (slug: PublicPlan['category_slugs'][number]) => CATEGORY_REGISTRY[slug].label

export function formatPlanDate(date: string): string {
  if (!isCanonicalDate(date)) return date
  return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric', weekday: 'long', timeZone: 'UTC' })
    .format(new Date(`${date}T12:00:00Z`))
}

export function formatBudgetMinor(amount: number): string {
  return new Intl.NumberFormat('ru-RU', { style: 'currency', currency: 'RUB', minimumFractionDigits: 0, maximumFractionDigits: 2 })
    .format(amount / 100)
}

export function sharePlanText(room: Pick<RoomSnapshot, 'name' | 'city_id' | 'public_plan'>): string {
  const city = roomCity(room.city_id)
  const lines = [`Присоединяйся к комнате «${room.name}»${city ? ` в городе ${city.name} (${city.timezone})` : ''}.`]
  const plan = room.public_plan
  if (!plan) return lines[0]

  lines.push(`Даты: ${plan.dates.map(formatPlanDate).join(', ')}`)
  lines.push(`Время: ${plan.time_slots.length ? plan.time_slots.map((slot) => TIME_SLOT_OPTIONS.find((item) => item.value === slot)?.label ?? slot).join(', ') : 'любое'}.`)
  lines.push(`Интересы: ${plan.category_slugs.map(categoryLabel).join(', ')}.`)
  lines.push(`Бюджет: до ${formatBudgetMinor(plan.budget_max_minor)}.`)
  return lines.join('\n')
}
