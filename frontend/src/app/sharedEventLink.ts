function decodePathSegment(value: string): string {
  try { return decodeURIComponent(value) } catch { return value }
}

export function eventIdFromStartParam(startParam: string | null): string | null {
  const match = startParam?.match(/^event_([0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12})$/i)
  return match?.[1] ?? null
}

export function getOpenInMaxStartParam(pathname: string): string | null {
  const inviteToken = pathname.match(/^\/join\/([^/]+)$/)?.[1]
  if (inviteToken) return decodePathSegment(inviteToken)

  const eventId = pathname.match(/^\/events\/([^/]+)$/)?.[1]
  if (!eventId) return null
  const startParam = `event_${decodePathSegment(eventId)}`
  return eventIdFromStartParam(startParam) ? startParam : null
}
