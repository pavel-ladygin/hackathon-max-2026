import { beforeEach, describe, expect, it, vi } from 'vitest'

const intent = {
  dates: ['2026-09-19'],
  day_types: [],
  time_slots: ['evening'],
  category_slugs: ['concerts'],
  budget_max_minor: 300_000,
  radius_m: 5_000,
  exclusion_slugs: [],
  location: null,
  free_text: null,
  version: 1,
  round_no: 1,
  submitted_at: '2026-09-16T12:00:00.000Z',
}

describe('two-tab mock state', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    history.replaceState({}, '', '/?resetMock=1&mockUser=ivan')
    vi.resetModules()
  })

  it('keeps private identities and reaches a match without losing shared state', async () => {
    const state = await import('./state')
    const { EVENTS } = await import('./fixtures')
    const room = state.createRoom('Суббота', 'moscow')

    history.replaceState({}, '', `/?mockUser=anna`)
    const joined = state.roomByToken(room.token)!
    joined.participant = 'anna'
    state.saveState()

    history.replaceState({}, '', `/?mockUser=ivan`)
    const forIvan = state.getState().rooms[room.id]
    expect(forIvan.participant).toBe('anna')
    forIvan.intents.ivan = intent
    state.reconcile(forIvan)
    expect(forIvan.state).toBe('collecting_intents')

    history.replaceState({}, '', `/?mockUser=anna`)
    const forAnna = state.getState().rooms[room.id]
    expect(Object.keys(forAnna.intents)).toEqual(['ivan'])
    forAnna.intents.anna = intent
    state.reconcile(forAnna)
    expect(forAnna.state).toBe('voting')
    expect(state.publicRoom(forAnna).my_intent).toEqual(intent)

    history.replaceState({}, '', `/?mockUser=ivan`)
    const ivanVote = state.getState().rooms[room.id]
    ivanVote.votes.ivan = { [EVENTS[0].id]: 'like' }
    state.reconcile(ivanVote)
    expect(ivanVote.state).toBe('voting')

    history.replaceState({}, '', `/?mockUser=anna`)
    const annaVote = state.getState().rooms[room.id]
    annaVote.votes.anna = { [EVENTS[0].id]: 'like' }
    state.reconcile(annaVote)

    expect(annaVote.state).toBe('matched')
    expect(annaVote.match?.event.id).toBe(EVENTS[0].id)
    expect(annaVote.match?.participants).toHaveLength(2)
  })
})
