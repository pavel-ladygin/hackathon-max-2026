export type YandexCoordinates = [number, number]

/** Keep event thumbnails within a usable range while following map zoom. */
export function markerSizeForZoom(zoom: number) {
  return Math.min(64, Math.max(36, 48 + (zoom - 11) * 4))
}

export interface YandexMap {
  addChild(child: unknown): YandexMap
  removeChild(child: unknown): YandexMap
  destroy(): void
}

interface YandexMapConstructor {
  new (container: HTMLElement, options: { location: { center: YandexCoordinates; zoom: number } }): YandexMap
}

interface YandexMarkerConstructor {
  new (options: { coordinates: YandexCoordinates }, element: HTMLElement): unknown
}

export interface YandexMapUpdateEvent {
  location?: { center?: YandexCoordinates; zoom?: number }
  camera?: { center?: YandexCoordinates; zoom?: number }
}

export interface YandexCamera { center: YandexCoordinates; zoom: number }

/** Approximate the visible viewport's half-diagonal and clamp to the search API's 50 km limit. */
export function radiusForViewport(zoom: number, width: number, height: number) {
  const safeZoom = Math.min(22, Math.max(0, zoom))
  const halfDiagonalPixels = Math.hypot(Math.max(1, width), Math.max(1, height)) / 2
  const metersPerPixel = 40_075_000 / (256 * 2 ** safeZoom)
  return Math.round(Math.min(50_000, Math.max(1_000, halfDiagonalPixels * metersPerPixel)))
}

interface YandexMapListenerOptions {
  onUpdate?: (event: YandexMapUpdateEvent) => void
}

interface YandexMapListenerConstructor {
  new (options: YandexMapListenerOptions): unknown
}

export interface YandexMapsApi {
  ready: Promise<void>
  YMap: YandexMapConstructor
  YMapDefaultSchemeLayer: new () => unknown
  YMapDefaultFeaturesLayer: new () => unknown
  YMapMarker: YandexMarkerConstructor
  YMapListener: YandexMapListenerConstructor
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
