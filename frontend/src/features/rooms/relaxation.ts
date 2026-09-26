import type { DayType, RoomIntentRequestDto, RoomSnapshot } from '../../shared/api/types'

export type Relaxation = { id: 'budget' | 'date' | 'time'; title: string; intent: RoomIntentRequestDto }

export function relaxedIntent(current: RoomSnapshot['myIntent'], kind: Relaxation['id'], now = new Date()): RoomIntentRequestDto {
  const today = localDate(now)
  const base = baseIntent(current, today)
  if (kind === 'budget') base.budget_max_minor = Math.max(base.budget_max_minor, 350_000)
  if (kind === 'date') {
    const date = nextAvailableDate(base.dates, base.day_types, now)
    if (date) base.dates = [...new Set([...base.dates, date])].sort()
  }
  if (kind === 'time') base.time_slots = ['morning', 'day', 'evening', 'night']
  return base
}

export function availableRelaxations(current: RoomSnapshot['myIntent'], now = new Date()): Relaxation[] {
  const baseline = baseIntent(current, localDate(now))
  const candidates: Relaxation[] = []
  if (baseline.budget_max_minor < 350_000) candidates.push({ id: 'budget', title: 'Увеличить бюджет до 3 500 ₽', intent: relaxedIntent(current, 'budget', now) })
  if (baseline.dates.length < 14 && nextAvailableDate(baseline.dates, baseline.day_types, now)) candidates.push({ id: 'date', title: 'Добавить ещё одну дату', intent: relaxedIntent(current, 'date', now) })
  if (baseline.time_slots.length > 0 && baseline.time_slots.length < 4) candidates.push({ id: 'time', title: 'Разрешить любое время', intent: relaxedIntent(current, 'time', now) })
  return candidates.filter(({ id, intent }) => changed(baseline, intent, id))
}

function changed(base: RoomIntentRequestDto, candidate: RoomIntentRequestDto, kind: Relaxation['id']) {
  if (kind === 'budget') return candidate.budget_max_minor !== base.budget_max_minor
  if (kind === 'date') return candidate.dates.length !== base.dates.length
  return candidate.time_slots.length !== base.time_slots.length
}

function baseIntent(current: RoomSnapshot['myIntent'], today: string): RoomIntentRequestDto {
  const base: RoomIntentRequestDto = current ? { dates: current.dates.filter((date) => date >= today), day_types: current.day_types, time_slots: current.time_slots, category_slugs: current.category_slugs, budget_max_minor: current.budget_max_minor, radius_m: null, exclusion_slugs: current.exclusion_slugs, location: null, free_text: current.free_text } : { dates: [today], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: null, exclusion_slugs: [], location: null, free_text: null }
  if (!base.dates.length) base.dates = [nextCompatibleDate(current?.day_types ?? [], new Date(`${today}T12:00:00`))]
  return base
}

function nextAvailableDate(dates: string[], dayTypes: DayType[], now: Date) {
  for (let offset = 1; offset <= 14; offset += 1) {
    const candidateDate = addDays(now, offset)
    const candidate = localDate(candidateDate)
    if (!dates.includes(candidate) && compatibleDayType(candidateDate, dayTypes)) return candidate
  }
  return null
}

function nextCompatibleDate(dayTypes: DayType[], now: Date) {
  for (let offset = 0; offset <= 7; offset += 1) {
    const candidate = addDays(now, offset)
    if (compatibleDayType(candidate, dayTypes)) return localDate(candidate)
  }
  return localDate(now)
}

function compatibleDayType(date: Date, dayTypes: DayType[]) {
  if (dayTypes.length === 0) return true
  const isWeekend = date.getDay() === 0 || date.getDay() === 6
  return dayTypes.includes(isWeekend ? 'weekend' : 'weekday')
}

function addDays(date: Date, amount: number) { const next = new Date(date); next.setDate(next.getDate() + amount); return next }
function localDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
