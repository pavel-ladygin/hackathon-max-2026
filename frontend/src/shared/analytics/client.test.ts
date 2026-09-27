import { beforeEach, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'

const recordBehavior = vi.hoisted(() => vi.fn())

vi.mock('../api/client', () => ({ apiClient: { recordBehavior } }))
vi.mock('../platform/max/adapter', () => ({ maxPlatform: { isMax: false, getPlatform: () => null } }))

beforeEach(() => {
  vi.resetModules()
  recordBehavior.mockReset()
  window.localStorage.clear()
})

it('retries an undelivered event with its original client_event_id', async () => {
  recordBehavior.mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ accepted: 2, duplicates: 0, rejected: 0 })
  const analytics = await import('./client')
  analytics.activateAnalytics('user-1')
  analytics.track('room_creation_started')
  await waitFor(() => expect(recordBehavior).toHaveBeenCalledTimes(1))
  window.dispatchEvent(new Event('online'))
  await waitFor(() => expect(recordBehavior).toHaveBeenCalledTimes(2))
  const first = recordBehavior.mock.calls[0][0][0]
  const second = recordBehavior.mock.calls[1][0][0]
  expect(second.client_event_id).toBe(first.client_event_id)
  await waitFor(() => expect(JSON.parse(window.localStorage.getItem('max.analytics.queue.v1') ?? '{}').events).toEqual([]))
})

it('does not deliver a previous user queue under a new user identity', async () => {
  recordBehavior.mockRejectedValue(new Error('offline'))
  const analytics = await import('./client')
  analytics.activateAnalytics('user-1')
  analytics.track('room_creation_started')
  await waitFor(() => expect(recordBehavior).toHaveBeenCalledTimes(1))
  recordBehavior.mockReset().mockResolvedValue({ accepted: 0, duplicates: 0, rejected: 0 })
  analytics.activateAnalytics('user-2')
  expect(JSON.parse(window.localStorage.getItem('max.analytics.queue.v1') ?? '{}')).toMatchObject({ userId: 'user-2', events: [] })
  expect(recordBehavior).not.toHaveBeenCalled()
})

it('omits free-form text from the persisted retry queue', async () => {
  recordBehavior.mockRejectedValue(new Error('offline'))
  const analytics = await import('./client')
  analytics.activateAnalytics('user-1')
  analytics.track('search_performed', { properties: { query_length: 12, search_query: 'personal search text' } })
  await waitFor(() => expect(recordBehavior).toHaveBeenCalledTimes(1))
  const stored = window.localStorage.getItem('max.analytics.queue.v1') ?? ''
  expect(stored).not.toContain('personal search text')
  expect(JSON.parse(stored).events.at(-1).properties).toEqual({ query_length: 12 })
})
