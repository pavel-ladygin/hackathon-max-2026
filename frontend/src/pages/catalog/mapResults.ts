import type { MapBounds } from '../../shared/api/types'

const MAX_MERCATOR_LAT = 85.05112878
const WORLD_SIZE = 256

export function normalizeLongitude(longitude: number) {
  return ((longitude + 180) % 360 + 360) % 360 - 180
}

function mercatorY(latitude: number) {
  const radians = Math.max(-MAX_MERCATOR_LAT, Math.min(MAX_MERCATOR_LAT, latitude)) * Math.PI / 180
  return (1 - Math.asinh(Math.tan(radians)) / Math.PI) / 2
}

function latitudeAtY(y: number) {
  return Math.atan(Math.sinh(Math.PI * (1 - 2 * y))) * 180 / Math.PI
}

/** Geographic viewport expanded by 25% on each side, using the map's Web Mercator scale. */
export function paddedViewportBounds(center: [number, number], zoom: number, width: number, height: number, padding = 0.25): MapBounds {
  const scale = WORLD_SIZE * 2 ** Math.max(0, Math.min(22, zoom))
  const horizontal = Math.min(1, Math.max(1, width) * (1 + 2 * padding) / scale)
  const vertical = Math.min(1, Math.max(1, height) * (1 + 2 * padding) / scale)
  const x = ((center[0] + 180) / 360 + 1) % 1
  const y = mercatorY(center[1])
  const west = normalizeLongitude((x - horizontal / 2) * 360 - 180)
  const east = normalizeLongitude((x + horizontal / 2) * 360 - 180)
  const north = latitudeAtY(Math.max(0, y - vertical / 2))
  const south = latitudeAtY(Math.min(1, y + vertical / 2))
  if (horizontal >= 1) return { west: -180, east: 180, south, north }
  return { west, east, south, north }
}

function longitudeStart(value: number) { return ((value + 180) % 360 + 360) % 360 }
function longitudeSpan(bounds: MapBounds) {
  if (bounds.west === -180 && bounds.east === 180) return 360
  return ((bounds.east - bounds.west) % 360 + 360) % 360
}

export function boundsContain(outer: MapBounds, inner: MapBounds) {
  const outerSpan = longitudeSpan(outer)
  const innerSpan = longitudeSpan(inner)
  const longitudeFits = outerSpan >= 360 || (innerSpan <= outerSpan && ((longitudeStart(inner.west) - longitudeStart(outer.west) + 360) % 360) + innerSpan <= outerSpan)
  return longitudeFits && outer.south <= inner.south && outer.north >= inner.north
}

export type TimedArea<T> = { bounds: MapBounds; value: T; savedAt: number }

/** Small LRU cache for successful map responses; misses and failed requests are never stored. */
export class MapAreaCache<T> {
  private readonly areas = new Map<string, TimedArea<T>>()
  constructor(private readonly maxAreas = 20, private readonly maxAgeMs = 5 * 60_000) {}
  get(key: string, bounds: MapBounds, now = Date.now()): T | undefined {
    for (const [areaKey, area] of this.areas) {
      if (!areaKey.startsWith(`${key}\u0000`)) continue
      if (now - area.savedAt > this.maxAgeMs) { this.areas.delete(areaKey); continue }
      if (!boundsContain(area.bounds, bounds)) continue
      this.areas.delete(areaKey)
      this.areas.set(areaKey, area)
      return area.value
    }
    return undefined
  }
  set(key: string, bounds: MapBounds, value: T, now = Date.now()) {
    const id = `${key}\u0000${now}\u0000${Math.random()}`
    this.areas.set(id, { bounds, value, savedAt: now })
    while (this.areas.size > this.maxAreas) this.areas.delete(this.areas.keys().next().value as string)
  }
  clear() { this.areas.clear() }
}
