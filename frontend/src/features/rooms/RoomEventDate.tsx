function dateWord(count: number): string {
  const lastTwo = count % 100
  if (lastTwo >= 11 && lastTwo <= 14) return 'дат'
  const last = count % 10
  if (last === 1) return 'дата'
  if (last >= 2 && last <= 4) return 'даты'
  return 'дат'
}

export function RoomEventDate({ label, otherOccurrencesCount }: { label: string; otherOccurrencesCount?: number }) {
  return <span>◷ {label}{otherOccurrencesCount !== undefined && otherOccurrencesCount > 0 ? ` · ещё ${otherOccurrencesCount} ${dateWord(otherOccurrencesCount)}` : null}</span>
}
