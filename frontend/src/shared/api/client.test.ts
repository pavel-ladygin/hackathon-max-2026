import { describe, expect, it, vi } from 'vitest'
import { ApiClient } from './client'

describe('API client', () => {
  it('uploads a poster as multipart with auth and reports progress', async () => {
    const fakeXHR = {
      upload: { onprogress: null as ((event: ProgressEvent) => void) | null },
      open: vi.fn(), setRequestHeader: vi.fn(), send: vi.fn(), abort: vi.fn(),
      status: 0, responseText: '', onerror: null as (() => void) | null,
      onabort: null as (() => void) | null, onload: null as ((event: ProgressEvent) => void) | null,
      getResponseHeader: vi.fn(() => null),
    }
    const xhr = fakeXHR as unknown as XMLHttpRequest
    const client = new ApiClient({ baseUrl: '/api/v1', getToken: () => 'token-1', xhrFactory: () => xhr })
    const progress = vi.fn()
    const file = new File(['poster'], 'poster.png', { type: 'image/png' })
    const response = client.uploadEventSubmissionImage(file, { onProgress: progress })

    expect(fakeXHR.open).toHaveBeenCalledWith('POST', '/api/v1/event-submissions/images')
    expect(fakeXHR.setRequestHeader).toHaveBeenCalledWith('Authorization', 'Bearer token-1')
    expect(fakeXHR.setRequestHeader).toHaveBeenCalledWith('Accept', 'application/json')
    const form = fakeXHR.send.mock.calls[0]?.[0] as FormData
    expect(form.get('file')).toBe(file)
    expect(fakeXHR.setRequestHeader.mock.calls.map(([name]) => name)).not.toContain('Content-Type')
    fakeXHR.upload.onprogress?.({ lengthComputable: true, total: 100, loaded: 42 } as ProgressEvent)
    expect(progress).toHaveBeenCalledWith(42)
    fakeXHR.status = 201
    fakeXHR.responseText = '{"image_id":"upload-1"}'
    fakeXHR.onload?.(new ProgressEvent('load'))
    await expect(response).resolves.toEqual({ image_id: 'upload-1' })
  })

  it('updates notification preferences through the authenticated endpoint', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () => Response.json({ daily_notifications_enabled: true }))
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl, getToken: () => 'token-1' })

    await expect(client.updateNotificationPreferences({ daily_notifications_enabled: true })).resolves.toEqual({ daily_notifications_enabled: true })

    expect(String(fetchImpl.mock.calls[0][0])).toBe('/api/v1/me/notification-preferences')
    expect(fetchImpl.mock.calls[0][1]?.method).toBe('PATCH')
    expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body))).toEqual({ daily_notifications_enabled: true })
    expect(new Headers(fetchImpl.mock.calls[0][1]?.headers).get('Authorization')).toBe('Bearer token-1')
  })

  it('uses the authenticated HTTP API for discovery', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async (_input, init) => {
      const body = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
      if ('init_data' in body) {
        return Response.json({
          access_token: 'real-access-token',
          user: { id: 'user-1', display_name: 'Иван', avatar_url: null, city_id: null, locale: 'ru' },
          onboarding_state: 'new',
          preferences: null,
          daily_notifications_enabled: false,
          invite_context: null,
        })
      }
      if (String(_input).includes('/feed/home')) return Response.json({ feed_id: 'feed-real', generated_at: '', sections: [], active_room: null })
      return Response.json({
        ...body,
        version: 1,
        updated_at: '2026-09-20T00:00:00Z',
      })
    })
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl })

    const bootstrap = await client.bootstrap({ init_data: 'signed-max-data', start_param: null })
    expect(bootstrap.dailyNotificationsEnabled).toBe(false)
    await client.replacePreferences({
      city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24',
      interest_slugs: ['concerts'],
      budget_max_minor: 350_000,
      usual_day_types: ['weekend'],
      usual_time_slots: ['evening'],
    })
    const feed = await client.getHomeFeed()

    expect(fetchImpl).toHaveBeenCalledTimes(3)
    expect(String(fetchImpl.mock.calls[0][0])).toBe('/api/v1/auth/max/bootstrap')
    expect(String(fetchImpl.mock.calls[1][0])).toBe('/api/v1/me/preferences')
    expect(new Headers(fetchImpl.mock.calls[1][1]?.headers).get('Authorization')).toBe('Bearer real-access-token')
    expect(String(fetchImpl.mock.calls[2][0])).toBe('/api/v1/feed/home')
    expect(feed.feed_id).toBe('feed-real')
  })

  it('returns the server-validated shared event ID from bootstrap', async () => {
    const eventId = '6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1'
    const fetchImpl = vi.fn<typeof fetch>(async () => Response.json({
      access_token: 'real-access-token',
      user: { id: 'user-1', display_name: 'Иван', avatar_url: null, city_id: null, locale: 'ru' },
      onboarding_state: 'complete', preferences: null, daily_notifications_enabled: true, invite_context: null, shared_event_id: eventId,
    }))
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl })

    const bootstrap = await client.bootstrap({ init_data: 'signed-max-data', start_param: `event_${eventId}` })

    expect(bootstrap.sharedEventId).toBe(eventId)
    expect(bootstrap.dailyNotificationsEnabled).toBe(true)
  })
})
