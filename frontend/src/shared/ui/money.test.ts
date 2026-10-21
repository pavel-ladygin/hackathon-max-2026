import { describe, expect, it } from 'vitest'
import { decimalToMinor } from './money'

describe('decimalToMinor', () => {
  it.each([['12,34', 1234], ['12.34', 1234], ['5,6', 560], ['0', 0]])('parses %s into exact kopecks', (value, expected) => {
    expect(decimalToMinor(value)).toBe(expected)
  })

  it.each(['3,', '1.234', '-1', '1,2.3', ''])('rejects incomplete or invalid value %s without rounding', (value) => {
    expect(decimalToMinor(value)).toBeNull()
  })
})
