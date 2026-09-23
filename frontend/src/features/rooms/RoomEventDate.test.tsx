import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RoomEventDate } from './RoomEventDate'

describe('room event date', () => {
  it.each([
    [1, 'ещё 1 дата'],
    [2, 'ещё 2 даты'],
    [5, 'ещё 5 дат'],
    [11, 'ещё 11 дат'],
    [21, 'ещё 21 дата'],
  ])('shows %i other occurrences as %s', (count, text) => {
    render(<RoomEventDate label="19 сентября, 19:30" otherOccurrencesCount={count} />)
    expect(screen.getByText(`◷ 19 сентября, 19:30 · ${text}`)).toBeVisible()
  })

  it.each([0, undefined])('keeps only the primary date for %s', (count) => {
    render(<RoomEventDate label="19 сентября, 19:30" otherOccurrencesCount={count} />)
    expect(screen.getByText('◷ 19 сентября, 19:30')).toBeVisible()
    expect(screen.queryByText(/ещё/)).not.toBeInTheDocument()
  })
})
