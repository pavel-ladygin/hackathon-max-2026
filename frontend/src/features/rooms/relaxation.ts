import type { CategorySlug, DayType, RoomIntentRequestDto, RoomSnapshot } from '../../shared/api/types'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'

export const RELAXATION_CATEGORIES = CATEGORY_OPTIONS

const MAX_RELAXED_BUDGET_RUB = 10_000
const BUDGET_STEP_RUB = 500
const DEFAULT_TIME_ZONE = 'Europe/Moscow'

export type Relaxation = {
  id: 'budget' | 'category' | 'date' | 'time'
  title: string
  intent: RoomIntentRequestDto | null
  dates?: string[]
  budgets?: number[]
  categories?: Array<{ slug: CategorySlug; label: string }>
}

export type RelaxationChanges = {
  budget?: number
  category?: CategorySlug
  date?: string
  time?: true
}

export function combinedRelaxedIntent(current: RoomSnapshot['myIntent'], changes: RelaxationChanges, now = new Date(), timeZone = DEFAULT_TIME_ZONE): RoomIntentRequestDto {
  if (!current) throw new Error('A current intent is required before relaxing room preferences')
  const kinds = (['budget', 'category', 'date', 'time'] as const).filter((kind) => changes[kind] !== undefined)
  const available = availableRelaxations(current, now, timeZone)
  if (!kinds.length || kinds.some((kind) => !available.some((item) => item.id === kind))) throw new Error('Select available room intent changes')
  const result = baseIntent(current, localDate(now, timeZone))
  // Validate each change against the same original intent, then copy only its
  // field so adding a category cannot overwrite a previously selected budget.
  for (const kind of kinds) {
    const value = changes[kind]
    const candidate = relaxedIntent(current, kind, now, value === true ? undefined : value, timeZone)
    if (kind === 'budget') result.budget_max_minor = candidate.budget_max_minor
    else if (kind === 'category') result.category_slugs = candidate.category_slugs
    else if (kind === 'date') result.dates = candidate.dates
    else result.time_slots = candidate.time_slots
  }
  return result
}

export function relaxedIntent(current: RoomSnapshot['myIntent'], kind: Relaxation['id'], now = new Date(), selectedValue?: string | number, timeZone = DEFAULT_TIME_ZONE): RoomIntentRequestDto {
  if (!current) throw new Error('A current intent is required before relaxing room preferences')
  const today = localDate(now, timeZone)
  const base = baseIntent(current, today)
  if (kind === 'budget') {
    if (typeof selectedValue !== 'number' || !availableBudgets(base.budget_max_minor).includes(selectedValue)) throw new Error('Select an available higher budget')
    base.budget_max_minor = selectedValue * 100
  }
  if (kind === 'category') {
    if (typeof selectedValue !== 'string' || !availableCategories(base.category_slugs).some(({ slug }) => slug === selectedValue)) throw new Error('Select an available category')
    base.category_slugs = [...base.category_slugs, selectedValue as CategorySlug]
  }
  if (kind === 'date') {
    if (typeof selectedValue !== 'string' || !availableDates(base.dates, base.day_types, now, timeZone).includes(selectedValue)) throw new Error('An explicit available date is required to expand room intent')
    base.dates = [...new Set([...base.dates, selectedValue])].sort()
  }
  if (kind === 'time') base.time_slots = ['morning', 'day', 'evening', 'night']
  return base
}

export function availableRelaxations(current: RoomSnapshot['myIntent'], now = new Date(), timeZone = DEFAULT_TIME_ZONE): Relaxation[] {
  if (!current) return []
  const baseline = baseIntent(current, localDate(now, timeZone))
  const candidates: Relaxation[] = []
  const dates = availableDates(baseline.dates, baseline.day_types, now, timeZone)
  // Expired dates must be replaced through an explicit choice before other
  // changes can be submitted as a valid intent.
  if (!baseline.dates.length) {
    if (dates.length) candidates.push({ id: 'date', title: 'Добавить дату', intent: null, dates })
    return candidates
  }
  const budgets = availableBudgets(baseline.budget_max_minor)
  if (budgets.length) candidates.push({ id: 'budget', title: 'Увеличить бюджет', intent: null, budgets })
  const categories = availableCategories(baseline.category_slugs)
  if (categories.length) candidates.push({ id: 'category', title: 'Добавить категорию', intent: null, categories })
  if (baseline.dates.length < 14 && dates.length) candidates.push({ id: 'date', title: 'Добавить дату', intent: null, dates })
  if (baseline.time_slots.length > 0 && baseline.time_slots.length < 4) candidates.push({ id: 'time', title: 'Разрешить любое время', intent: relaxedIntent(current, 'time', now, undefined, timeZone) })
  return candidates.filter(({ id, intent }) => id === 'date' || intent === null || changed(baseline, intent, id))
}

export function availableDates(dates: string[], dayTypes: DayType[], now = new Date(), timeZone = DEFAULT_TIME_ZONE) {
  const choices: string[] = []
  const today = localDate(now, timeZone)
  for (let offset = 1; offset <= 14; offset += 1) {
    const candidate = addDays(today, offset)
    const date = new Date(`${candidate}T12:00:00Z`)
    if (!dates.includes(candidate) && compatibleDayType(date, dayTypes)) choices.push(candidate)
  }
  return choices
}

function availableBudgets(currentMinor: number) {
  const currentRub = currentMinor / 100
  const firstChoice = Math.floor(currentRub / BUDGET_STEP_RUB) * BUDGET_STEP_RUB + BUDGET_STEP_RUB
  const choices: number[] = []
  for (let budget = firstChoice; budget <= MAX_RELAXED_BUDGET_RUB; budget += BUDGET_STEP_RUB) choices.push(budget)
  return choices
}

function availableCategories(selected: CategorySlug[]) {
  return RELAXATION_CATEGORIES.filter(({ slug }) => !selected.includes(slug))
}

function changed(base: RoomIntentRequestDto, candidate: RoomIntentRequestDto, kind: Relaxation['id']) {
  if (kind === 'budget') return candidate.budget_max_minor !== base.budget_max_minor
  if (kind === 'date') return candidate.dates.length !== base.dates.length
  if (kind === 'category') return candidate.category_slugs.length !== base.category_slugs.length
  return candidate.time_slots.length !== base.time_slots.length
}

function baseIntent(current: NonNullable<RoomSnapshot['myIntent']>, today: string): RoomIntentRequestDto {
  return { dates: current.dates.filter((date) => date >= today), day_types: current.day_types, time_slots: current.time_slots, category_slugs: current.category_slugs, budget_max_minor: current.budget_max_minor, radius_m: null, exclusion_slugs: current.exclusion_slugs, location: null, free_text: current.free_text }
}

function compatibleDayType(date: Date, dayTypes: DayType[]) {
  if (dayTypes.length === 0) return true
  const isWeekend = date.getUTCDay() === 0 || date.getUTCDay() === 6
  return dayTypes.includes(isWeekend ? 'weekend' : 'weekday')
}

function addDays(date: string, amount: number) { const next = new Date(`${date}T12:00:00Z`); next.setUTCDate(next.getUTCDate() + amount); return `${next.getUTCFullYear()}-${String(next.getUTCMonth() + 1).padStart(2, '0')}-${String(next.getUTCDate()).padStart(2, '0')}` }
function localDate(date: Date, timeZone: string) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(date)
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((item) => item.type === type)!.value
  return `${part('year')}-${part('month')}-${part('day')}`
}
