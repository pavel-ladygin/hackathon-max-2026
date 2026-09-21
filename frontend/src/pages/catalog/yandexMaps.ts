export type YandexCoordinates = [number, number]

export interface YandexMap {
  addChild(child: unknown): YandexMap
  destroy(): void
}

interface YandexMapConstructor {
  new (container: HTMLElement, options: { location: { center: YandexCoordinates; zoom: number } }): YandexMap
}

interface YandexMarkerConstructor {
  new (options: { coordinates: YandexCoordinates }, element: HTMLElement): unknown
}

export interface YandexMapsApi {
  ready: Promise<void>
  YMap: YandexMapConstructor
  YMapDefaultSchemeLayer: new () => unknown
  YMapDefaultFeaturesLayer: new () => unknown
  YMapMarker: YandexMarkerConstructor
}

declare global {
  interface Window {
    ymaps3?: YandexMapsApi
  }
}

let apiPromise: Promise<YandexMapsApi> | null = null

export function loadYandexMaps(apiKey: string): Promise<YandexMapsApi> {
  if (typeof window === 'undefined') return Promise.reject(new Error('Yandex Maps is available only in a browser'))
  if (window.ymaps3) return window.ymaps3.ready.then(() => window.ymaps3 as YandexMapsApi)
  if (apiPromise) return apiPromise

  apiPromise = new Promise<YandexMapsApi>((resolve, reject) => {
    const script = document.createElement('script')
    script.async = true
    script.dataset.yandexMapsApi = 'true'
    script.src = `https://api-maps.yandex.ru/v3/?apikey=${encodeURIComponent(apiKey)}&lang=ru_RU`
    script.addEventListener('load', () => {
      if (!window.ymaps3) {
        reject(new Error('Yandex Maps API did not expose ymaps3'))
        return
      }
      void window.ymaps3.ready.then(() => resolve(window.ymaps3 as YandexMapsApi), reject)
    }, { once: true })
    script.addEventListener('error', () => reject(new Error('Yandex Maps API failed to load')), { once: true })
    document.head.appendChild(script)
  })

  return apiPromise
}
