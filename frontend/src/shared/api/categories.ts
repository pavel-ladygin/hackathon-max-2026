import type { CategorySlug } from './types'

/** Shared category metadata. Record enforces labels for every OpenAPI slug. */
export const CATEGORY_REGISTRY: Record<CategorySlug, { label: string; eventLabel: string }> = {
  concerts: { label: 'Концерты', eventLabel: 'Концерт' },
  cinema: { label: 'Кино', eventLabel: 'Кино' },
  theatre: { label: 'Театр', eventLabel: 'Театр' },
  standup: { label: 'Стендап', eventLabel: 'Стендап' },
  exhibitions: { label: 'Выставки', eventLabel: 'Выставка' },
  sports: { label: 'Спорт', eventLabel: 'Спорт' },
  food: { label: 'Еда', eventLabel: 'Еда' },
  parties: { label: 'Вечеринки', eventLabel: 'Вечеринка' },
  festivals: { label: 'Фестивали', eventLabel: 'Фестиваль' },
  walks: { label: 'Прогулки', eventLabel: 'Прогулка' },
  other: { label: 'Другое', eventLabel: 'Событие' },
}

export const CATEGORY_SLUGS = Object.keys(CATEGORY_REGISTRY) as [CategorySlug, ...CategorySlug[]]
export const CATEGORY_OPTIONS = CATEGORY_SLUGS.map((slug) => ({ slug, ...CATEGORY_REGISTRY[slug] }))
