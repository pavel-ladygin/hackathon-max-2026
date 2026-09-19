import type { EventCardDto, EventDetailDto, RoomSnapshotDto, UserDto } from '../shared/api/types'

export const IVAN: UserDto = { id: '11111111-1111-7111-8111-111111111111', display_name: 'Иван', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru-RU' }
export const ANNA: UserDto = { id: '22222222-2222-7222-8222-222222222222', display_name: 'Анна', avatar_url: null, city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24', locale: 'ru-RU' }

type EventSeed = Pick<EventDetailDto, 'id' | 'title' | 'subtitle' | 'category_slug' | 'date_label' | 'venue_name' | 'price_from_minor' | 'price_label' | 'image_url' | 'description'>

const eventDate = (index: number) => { const value = new Date(); value.setHours(16 + (index % 5), 0, 0, 0); value.setDate(value.getDate() + index + 1); return value }
const makeEvent = (seed: EventSeed, index: number): EventDetailDto => ({
  ...seed,
  starts_at: eventDate(index).toISOString(),
  date_label: new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' }).format(eventDate(index)),
  timezone: 'Europe/Moscow',
  distance_m: 1800 + index * 650,
  distance_label: `${(1.8 + index * 0.65).toFixed(1).replace('.', ',')} км`,
  currency: 'RUB',
  saved: false,
  reasons: [
    { code: 'shared_category', text: 'Пересекается с вашими интересами' },
    { code: 'budget_fit', text: 'Вписывается в общий бюджет' },
  ],
  ends_at: null,
  venue: {
    id: `44444444-4444-7444-8444-44444444444${index}`,
    name: seed.venue_name,
    address: 'Москва, центр города',
    latitude: 55.71 + index * .018,
    longitude: 37.56 + index * .024,
    metro: 'Центр',
    district: 'Центральный',
  },
  images: [{ url: seed.image_url ?? '', width: 900, height: 700, role: 'hero' }],
  ticket_available: true,
  status: 'published',
  age_rating: '12+',
  data_provenance: { source: 'demo', source_updated_at: null, is_demo: true },
})

export const EVENTS: EventDetailDto[] = [
  makeEvent({ id: '33333333-3333-7333-8333-333333333331', title: 'Егор Крид', subtitle: 'Большой концерт', category_slug: 'concerts', date_label: '18 сентября, 19:00', venue_name: 'VK Stadium', price_from_minor: 250000, price_label: 'от 2 500 ₽', image_url: '/events/concert-singer.png', description: 'Большой сольный концерт: любимые хиты, новые треки и атмосфера живого выступления.' }, 0),
  makeEvent({ id: '33333333-3333-7333-8333-333333333332', title: 'Джазовый вечер в Оранжерее', subtitle: 'Музыка среди тропических растений', category_slug: 'concerts', date_label: '19 сентября, 19:30', venue_name: 'Оранжерея ВДНХ', price_from_minor: 180000, price_label: 'от 1 800 ₽', image_url: '/events/jazz-comedy.png', description: 'Камерный вечер живого джаза в окружении зелени и мягкого света.' }, 1),
  makeEvent({ id: '33333333-3333-7333-8333-333333333333', title: 'Современное искусство: Новая волна', subtitle: 'Выставка молодых авторов', category_slug: 'exhibitions', date_label: '20 сентября, 12:00', venue_name: 'ГЭС-2', price_from_minor: 90000, price_label: 'от 900 ₽', image_url: '/events/contemporary-gallery.png', description: 'Новые имена и свежий взгляд на современное искусство в просторных залах.' }, 2),
  makeEvent({ id: '33333333-3333-7333-8333-333333333334', title: 'Большой Stand Up вечер', subtitle: 'Лучшие комики Москвы', category_slug: 'standup', date_label: '21 сентября, 20:00', venue_name: 'StandUp Store', price_from_minor: 150000, price_label: 'от 1 500 ₽', image_url: '/events/jazz-comedy.png', description: 'Один вечер, четыре комика и истории, которые захочется пересказать друзьям.' }, 3),
  makeEvent({ id: '33333333-3333-7333-8333-333333333335', title: 'Вишнёвый сад', subtitle: 'Новая постановка', category_slug: 'theatre', date_label: '22 сентября, 19:00', venue_name: 'Театр Наций', price_from_minor: 220000, price_label: 'от 2 200 ₽', image_url: '/events/concert-singer.png', description: 'Современное прочтение классической пьесы с сильным актёрским составом.' }, 4),
  makeEvent({ id: '33333333-3333-7333-8333-333333333336', title: 'Летняя терраса', subtitle: 'Ужин с видом на город', category_slug: 'food', date_label: '23 сентября, 18:00', venue_name: 'Красный Октябрь', price_from_minor: 200000, price_label: 'от 2 000 ₽', image_url: '/events/rooftop-dinner.png', description: 'Тёплый вечер, авторское меню и город в золотом свете.' }, 5),
]

export const eventCards = (): EventCardDto[] => EVENTS.map((event) => ({
  id: event.id,
  title: event.title,
  subtitle: event.subtitle,
  category_slug: event.category_slug,
  starts_at: event.starts_at,
  timezone: event.timezone,
  date_label: event.date_label,
  venue_name: event.venue_name,
  distance_m: event.distance_m,
  distance_label: event.distance_label,
  price_from_minor: event.price_from_minor,
  currency: event.currency,
  price_label: event.price_label,
  image_url: event.image_url,
  saved: event.saved,
  reasons: event.reasons,
}))
export const now = () => new Date().toISOString()
export type MockRoom = { id: string; name: string; city_id: string; token: string; creator: string; participant: string | null; intents: Record<string, any>; votes: Record<string, Record<string, 'like' | 'dislike'>>; state: RoomSnapshotDto['state'] | (string & {}); version: number; round_no: number; created_at: string; expires_at: string; match: { id: string; matched_at: string; event: EventDetailDto; participants: any[] } | null }
