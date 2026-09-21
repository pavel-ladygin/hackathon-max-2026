import type { CategorySlug, RoomIntentRequestDto, RoomSnapshot } from '../../shared/api/types'

export function relaxedIntent(current: RoomSnapshot['myIntent'], kind: string, now = new Date()): RoomIntentRequestDto {
  const today = localDate(now)
  const base: RoomIntentRequestDto = current ? { dates: current.dates.filter((date) => date >= today), day_types: current.day_types, time_slots: current.time_slots, category_slugs: current.category_slugs, budget_max_minor: current.budget_max_minor, radius_m: current.radius_m, exclusion_slugs: current.exclusion_slugs, location: current.location, free_text: current.free_text } : { dates: [today], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 5_000, exclusion_slugs: [], location: null, free_text: null }
  if (!base.dates.length) base.dates = [today]
  if (kind === 'budget') base.budget_max_minor = Math.max(base.budget_max_minor, 350_000)
  if (kind === 'date') base.dates = [...new Set([...base.dates, localDate(nextFriday(now))])].sort()
  if (kind === 'radius' && base.radius_m !== null) base.radius_m = Math.max(base.radius_m, 10_000)
  if (kind === 'category') base.category_slugs = [...new Set([...base.category_slugs, 'exhibitions' as CategorySlug])]
  return base
}

function addDays(date: Date, amount: number) { const next = new Date(date); next.setDate(next.getDate() + amount); return next }
function nextFriday(date: Date) { return addDays(date, (5 - date.getDay() + 7) % 7 || 7) }
function localDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
