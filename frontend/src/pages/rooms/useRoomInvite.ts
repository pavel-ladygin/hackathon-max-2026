import { useEffect, useRef, useState } from 'react'
import type { RoomSnapshot } from '../../shared/api/types'
import { trackFailure } from '../../shared/analytics/client'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { sharePlanText } from './public-plan'

export type RoomInviteStatus = 'idle' | 'sharing' | 'copying' | 'opened' | 'cancelled' | 'share-error' | 'copied' | 'copy-error'

export function useInviteExpired(expiresAt: string | undefined, refetch: () => unknown) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!expiresAt) return
    const delay = Date.parse(expiresAt) - Date.now()
    if (delay > 2_147_483_647) return
    const timer = window.setTimeout(() => { setNow(Date.now()); void refetch() }, Math.max(0, delay))
    return () => window.clearTimeout(timer)
  }, [expiresAt, refetch])
  return Boolean(expiresAt && Date.parse(expiresAt) <= now)
}

export function useRoomInvite(room: RoomSnapshot | undefined, refetch: () => unknown) {
  const rawUrl = room?.invite?.url ?? ''
  const maxLink = room?.invite?.max_deep_link ?? rawUrl
  const expired = useInviteExpired(room?.invite?.expires_at, refetch)
  const inviteKey = `${room?.id ?? ''}:${rawUrl}`
  const [feedback, setFeedback] = useState<{ key: string; status: RoomInviteStatus }>({ key: '', status: 'idle' })
  const actionLock = useRef<{ key: string } | null>(null)
  const status = feedback.key === inviteKey ? feedback.status : 'idle'

  const canInvite = Boolean(room && room.invite
    && room.allowed_actions.includes('invite')
    && room.participants.length < 2
    && !expired
    && ['collecting_intents', 'ranking'].includes(room.state))
  const busy = status === 'sharing' || status === 'copying'
  const availableAtClick = (candidate: RoomSnapshot | undefined): candidate is RoomSnapshot => {
    if (!candidate?.invite) return false
    return candidate.allowed_actions.includes('invite')
      && candidate.participants.length < 2
      && Date.parse(candidate.invite.expires_at) > Date.now()
      && ['collecting_intents', 'ranking'].includes(candidate.state)
  }

  const share = async () => {
    if (!availableAtClick(room) || !maxLink || actionLock.current?.key === inviteKey) return
    const operation = { key: inviteKey }
    actionLock.current = operation
    setFeedback({ key: inviteKey, status: 'sharing' })
    try {
      const result = await maxPlatform.shareInvite({ text: sharePlanText(room), link: maxLink })
      if (actionLock.current === operation) setFeedback({ key: inviteKey, status: result.status === 'opened' ? 'opened' : result.status === 'cancelled' ? 'cancelled' : 'share-error' })
      // invite_shared requires an agreed metric definition; opening a sheet is not delivery.
      if (result.status === 'failed' || result.status === 'unavailable') trackFailure('invite_share_failed', new Error('Invite sharing failed'), { roomId: room.id })
    } catch (error) {
      if (actionLock.current === operation) setFeedback({ key: inviteKey, status: 'share-error' })
      trackFailure('invite_share_failed', error, { roomId: room.id })
    } finally {
      if (actionLock.current === operation) actionLock.current = null
    }
  }
  const copy = async () => {
    if (!availableAtClick(room) || !rawUrl || actionLock.current?.key === inviteKey) return
    const operation = { key: inviteKey }
    actionLock.current = operation
    setFeedback({ key: inviteKey, status: 'copying' })
    try {
      const copied = await maxPlatform.copyText(rawUrl)
      if (actionLock.current === operation) setFeedback({ key: inviteKey, status: copied ? 'copied' : 'copy-error' })
    } catch {
      if (actionLock.current === operation) setFeedback({ key: inviteKey, status: 'copy-error' })
    } finally {
      if (actionLock.current === operation) actionLock.current = null
    }
  }
  return { canInvite, expired, rawUrl, maxLink, status, busy, share, copy }
}
