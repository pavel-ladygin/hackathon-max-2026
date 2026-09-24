import { describe, expect, it } from 'vitest'
import { eventHeroImage, eventImage, normalizeEventImageUrl } from './events'

const id = '6167e34b-0b30-4a3a-8d5a-3f9bc0cabe26'

describe('Timepad poster URLs', () => {
	it('uses the stored original instead of small or repeated previews', () => {
		const broken = `https://ucare.timepad.ru/${id}/-/preview/308x600/-/format/jpeg/-/format/jpeg/poster_org_418499.jpg/-/preview/308x600/-/format/jpeg/poster_event_4202945.jpg`
		expect(eventImage(broken, 'concerts')).toBe(`https://ucare.timepad.ru/${id}/`)
	})

	it('uses original for valid Timepad previews and keeps other provider images unchanged', () => {
		const valid = `https://ucare.timepad.ru/${id}/-/preview/308x600/-/format/jpeg/poster_event_4184239.jpg`
		expect(normalizeEventImageUrl(valid)).toBe(`https://ucare.timepad.ru/${id}/`)
		expect(normalizeEventImageUrl('https://kudago.com/media/images/event/photo.jpg')).toBe('https://kudago.com/media/images/event/photo.jpg')
	})

  it('does not rewrite URLs without a valid Timepad upload ID', () => {
    const invalid = 'https://ucare.timepad.ru/not-an-id/-/preview/308x600/-/preview/600x600/'
    expect(normalizeEventImageUrl(invalid)).toBe(invalid)
	})

	it('chooses the largest known image even when a smaller image is marked hero', () => {
		const images = [
			{ url: 'https://cdn.example/card.jpg', width: 1600, height: 900, role: 'card' as const },
			{ url: 'https://cdn.example/gallery.jpg', width: 2400, height: 1600, role: 'gallery' as const },
			{ url: `https://ucare.timepad.ru/${id}/-/preview/308x600/`, width: 308, height: 600, role: 'hero' as const },
		]
		expect(eventHeroImage(images, null, 'concerts')).toBe('https://cdn.example/gallery.jpg')
	})

	it('chooses the largest image when no hero is provided', () => {
		const images = [
			{ url: 'https://cdn.example/card.jpg', width: 600, height: 400, role: 'card' as const },
			{ url: 'https://cdn.example/gallery.jpg', width: 2400, height: 1600, role: 'gallery' as const },
		]
		expect(eventHeroImage(images, null, 'concerts')).toBe('https://cdn.example/gallery.jpg')
	})

	it('uses hero role to break ties and prefers known dimensions to unknown', () => {
		const images = [
			{ url: 'https://cdn.example/unknown-hero.jpg', width: null, height: null, role: 'hero' as const },
			{ url: 'https://cdn.example/known-gallery.jpg', width: 100, height: 100, role: 'gallery' as const },
			{ url: 'https://cdn.example/known-hero.jpg', width: 100, height: 100, role: 'hero' as const },
		]
		expect(eventHeroImage(images, null, 'concerts')).toBe('https://cdn.example/known-hero.jpg')
	})
})
