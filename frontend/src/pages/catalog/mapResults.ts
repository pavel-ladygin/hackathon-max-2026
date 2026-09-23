export function collectMapEvents<T extends { id: string }>(pages: Array<{ items: T[] }> | undefined, cap = 150) {
  const seen = new Set<string>()
  const items: T[] = []
  for (const event of pages?.flatMap((page) => page.items) ?? []) {
    if (seen.has(event.id)) continue
    seen.add(event.id)
    items.push(event)
    if (items.length >= cap) break
  }
  return items
}

export function shouldFetchMapPage(count: number, cap: number, hasNextPage: boolean, isFetching: boolean) {
  return count < cap && hasNextPage && !isFetching
}
