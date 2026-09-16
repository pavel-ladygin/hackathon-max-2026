import { ANNA, EVENTS, IVAN, now, type MockRoom } from './fixtures'
const KEY = 'max-together-mock-v1'
type State = { users: Record<string, any>; rooms: Record<string, MockRoom>; preferences: Record<string, any>; behavior: string[] }
const blank = (): State => ({ users: { ivan: IVAN, anna: ANNA }, rooms: {}, preferences: {}, behavior: [] })
const read = (): State => { try { const x = localStorage.getItem(KEY); return x ? JSON.parse(x) : blank() } catch { return blank() } }
let state = read()
if (new URLSearchParams(location.search).get('resetMock') === '1') { state = blank(); localStorage.setItem(KEY, JSON.stringify(state)) }
// A mock identity belongs to the browser tab, not to an individual route.
// Keep it in sessionStorage so client-side navigation and Vite HMR cannot turn
// Anna back into the default Ivan after the debug query has been consumed.
const MOCK_USER_KEY = 'max-together-mock-user'
const queryMockUser = new URLSearchParams(location.search).get('mockUser')
const initialMockUser = queryMockUser === 'anna' || queryMockUser === 'ivan'
  ? queryMockUser
  : sessionStorage.getItem(MOCK_USER_KEY) === 'anna' ? 'anna' : 'ivan'
sessionStorage.setItem(MOCK_USER_KEY, initialMockUser)
export const mockUser = () => {
  const fromQuery = new URLSearchParams(location.search).get('mockUser')
  if (fromQuery === 'anna' || fromQuery === 'ivan') {
    sessionStorage.setItem(MOCK_USER_KEY, fromQuery)
    return fromQuery
  }
  return sessionStorage.getItem(MOCK_USER_KEY) === 'anna' ? 'anna' : 'ivan'
}
const save = () => { localStorage.setItem(KEY, JSON.stringify(state)); try { new BroadcastChannel('max-together-mock').postMessage({ type: 'state-changed' }) } catch { /* BroadcastChannel is optional in older webviews. */ } }
// localStorage is shared by the demo tabs, while module memory is not. Always
// rebase a request on the latest persisted snapshot before mutating it.
export const getState = () => { state = read(); return state }
export const saveState = save
export const installMockSync = (onChange?: () => void) => { try { const channel = new BroadcastChannel('max-together-mock'); channel.onmessage = (event) => { if (event.data?.type === 'state-changed') { state = read(); onChange?.() } }; return () => channel.close() } catch { return () => {} } }
export const getUser = (id = mockUser()) => getState().users[id]
export const createRoom = (name: string, cityId: string, creator = mockUser()): MockRoom => { const latest = getState(); const existing = Object.values(latest.rooms).find(r => r.creator === creator && r.state !== 'exhausted'); if (existing) return existing; const id = crypto.randomUUID(); const room: MockRoom = { id, name, city_id: cityId, token: `invite-${id}`, creator, participant: null, intents: {}, votes: {}, state: 'collecting_intents', version: 1, round_no: 1, created_at: now(), expires_at: new Date(Date.now() + 172800000).toISOString(), match: null }; latest.rooms[id] = room; save(); return room }
export const roomByToken = (token: string) => Object.values(getState().rooms).find(r => r.token === token)
export const reconcile = (room: MockRoom) => { const ids = [room.creator, room.participant].filter(Boolean) as string[]; if (ids.length < 2 || ids.some(id => !room.intents[id])) { save(); return } room.state = 'voting'; const likes = ids.map(id => room.votes[id] ?? {}); const shared = EVENTS.find(e => likes.every(v => v[e.id] === 'like')); if (shared) { room.state = 'matched'; room.match = { id: crypto.randomUUID(), matched_at: now(), event: shared, participants: ids.map((id, i) => ({ id: state.users[id].id, display_name: state.users[id].display_name, avatar_url: null, role: i === 0 ? 'creator' : 'participant', intent_ready: true })) } } else if (ids.every(id => Object.keys(room.votes[id] ?? {}).length >= EVENTS.length)) room.state = 'exhausted'; room.version++; save() }
export const publicRoom = (room: MockRoom, user = mockUser()) => { const participants = [room.creator, room.participant].filter(Boolean).map(id => ({ id: state.users[id as string].id, display_name: state.users[id as string].display_name, avatar_url: null, role: id === room.creator ? 'creator' : 'participant', intent_ready: Boolean(room.intents[id as string]) })); const mine = room.intents[user] ?? null; const voteCount = Object.keys(room.votes[user] ?? {}).length; return { id: room.id, name: room.name, city_id: room.city_id, state: room.state, round_no: room.round_no, version: room.version, participants, my_intent: mine, pool: room.state === 'collecting_intents' ? null : { version: room.version, round_no: room.round_no, state: room.state === 'exhausted' ? 'exhausted' : 'ready', total: EVENTS.length, voted_by_me: voteCount, my_pool_finished: voteCount >= EVENTS.length, room_exhausted: room.state === 'exhausted', retry_after_seconds: null, exhaustion_reasons: [] }, match: room.match ? { id: room.match.id, room_id: room.id, event_id: room.match.event.id, matched_at: room.match.matched_at, participants: room.match.participants } : null, invite: room.creator === user ? { url: `${location.origin}/join/${room.token}`, max_deep_link: `https://max.ru/app?startapp=${room.token}`, expires_at: room.expires_at } : null, allowed_actions: room.state === 'collecting_intents' ? ['invite', 'join', 'edit_intent', 'wait'] : room.state === 'voting' ? ['vote', 'wait'] : room.state === 'matched' ? ['view_match'] : ['restart_with_new_intent'], created_at: room.created_at, expires_at: room.expires_at }
}
