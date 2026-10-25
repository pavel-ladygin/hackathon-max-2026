import { describe, expect, it } from 'vitest'
import { timeZoneLabel } from './timeZoneLabel'

describe('timeZoneLabel', () => {
  it('uses a Russian label for Moscow time', () => {
    expect(timeZoneLabel('Europe/Moscow')).toBe('Московское время')
  })

  it('names the local time zone for other registered Russian cities', () => {
    expect(timeZoneLabel('Europe/Kaliningrad')).toBe('Калининградское время')
    expect(timeZoneLabel('Asia/Yekaterinburg')).toBe('Екатеринбургское время')
    expect(timeZoneLabel('Asia/Novosibirsk')).toBe('Новосибирское время')
  })

  it('does not expose an unknown IANA identifier to users', () => {
    expect(timeZoneLabel('Pacific/Apia')).toBe('Местное время')
  })
})
