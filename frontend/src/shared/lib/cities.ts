/** Curated city choices shown throughout discovery and room creation. */
export const MOSCOW_CITY_ID = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
export const SAINT_PETERSBURG_CITY_ID = 'bf61fbf5-8c5b-53da-96c7-3560df3f8177'
export const KAZAN_CITY_ID = '3d3c83de-691b-510e-85e5-79685c4012ee'

export type CityOption = {
  id: string
  name: string
  timezone: string
  center: readonly [latitude: number, longitude: number]
  ready: boolean
  aliases: readonly string[]
}

export const CITIES: readonly CityOption[] = [
  { id: MOSCOW_CITY_ID, name: 'Москва', timezone: 'Europe/Moscow', center: [55.7558, 37.6173], ready: true, aliases: ['мск', 'мсква'] },
  { id: SAINT_PETERSBURG_CITY_ID, name: 'Санкт-Петербург', timezone: 'Europe/Moscow', center: [59.939095, 30.315868], ready: true, aliases: ['питер', 'спб', 'санкт петербург'] },
  { id: 'city:novosibirsk', name: 'Новосибирск', timezone: 'Asia/Novosibirsk', center: [55.008353, 82.935733], ready: false, aliases: ['новосиб'] },
  { id: 'city:yekaterinburg', name: 'Екатеринбург', timezone: 'Asia/Yekaterinburg', center: [56.838011, 60.597465], ready: false, aliases: ['екб'] },
  { id: KAZAN_CITY_ID, name: 'Казань', timezone: 'Europe/Moscow', center: [55.78874, 49.12214], ready: false, aliases: [] },
  { id: 'city:nizhny-novgorod', name: 'Нижний Новгород', timezone: 'Europe/Moscow', center: [56.296504, 43.936059], ready: false, aliases: ['нижний'] },
]

export const ACTIVE_CITIES = CITIES.filter((city) => city.ready)

export function cityById(id?: string | null): CityOption | undefined {
  return CITIES.find((city) => city.id === id)
}

function normalizeCityQuery(value: string): string {
  return value.toLocaleLowerCase('ru-RU').replace(/[\s\p{Dash_Punctuation}]+/gu, '')
}

export function searchCities(query: string): CityOption[] {
  const normalizedQuery = normalizeCityQuery(query)
  if (!normalizedQuery) return [...CITIES]
  return CITIES.filter((city) => [city.name, ...city.aliases]
    .some((value) => normalizeCityQuery(value).includes(normalizedQuery)))
}
