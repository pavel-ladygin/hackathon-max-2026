import { describe, expect, it } from 'vitest'
import { ApiError, normalizeApiError } from './errors'

describe('ApiError', () => {
  it('maps validation details to fields without exposing raw server text', () => {
    const error = new ApiError(400, { error: { code: 'VALIDATION_FAILED', message: 'raw internal message', request_id: 'request-1', details: { fields: [{ field: 'name', reason: 'Слишком длинное значение' }] } } })
    expect(error.message).toBe('Проверьте заполненные поля.')
    expect(error.fieldErrors).toEqual({ name: 'Слишком длинное значение' })
    expect(error.requestId).toBe('request-1')
    expect(error.kind).toBe('validation')
  })

  it('marks retryable errors and parses Retry-After', () => {
    const error = new ApiError(429, { error: { code: 'RATE_LIMITED' } }, '12')
    expect(error.retryable).toBe(true)
    expect(error.retryAfterSeconds).toBe(12)
  })

  it('normalizes transport failures', () => {
    const error = normalizeApiError(new TypeError('Failed to fetch'))
    expect(error.status).toBe(0)
    expect(error.retryable).toBe(true)
    expect(error.message).not.toContain('Failed to fetch')
  })
})
