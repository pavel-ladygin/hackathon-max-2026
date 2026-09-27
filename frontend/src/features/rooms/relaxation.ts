import type { CategorySlug, DayType, RoomIntentRequestDto, RoomSnapshot } from '../../shared/api/types'

export const RELAXATION_CATEGORIES: Array<{ slug: CategorySlug; label: string }> = [
  { slug: 'concerts', label: 'Концерты' },
  { slug: 'theatre', label: 'Театр' },
  { slug: 'standup', label: 'Стендап' },
  { slug: 'exhibitions', label: 'Выставки' },
  { slug: 'cinema', label: 'Кино' },
  { slug: 'food', label: 'Еда' },
]

const MAX_RELAXED_BUDGET_RUB = 10_000
const BUDGET_STEP_RUB = 500

export type Relaxation = {
  id: 'budget' | 'category' | 'date' | 'time'
  title: string
  intent: RoomIntentRequestDto | null
  dates?: string[]
  budgets?: number[]
  categories?: Array<{ slug: CategorySlug; label: string }>
}

export function relaxedIntent(current: RoomSnapshot['myIntent'], kind: Relaxation['id'], now = new Date(), selectedValue?: string | number): RoomIntentRequestDto {
  const today = localDate(now)
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
    if (typeof selectedValue !== 'string' || !availableDates(base.dates, base.day_types, now).includes(selectedValue)) throw new Error('An explicit available date is required to expand room intent')
    base.dates = [...new Set([...base.dates, selectedValue])].sort()
  }
  if (kind === 'time') base.time_slots = ['morning', 'day', 'evening', 'night']
  return base
}

export function availableRelaxations(current: RoomSnapshot['myIntent'], now = new Date()): Relaxation[] {
  const baseline = baseIntent(current, localDate(now))
  const candidates: Relaxation[] = []
  const dates = availableDates(baseline.dates, baseline.day_types, now)
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
  if (baseline.time_slots.length > 0 && baseline.time_slots.length < 4) candidates.push({ id: 'time', title: 'Разрешить любое время', intent: relaxedIntent(current, 'time', now) })
  return candidates.filter(({ id, intent }) => id === 'date' || intent === null || changed(baseline, intent, id))
}

export function availableDates(dates: string[], dayTypes: DayType[], now = new Date()) {
  const choices: string[] = []
  for (let offset = 1; offset <= 14; offset += 1) {
    const date = addDays(now, offset)
    const candidate = localDate(date)
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

function baseIntent(current: RoomSnapshot['myIntent'], today: string): RoomIntentRequestDto {
  return current ? { dates: current.dates.filter((date) => date >= today), day_types: current.day_types, time_slots: current.time_slots, category_slugs: current.category_slugs, budget_max_minor: current.budget_max_minor, radius_m: null, exclusion_slugs: current.exclusion_slugs, location: null, free_text: current.free_text } : { dates: [today], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: null, exclusion_slugs: [], location: null, free_text: null }
}

function compatibleDayType(date: Date, dayTypes: DayType[]) {
  if (dayTypes.length === 0) return true
  const isWeekend = date.getDay() === 0 || date.getDay() === 6
  return dayTypes.includes(isWeekend ? 'weekend' : 'weekday')
}

function addDays(date: Date, amount: number) { const next = new Date(date); next.setDate(next.getDate() + amount); return next }
function localDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
