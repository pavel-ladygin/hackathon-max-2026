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

export function eventImage(imageUrl: string | null | undefined, category: CategorySlug | string) {
  return imageUrl || eventImageFallback(category)
}
