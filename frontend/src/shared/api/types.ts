export type CategorySlug = 'concerts' | 'cinema' | 'theatre' | 'standup' | 'exhibitions' | 'sports' | 'food' | 'parties' | 'festivals' | 'walks' | 'other'
export type DayType = 'weekday' | 'weekend'
export type TimeSlot = 'morning' | 'day' | 'evening' | 'night'
export type VoteValue = 'like' | 'dislike'
export type RoomState = 'collecting_intents' | 'ranking' | 'voting' | 'matched' | 'exhausted'

export interface UserDto { id: string; display_name: string; avatar_url: string | null; city_id: string | null; locale: string }
export interface PreferencesDto { city_id: string; interest_slugs: CategorySlug[]; budget_max_minor: number; usual_day_types: DayType[]; usual_time_slots: TimeSlot[]; version: number; updated_at: string }
export interface RecommendationReasonDto { code: string; text: string }
export interface EventCardDto { id: string; title: string; subtitle: string | null; category_slug: CategorySlug; starts_at: string; timezone: string; date_label: string; venue_name: string; latitude?: number | null; longitude?: number | null; other_occurrences_count?: number; distance_m: number | null; distance_label: string | null; price_from_minor: number | null; currency: 'RUB'; price_label: string; image_url: string | null; saved: boolean; reasons: RecommendationReasonDto[] }
export interface VenueDto { id: string; name: string; address: string; latitude?: number | null; longitude?: number | null; metro: string | null; district: string | null }
export interface EventImageDto { url: string; width: number | null; height: number | null; role: 'card' | 'hero' | 'gallery' }
export interface EventDetailDto extends EventCardDto { description: string; ends_at: string | null; venue: VenueDto; images: EventImageDto[]; ticket_available: boolean; status: 'published' | 'sold_out' | 'cancelled'; age_rating: string | null; data_provenance: { source: string; source_updated_at: string | null; is_demo: boolean } }
export interface PublicParticipantDto { id: string; display_name: string; avatar_url: string | null; role: 'creator' | 'participant'; intent_ready: boolean }
export interface RoomInviteDto { url: string; max_deep_link: string; expires_at: string }
export interface InviteContextDto { token: string; room_name: string; inviter: PublicParticipantDto; expires_at: string; already_joined: boolean; status: 'joinable' | 'full' | 'expired' }
export interface MyIntentDto { dates: string[]; day_types: DayType[]; time_slots: TimeSlot[]; category_slugs: CategorySlug[]; budget_max_minor: number; radius_m: number | null; exclusion_slugs: string[]; location: { lat: number; lng: number } | null; free_text: string | null; version: number; round_no: number; submitted_at: string }
export interface PoolSummaryDto { version: number; round_no: number; state: 'ranking' | 'ready' | 'exhausted'; total: number; is_small?: boolean; voted_by_me: number; my_pool_finished: boolean; room_exhausted: boolean; retry_after_seconds: number | null; exhaustion_reasons: { code: string; text: string }[] }
export interface MatchSummaryDto { id: string; room_id: string; event_id: string; matched_at: string; participants: PublicParticipantDto[] }
export interface RoomSnapshotDto { id: string; name: string; city_id: string; state: RoomState; round_no: number; version: number; participants: PublicParticipantDto[]; my_intent: MyIntentDto | null; pool: PoolSummaryDto | null; match: MatchSummaryDto | null; invite: RoomInviteDto | null; allowed_actions: string[]; created_at: string; expires_at: string }
export interface RoomEventDto { cursor: string; position: number; event: EventCardDto }
export interface RoomEventsResponseDto { room_id: string; pool_version: number; round_no: number; items: RoomEventDto[]; next_cursor: string | null; total: number }
export interface MatchDto { id: string; matched_at: string; event: EventCardDto; participants: PublicParticipantDto[] }
export interface HomeFeedSectionDto { type: 'hero' | 'popular' | 'for_you' | 'nearby'; title: string; items: EventCardDto[] }
export interface HomeFeedResponseDto { feed_id: string; generated_at: string; sections: HomeFeedSectionDto[]; active_room: { id: string; name: string; city_id: string; state: RoomState } | null }
export interface EventSearchResponseDto { items: EventCardDto[]; applied_filters: Record<string, unknown>; total_estimate: number; next_cursor: string | null }
export interface SavedStateResponseDto { event_id: string; saved: boolean; saved_at: string | null }
export interface SavedEventDto { event: EventCardDto; saved_at: string | null; match: MatchSummaryDto | null }
export interface SavedEventsResponseDto { items: SavedEventDto[]; next_cursor: string | null }
export interface VoteResponseDto { accepted_vote: VoteValue; pool_version: number; my_pool_finished: boolean; room_exhausted: boolean; match: MatchDto | null }

export interface BootstrapRequestDto { init_data: string; start_param?: string | null }
export interface PreferencesRequestDto { city_id: string; interest_slugs: CategorySlug[]; budget_max_minor: number; usual_day_types: DayType[]; usual_time_slots: TimeSlot[] }
export interface RoomIntentRequestDto { dates: string[]; day_types: DayType[]; time_slots: TimeSlot[]; category_slugs: CategorySlug[]; budget_max_minor: number; radius_m: number | null; exclusion_slugs: string[]; location: { lat: number; lng: number } | null; free_text: string | null }
export interface ApiErrorBody { error: { code: string; message: string; request_id: string; details?: Record<string, unknown> } }

export interface User { id: string; displayName: string; avatarUrl: string | null; cityId: string | null; locale: string }
export interface Preferences { cityId: string; interestSlugs: CategorySlug[]; budgetMaxMinor: number; usualDayTypes: DayType[]; usualTimeSlots: TimeSlot[]; version: number; updatedAt: string }
export interface EventCard extends Omit<EventCardDto, 'subtitle' | 'image_url' | 'distance_m' | 'distance_label' | 'price_from_minor'> { subtitle: string | null; imageUrl: string | null; distanceM: number | null; distanceLabel: string | null; priceFromMinor: number | null }
export interface EventDetail extends Omit<EventDetailDto, keyof EventCardDto | 'ends_at' | 'ticket_available' | 'data_provenance'>, EventCard { endsAt: string | null; ticketAvailable: boolean; dataProvenance: EventDetailDto['data_provenance']; venue: VenueDto; images: EventImageDto[] }
export interface PublicParticipant { id: string; displayName: string; avatarUrl: string | null; role: PublicParticipantDto['role']; intentReady: boolean }
export interface RoomSnapshot extends Omit<RoomSnapshotDto, 'my_intent' | 'participants' | 'match'> { myIntent: MyIntentDto | null; participants: PublicParticipant[]; match: MatchSummaryDto | null }
