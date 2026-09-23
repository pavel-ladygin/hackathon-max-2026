import type { CategorySlug } from '../api/types'

const categoryLabels: Record<CategorySlug, string> = {
  concerts: 'Концерт',
  cinema: 'Кино',
  theatre: 'Театр',
  standup: 'Стендап',
  exhibitions: 'Выставка',
  sports: 'Спорт',
  food: 'Еда',
  parties: 'Вечеринка',
  festivals: 'Фестиваль',
  walks: 'Прогулка',
  other: 'Событие',
}

const categoryImages: Record<CategorySlug, string> = {
  concerts: '/events/concert-singer.png',
  cinema: '/events/contemporary-gallery.png',
  theatre: '/events/contemporary-gallery.png',
  standup: '/events/jazz-comedy.png',
  exhibitions: '/events/contemporary-gallery.png',
  sports: '/events/rooftop-dinner.png',
  food: '/events/rooftop-dinner.png',
  parties: '/events/concert-singer.png',
  festivals: '/events/concert-singer.png',
  walks: '/events/rooftop-dinner.png',
  other: '/events/jazz-comedy.png',
}

export function eventImageFallback(category: CategorySlug | string) {
  return categoryImages[category as CategorySlug] ?? categoryImages.other
}

export function eventCategoryLabel(category: CategorySlug | string) {
  return categoryLabels[category as CategorySlug] ?? 'Событие'
}

const uploadcareId = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function normalizeEventImageUrl(value: string) {
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.hostname !== 'ucare.timepad.ru' || url.username || url.password) return value
    const id = url.pathname.split('/')[1]
    if (!uploadcareId.test(id)) return value
    const path = url.pathname
    const previews = path.match(/\/-\/preview\//gi)?.length ?? 0
    const formats = path.match(/\/-\/format\//gi)?.length ?? 0
    const posters = path.match(/\/poster_[^/]+/gi)?.length ?? 0
    if (previews < 2 && formats < 2 && posters < 2) return value
    return `https://ucare.timepad.ru/${id}/-/preview/1200x1200/`
  } catch {
    return value
  }
}

export function eventImage(imageUrl: string | null | undefined, category: CategorySlug | string) {
  return imageUrl ? normalizeEventImageUrl(imageUrl) : eventImageFallback(category)
}
