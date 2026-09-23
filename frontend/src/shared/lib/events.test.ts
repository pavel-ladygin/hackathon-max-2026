import { describe, expect, it } from 'vitest'
import { eventImage, normalizeEventImageUrl } from './events'

const id = '6167e34b-0b30-4a3a-8d5a-3f9bc0cabe26'

describe('Timepad poster URLs', () => {
  it('repairs repeated image transforms in an already saved URL', () => {
    const broken = `https://ucare.timepad.ru/${id}/-/preview/308x600/-/format/jpeg/-/format/jpeg/poster_org_418499.jpg/-/preview/308x600/-/format/jpeg/poster_event_4202945.jpg`
    expect(eventImage(broken, 'concerts')).toBe(`https://ucare.timepad.ru/${id}/-/preview/1200x1200/`)
  })

  it('keeps valid Timepad and other provider images unchanged', () => {
    const valid = `https://ucare.timepad.ru/${id}/-/preview/308x600/-/format/jpeg/poster_event_4184239.jpg`
    expect(normalizeEventImageUrl(valid)).toBe(valid)
    expect(normalizeEventImageUrl('https://kudago.com/media/images/event/photo.jpg')).toBe('https://kudago.com/media/images/event/photo.jpg')
  })

  it('does not rewrite URLs without a valid Timepad upload ID', () => {
    const invalid = 'https://ucare.timepad.ru/not-an-id/-/preview/308x600/-/preview/600x600/'
    expect(normalizeEventImageUrl(invalid)).toBe(invalid)
  })
})
