import { decode } from 'entities'
import type { EventCard, EventCardDto, EventDetail, EventDetailDto, Preferences, PreferencesDto, PublicParticipant, PublicParticipantDto, RoomSnapshot, RoomSnapshotDto, User, UserDto } from './types'

export const mapUser = (x: UserDto): User => ({ id: x.id, displayName: x.display_name, avatarUrl: x.avatar_url, cityId: x.city_id, locale: x.locale })
export const mapPreferences = (x: PreferencesDto): Preferences => ({ cityId: x.city_id, interestSlugs: x.interest_slugs, budgetMaxMinor: x.budget_max_minor, usualDayTypes: x.usual_day_types, usualTimeSlots: x.usual_time_slots, version: x.version, updatedAt: x.updated_at })
const cleanText = (value: string | null): string | null => value === null ? null : decode(value)

export const mapEvent = (x: EventCardDto): EventCard => ({ id: x.id, title: decode(x.title), subtitle: cleanText(x.subtitle), category_slug: x.category_slug, starts_at: x.starts_at, timezone: x.timezone, date_label: x.date_label, venue_name: decode(x.venue_name), latitude: x.latitude ?? null, longitude: x.longitude ?? null, other_occurrences_count: x.other_occurrences_count, currency: x.currency, price_label: x.price_label, saved: x.saved, reasons: x.reasons.map((reason) => ({ ...reason, text: decode(reason.text) })), imageUrl: x.image_url, distanceM: x.distance_m, distanceLabel: x.distance_label, priceFromMinor: x.price_from_minor })
export const mapDetail = (x: EventDetailDto): EventDetail => ({ ...mapEvent(x), description: decode(x.description), endsAt: x.ends_at, ticketAvailable: x.ticket_available, dataProvenance: x.data_provenance, venue: { ...x.venue, name: decode(x.venue.name), address: decode(x.venue.address), metro: cleanText(x.venue.metro), district: cleanText(x.venue.district) }, images: x.images, status: x.status, age_rating: x.age_rating })
export const mapParticipant = (x: PublicParticipantDto): PublicParticipant => ({ id: x.id, displayName: x.display_name, avatarUrl: x.avatar_url, role: x.role, intentReady: x.intent_ready })
export const mapRoom = (x: RoomSnapshotDto): RoomSnapshot => ({ ...x, myIntent: x.my_intent, participants: x.participants.map(mapParticipant), match: x.match })
