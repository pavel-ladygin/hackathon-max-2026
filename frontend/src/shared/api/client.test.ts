import { describe, expect, it, vi } from 'vitest'
import { ApiClient } from './client'

describe('hybrid API client', () => {
  it('keeps auth and preferences on HTTP while discovery stays local', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async (_input, init) => {
      const body = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
      if ('init_data' in body) {
        return Response.json({
          access_token: 'real-access-token',
          user: { id: 'user-1', display_name: 'Иван', avatar_url: null, city_id: null, locale: 'ru' },
          onboarding_state: 'new',
          preferences: null,
          invite_context: null,
        })
      }
      return Response.json({
        ...body,
        version: 1,
        updated_at: '2026-09-20T00:00:00Z',
      })
    })
    const client = new ApiClient({ baseUrl: '/api/v1', fetchImpl })

    await client.bootstrap({ init_data: 'signed-max-data', start_param: null })
    await client.replacePreferences({
      city_id: 'a0f625ee-2154-5a45-8afe-37adf955ec24',
      interest_slugs: ['concerts'],
      budget_max_minor: 350_000,
      usual_day_types: ['weekend'],
      usual_time_slots: ['evening'],
    })
    const feed = await client.getHomeFeed()

    expect(fetchImpl).toHaveBeenCalledTimes(2)
    expect(String(fetchImpl.mock.calls[0][0])).toBe('/api/v1/auth/max/bootstrap')
    expect(String(fetchImpl.mock.calls[1][0])).toBe('/api/v1/me/preferences')
    expect(new Headers(fetchImpl.mock.calls[1][1]?.headers).get('Authorization')).toBe('Bearer real-access-token')
    expect(feed.feed_id).toBe('feed-demo')
  })
})
