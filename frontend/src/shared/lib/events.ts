import type { CategorySlug } from '../api/types'
import { CATEGORY_REGISTRY } from '../api/categories'

const unavailablePhoto = '/events/photo-unavailable.png'

export const eventImageFallback: (category: CategorySlug | string) => string = () => unavailablePhoto

export function eventCategoryLabel(category: CategorySlug | string) {
  return CATEGORY_REGISTRY[category as CategorySlug]?.eventLabel ?? 'Событие'
}

const uploadcareId = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function normalizeEventImageUrl(value: string) {
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.hostname !== 'ucare.timepad.ru' || url.username || url.password) return value
    const id = url.pathname.split('/')[1]
    if (!uploadcareId.test(id)) return value
    // Uploadcare's UUID endpoint serves the stored original. A generated
    // preview can be only 308px wide and is visibly soft when used as a hero.
    return `https://ucare.timepad.ru/${id}/`
  } catch {
    return value
  }
}

export function eventImage(imageUrl: string | null | undefined, category: CategorySlug | string, maxDimension = 256) {
  if (!imageUrl) return eventImageFallback(category)
  const normalized = normalizeEventImageUrl(imageUrl)
  const match = /^https:\/\/ucare\.timepad\.ru\/([0-9a-f-]{36})\/$/i.exec(normalized)
  return match ? `${normalized}-/preview/${maxDimension}x${maxDimension}/` : normalized
}

export function eventImageSrcSet(imageUrl: string | null | undefined, category: CategorySlug | string) {
  if (!imageUrl) return undefined
  const small = eventImage(imageUrl, category, 640)
  const large = eventImage(imageUrl, category, 1200)
  return small === large ? undefined : `${small} 640w, ${large} 1200w`
}

export function eventHeroImage(images: EventImageCandidate[] | null | undefined, imageUrl: string | null | undefined, category: CategorySlug | string, maxDimension = 1200) {
  const candidates = (images ?? []).filter((image) => image.url)
  const selected = [...candidates].sort((a, b) => {
    const aHasSize = a.width != null && a.height != null
    const bHasSize = b.width != null && b.height != null
    if (aHasSize !== bHasSize) return aHasSize ? -1 : 1
    if (aHasSize && bHasSize) {
      const size = b.width! * b.height! - a.width! * a.height!
      if (size) return size
    }
    return Number(b.role === 'hero') - Number(a.role === 'hero')
  })[0]
  return eventImage(selected?.url ?? imageUrl, category, maxDimension)
}

type EventImageCandidate = { url: string; width: number | null; height: number | null; role: 'card' | 'hero' | 'gallery' }
