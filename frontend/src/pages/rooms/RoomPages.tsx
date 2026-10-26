import { timeZoneLabel } from '../../shared/lib/timeZoneLabel'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AnimatePresence, motion, useMotionValue, useReducedMotion, useTransform } from 'motion/react'
import { createContext, lazy, Suspense, useContext, useEffect, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useNavigate, useParams } from 'react-router-dom'
import { z } from 'zod'
import { useEventDetail } from '../../features/discovery/queries'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { useRoom, useRoomEvents } from '../../features/rooms/queries'
import { RoomEventDate } from '../../features/rooms/RoomEventDate'
import { availableRelaxations, combinedRelaxedIntent, type Relaxation, type RelaxationChanges } from '../../features/rooms/relaxation'
import { isRoomError, roomErrorMessage } from '../../features/rooms/errors'
import { participantInitials, resolveSwipeIntent } from '../../features/rooms/animation'
import { withMinimumDuration } from '../../shared/lib/async'
import { eventCategoryLabel, eventImage, eventImageFallback } from '../../shared/lib/events'
import { apiClient } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { CategorySlug, RoomIntentRequestDto, RoomSnapshot, VoteValue } from '../../shared/api/types'
import { CATEGORY_OPTIONS, CATEGORY_SLUGS } from '../../shared/api/categories'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, Chip, ChipGroup, Empty, EventImage, PageContent, PageShell, PrivacyNote, ScreenSkeleton, TopBar } from '../../shared/ui/index'
import { ChoiceChips } from '../../shared/ui/ChoiceChips'
import { MoneyInput } from '../../shared/ui/MoneyInput'
import { decimalToMinor } from '../../shared/ui/money'
import { RoomDateChoices } from '../../shared/ui/RoomDateChoices'
import styles from '../pages.module.css'
import intentStyles from './intent.module.css'
import reliabilityStyles from './reliability.module.css'
import recoveryStyles from './recovery.module.css'
import { track, trackFailure } from '../../shared/analytics/client'
import { CreateRoomForm } from './CreateRoomForm'
import { PlanSummary } from './PlanSummary'
import { cityToday, isCanonicalDate, roomCity } from './public-plan'
import { RoomInviteActions } from './RoomInviteActions'
import { useInviteExpired, useRoomInvite } from './useRoomInvite'
import { useRoomNavigationGuard } from '../../features/rooms/useRoomNavigationGuard'

const MatchCelebration = lazy(() => import('../../features/rooms/MatchCelebration').then((module) => ({ default: module.MatchCelebration })))
type RoomFlowContextValue = { live: RoomSnapshot | null; remoteChanged: boolean; adopt: (snapshot?: RoomSnapshot) => boolean }
const RoomFlowContext = createContext<RoomFlowContextValue>({ live: null, remoteChanged: false, adopt: () => false })
const useRoomFlow = () => useContext(RoomFlowContext)
export function NewRoomPage() {
  const navigate = useNavigate()
  const { captureNavigation } = useRoomNavigationGuard()
  const reducedMotion = useReducedMotion()
  const bootstrap = useBootstrap()
  const queryClient = useQueryClient()
  const [createSuccess, setCreateSuccess] = useState(false)
  const createAttempt = useRef<{ body: string; key: ReturnType<typeof crypto.randomUUID> } | null>(null)
  const draftKey = `room-create-v1:${bootstrap.data?.user.id ?? 'pending'}`
  useEffect(() => { track('room_creation_started') }, [])
  const create = useMutation({
    onMutate: captureNavigation,
    mutationFn: (values: Parameters<typeof apiClient.createRoom>[0]) => {
      const body = JSON.stringify(values)
      if (createAttempt.current?.body !== body) createAttempt.current = { body, key: crypto.randomUUID() }
      return apiClient.createRoom(values, createAttempt.current.key)
    },
    onSuccess: async ({ room }, _values, canNavigate) => {
      queryClient.setQueryData(['room', room.id], room)
      await queryClient.invalidateQueries({ queryKey: ['home-feed'] })
      setCreateSuccess(true)
      try { window.sessionStorage.removeItem(draftKey) } catch { /* Storage may be unavailable in a WebView. */ }
      await new Promise<void>((resolve) => window.setTimeout(resolve, reducedMotion ? 0 : 350))
      if (!canNavigate?.()) return
      navigate(`/rooms/${room.id}/invite`)
    },
    onError: (error) => trackFailure('room_creation_failed', error),
  })
  if (bootstrap.isPending) return <ScreenSkeleton variant="room" label="Загружаем профиль…" />
  return (
    <PageShell><TopBar title="Новая комната" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ВМЕСТЕ ЛЕГЧЕ ВЫБРАТЬ</p><h1 className={styles.title}>Создать комнату</h1><p className={styles.subtitle}>Выберите город и предложите план встречи. Каждый отдельно подтвердит свои пожелания</p>
      {create.isError ? <><AuthRecoveryAction error={create.error} /><Button tone="ghost" onClick={() => navigate('/')}>На главную</Button></> : null}
      <CreateRoomForm key={draftKey} profileCityId={bootstrap.data?.preferences?.cityId ?? bootstrap.data?.user.cityId} draftKey={draftKey} pending={create.isPending} success={createSuccess} error={create.error ?? undefined} onSubmit={(values) => { if (!create.isPending && !createSuccess) create.mutate(values) }} />
    </PageContent></PageShell>
  )
}

export function InvitePage() {
  const { roomId } = useParams()
  return <RoomInvite key={roomId} roomId={roomId} />
}

function RoomInvite({ roomId }: { roomId: string | undefined }) {
  const navigate = useNavigate()
  const room = useRoom(roomId)
  const invite = useRoomInvite(room.data, room.refetch)
  if (room.isPending) return <ScreenSkeleton variant="room" label="Готовим приглашение…" />
  if (room.isError || !room.data) return <RoomReadError title="Комната недоступна" error={room.error} retry={() => void room.refetch()} pending={room.isFetching} />
  const creator = room.data.participants.find((participant) => participant.role === 'creator')
  const creatorInitial = Array.from(creator?.displayName.trim() ?? '')[0]?.toUpperCase() || '?'
  if (!invite.canInvite) return <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
    <h1 className={styles.title}>Приглашение больше недоступно</h1><p className={styles.subtitle}>{invite.expired ? 'Срок приглашения истёк.' : room.data.participants.length >= 2 ? 'Друг уже присоединился. Продолжите совместный выбор в комнате.' : 'Вступление в эту комнату завершено.'}</p>
    <div className={styles.footer}><Button onClick={() => navigate(`/rooms/${room.data.id}/${expectedScreen(room.data)}`)}>Перейти в комнату</Button><Button tone="secondary" onClick={() => navigate('/')}>На главную</Button></div>
  </PageContent></PageShell>
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>{creatorInitial}</span><span className={styles.avatar}>{room.data.participants.length > 1 ? 'А' : '+'}</span></div>
      <h1 className={styles.title}>Пригласите друга</h1><p className={styles.subtitle}>Отправьте приглашение в MAX. После подключения каждый отдельно заполнит пожелания.</p>
      <PlanSummary plan={room.data.public_plan} cityId={room.data.city_id} />
      <div className={styles.inviteLink} data-testid="invite-url">{invite.rawUrl || 'Ссылка появится после создания комнаты'}</div>
      <PrivacyNote>Предпочтения и оценки останутся скрытыми до взаимного лайка.</PrivacyNote>
      <RoomInviteActions {...invite} className={styles.footer}><Button tone="ghost" onClick={() => navigate(`/rooms/${roomId}/intent`)}>Перейти к моим пожеланиям</Button></RoomInviteActions>
    </PageContent></PageShell>
  )
}

export function JoinPage() {
  const { inviteToken } = useParams()
  const navigate = useNavigate()
  const { captureNavigation, canNavigate: canRestoreRoom } = useRoomNavigationGuard()
  const bootstrap = useBootstrap()
  const inviteContext = bootstrap.data?.inviteContext
  const context = inviteContext?.token === inviteToken ? inviteContext : null
  const inviteStatus = context?.status
  useEffect(() => {
    if (!inviteStatus || !context?.room_id) return
    const key = `invite-recipient-opened:${context.room_id}`
    try {
      if (window.sessionStorage.getItem(key)) return
      window.sessionStorage.setItem(key, '1')
    } catch { /* Best effort deduplication when storage is unavailable. */ }
    track('invite_link_opened', { roomId: context.room_id })
  }, [context?.room_id, inviteStatus])
  const expired = useInviteExpired(context?.expires_at, bootstrap.refetch)
  const joinBlocked = !context || (context.status !== 'joinable' || expired) && !context.already_joined
  const queryClient = useQueryClient()
  const join = useMutation({ onMutate: captureNavigation, mutationFn: () => {
    if (!inviteToken) throw new Error('Invite token is missing')
    return apiClient.joinRoom(inviteToken)
  }, onSuccess: async (room, _variables, canNavigate) => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['room', room.id] }),
      queryClient.invalidateQueries({ queryKey: ['room-events', room.id] }),
      queryClient.invalidateQueries({ queryKey: ['home-feed'] }),
    ])
    if (!canNavigate?.()) return
    navigate(`/rooms/${room.id}/intent`, { replace: true })
  }, onError: async (error) => {
    trackFailure('room_join_failed', error, { roomId: context?.room_id })
    if (error instanceof ApiError && (error.status === 409 || error.code === 'INVITE_EXPIRED')) await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
  } })
  // An invite is also a return link for existing members, including when its
  // capacity or expiry no longer permits a new participant to join.
  if (context?.already_joined) return <RoomRedirect to={`/rooms/${encodeURIComponent(context.room_id)}/waiting`} canNavigate={canRestoreRoom} />
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>{context?.inviter.display_name.slice(0, 1) ?? 'И'}</span><span className={styles.avatar}>+</span></div>
      <p className={styles.eyebrow}>СОВМЕСТНЫЙ ВЫБОР</p><h1 className={styles.title}>{context ? `Вас приглашает ${context.inviter.display_name}` : 'Не удалось подтвердить приглашение'}</h1><p className={styles.subtitle}>{context ? `Комната «${context.room_name}». После подключения каждый самостоятельно укажет свои условия.` : 'Вернитесь на главную и откройте актуальную ссылку-приглашение.'}</p>
      {context ? <PlanSummary plan={context.public_plan} cityId={context.city_id} /> : null}
      <PrivacyNote />{join.isError ? <AuthRecoveryAction error={join.error} /> : null}{join.isError ? <p className={styles.error} role="alert">{roomErrorMessage(join.error, 'Ссылка недействительна или комната уже заполнена.')}</p> : null}
      {context?.status === 'expired' || expired ? <p className={styles.error} role="alert">Срок приглашения истёк.</p> : context?.status === 'full' ? <p className={styles.error} role="alert">Комната уже заполнена.</p> : null}
      <div className={styles.footer}><Button disabled={join.isPending || joinBlocked} state={join.isPending ? 'loading' : 'idle'} loadingLabel="Подключаем…" onClick={() => { if (!join.isPending && !joinBlocked) { track('room_join_started', { roomId: context?.room_id }); join.mutate() } }}>Присоединиться</Button><Button tone="ghost" onClick={() => navigate('/')}>На главную</Button>{!maxPlatform.isMax && inviteToken ? <Button tone="secondary" onClick={() => void maxPlatform.openMaxLink(maxAppUrl(inviteToken))}>Открыть в MAX</Button> : null}</div>
    </PageContent></PageShell>
  )
}

export function RoomFlowPage() {
  const { roomId } = useParams()
  return <RoomFlow key={roomId} />
}

function RoomFlow() {
  const { roomId, roomScreen } = useParams()
  const queryClient = useQueryClient()
  const { canNavigate } = useRoomNavigationGuard()
  const room = useRoom(roomId)
  const [acknowledged, setAcknowledged] = useState<RoomSnapshot | null>(null)
  const [remote, setRemote] = useState<RoomSnapshot | null>(null)
  const loadedRoomId = room.data?.id
  useEffect(() => { if (loadedRoomId) track('room_opened', { roomId: loadedRoomId }) }, [loadedRoomId])
  // Adjust the acknowledged view during render so a remote phase cannot render
  // an intermediate redirect or unmount a dirty form before being fenced.
  const incoming = room.data
  if (incoming && incoming.version >= Math.max(acknowledged?.version ?? 0, remote?.version ?? 0)) {
    if (!acknowledged || roomPhaseKey(acknowledged) === roomPhaseKey(incoming)) {
      if (JSON.stringify(acknowledged) !== JSON.stringify(incoming) || remote) { setAcknowledged(incoming); setRemote(null) }
    } else if (JSON.stringify(remote) !== JSON.stringify(incoming)) setRemote(incoming)
  }
  if (room.isPending) return <ScreenSkeleton variant="room" label="Восстанавливаем комнату…" />
  if (!room.data) return <RoomReadError title="Комната недоступна" error={room.error} retry={() => void room.refetch()} pending={room.isFetching} />
  const live = remote && remote.version > room.data.version ? remote : acknowledged && acknowledged.version > room.data.version ? acknowledged : room.data
  const shown = acknowledged ?? live
  const livePhase = roomPhaseKey(live)
  const shownPhase = roomPhaseKey(shown)
  const adopt = (snapshot?: RoomSnapshot) => {
    const cached = queryClient.getQueryData<RoomSnapshot>(['room', roomId])
    const latest = [shown, live, cached].filter((value): value is RoomSnapshot => Boolean(value)).reduce((best, value) => value.version >= best.version ? value : best)
    const target = snapshot ?? latest
    if (target.id !== roomId || target.version < latest.version) return false
    setAcknowledged(target); setRemote(null)
    return true
  }
  const expected = expectedScreen(shown)
  let content
  if (shown.state === 'closed') content = <ClosedScreen room={shown} />
  else if (roomScreen !== expected) content = <RoomRedirect to={`/rooms/${roomId}/${expected}`} canNavigate={canNavigate} />
  else if (expected === 'intent') content = <IntentScreen key={`${shown.id}:${shown.round_no}`} room={shown} />
  else if (expected === 'waiting') content = <WaitingScreen room={shown} refetch={room.refetch} />
  else if (expected === 'vote') content = <VoteScreen key={`${shown.id}:${shown.round_no}:${shown.pool?.version ?? ''}`} room={shown} />
  else if (expected === 'match') content = <MatchScreen room={shown} />
  else content = <RecoveryScreen key={`${shown.id}:${shown.round_no}`} room={shown} />
  return <RoomFlowContext.Provider value={{ live: remote, remoteChanged: Boolean(remote && shownPhase !== livePhase), adopt }}>
    {room.isError ? <RoomReadError title="Не удалось обновить комнату" error={room.error} retry={() => void room.refetch()} pending={room.isFetching} /> : null}
    <fieldset disabled={room.isError} style={{ display: room.isError ? 'none' : 'contents', border: 0, padding: 0, margin: 0 }}>{content}</fieldset>
  </RoomFlowContext.Provider>
}

function RoomRedirect({ to, canNavigate }: { to: string; canNavigate: () => boolean }) {
  const navigate = useNavigate()
  useEffect(() => { if (canNavigate()) navigate(to, { replace: true }) }, [canNavigate, navigate, to])
  return null
}

const intentSchema = (timezone?: string) => z.object({
  dates: z.array(z.string()).min(1, 'Выберите хотя бы одну дату').max(14, 'Не больше 14 дат').refine((dates) => new Set(dates).size === dates.length && dates.every((date) => isCanonicalDate(date) && (!timezone || date >= cityToday(timezone))), 'Выберите актуальные даты без повторов; прошедшие даты нужно изменить самостоятельно'),
  time_slots: z.array(z.enum(['morning', 'day', 'evening', 'night'])),
  category_slugs: z.array(z.enum(CATEGORY_SLUGS)).min(1, 'Выберите хотя бы одну категорию'),
  budget: z.number({ invalid_type_error: 'Введите бюджет' }).min(0).max(1_000_000).multipleOf(0.01, 'Укажите сумму с точностью до копейки'),
  free_text: z.string().max(300),
})
type IntentForm = z.infer<ReturnType<typeof intentSchema>>

function IntentScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const { captureNavigation } = useRoomNavigationGuard()
  const flow = useRoomFlow()
  const queryClient = useQueryClient()
  const submitIntent = useIntentSubmission()
  const city = roomCity(room.city_id)
  const timezone = city?.timezone
  const bootstrap = useBootstrap()
  const confirmingPublicPlan = Boolean(room.public_plan && !room.myIntent)
  const errorSummary = useRef<HTMLParagraphElement>(null)
  const draftKey = `room-intent-v1:${bootstrap.data?.user.id ?? 'pending'}:${room.id}:${room.round_no}`
  const [formDefaults] = useState(() => readIntentDraft(draftKey, intentDefaults(room, timezone ? cityToday(timezone) : localDate(new Date()))))
  const form = useForm<IntentForm>({ resolver: zodResolver(intentSchema(timezone)), defaultValues: formDefaults })
  const [budgetText, setBudgetText] = useState(String(formDefaults.budget))
  const [remoteEditing, setRemoteEditing] = useState(false)
  useEffect(() => {
    const unsubscribe = form.subscribe({ formState: { values: true }, callback: ({ values }) => {
      try { window.sessionStorage.setItem(draftKey, JSON.stringify({ dates: values.dates, time_slots: values.time_slots, category_slugs: values.category_slugs, budget: values.budget, free_text: values.free_text })) } catch { /* Storage may be unavailable in a WebView. */ }
    } })
    return unsubscribe
  }, [draftKey, form])
  const dates = useWatch({ control: form.control, name: 'dates' })
  const timeSlots = useWatch({ control: form.control, name: 'time_slots' })
  const categories = useWatch({ control: form.control, name: 'category_slugs' })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const sourceRoom = flow.live ?? room
  const remotePreparation = canConfirmPreparation(sourceRoom)
  const save = useMutation({
    onMutate: captureNavigation,
    mutationFn: (values: IntentForm) => { if (flow.remoteChanged && !remoteEditing) throw new Error('Confirm the updated room first'); const source = remoteEditing ? flow.live ?? room : room; return submitIntent(source, toIntent(values)) },
    onSuccess: async (data, _values, canNavigate) => { await Promise.all([queryClient.invalidateQueries({ queryKey: ['room', room.id] }), queryClient.invalidateQueries({ queryKey: ['home-feed'] })]); if (!canNavigate?.() || !flow.adopt(data)) return; try { window.sessionStorage.removeItem(draftKey) } catch { /* Storage may be unavailable. */ } navigate(`/rooms/${room.id}/waiting`, { replace: true }) },
    onError: async (error) => {
      if (error instanceof ApiError && error.status === 409) await queryClient.invalidateQueries({ queryKey: ['room', room.id] })
      if (!(error instanceof ApiError)) return
      const mapping: Record<string, keyof IntentForm> = { dates: 'dates', time_slots: 'time_slots', category_slugs: 'category_slugs', budget_max_minor: 'budget', free_text: 'free_text' }
      for (const [field, message] of Object.entries(error.fieldErrors)) if (mapping[field]) form.setError(mapping[field], { message })
    },
  })
  const confirmRemote = useMutation({ onMutate: captureNavigation, mutationFn: () => {
    const source = flow.live ?? room
    const prepared = source.next_round?.my_intent ?? source.myIntent
    if (!prepared) throw new Error('No saved own intent available for next-round confirmation')
    return submitIntent(source, fromSavedIntent(prepared))
  }, onSuccess: async (data, _values, canNavigate) => { await Promise.all([queryClient.invalidateQueries({ queryKey: ['room', room.id] }), queryClient.invalidateQueries({ queryKey: ['home-feed'] })]); if (canNavigate?.()) flow.adopt(data) }, onError: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }) } })
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${intentStyles.intentPage}`}>
      <p className={`${styles.eyebrow} ${intentStyles.introEyebrow}`}>ВАШИ УСЛОВИЯ ДЛЯ ЭТОЙ ВСТРЕЧИ</p><h1 className={`${styles.title} ${intentStyles.introTitle}`}>{confirmingPublicPlan ? 'Подтвердите личные пожелания' : 'Мои предпочтения'}</h1><p className={`${styles.subtitle} ${intentStyles.introSubtitle}`}>{confirmingPublicPlan ? 'Мы перенесли условия из публичного плана встречи. Проверьте их и при необходимости измените для себя' : 'Укажите, что подходит именно сейчас. Другой участник не увидит ответы.'}</p>
      {confirmingPublicPlan ? <p className={styles.subtitle}>Каждый участник подтверждает свои пожелания отдельно</p> : null}
      <p className={styles.subtitle}>{city ? `${city.name} · ${timeZoneLabel(city.timezone)}` : 'Время зависит от выбранного города.'}</p><PrivacyNote />
      <form className={`${styles.formStack} ${intentStyles.intentForm}`} onSubmit={(event) => { void form.handleSubmit((values) => { if (!save.isPending) save.mutate(values) }, () => window.requestAnimationFrame(() => errorSummary.current?.focus()))(event) }}>
        {Object.keys(form.formState.errors).length ? <p ref={errorSummary} tabIndex={-1} className={`${styles.error} ${intentStyles.formErrors}`} role="alert">Проверьте выбранные даты, время, бюджет и интересы.</p> : null}
        <fieldset className={intentStyles.choiceField}><legend>Когда удобно</legend><RoomDateChoices id="intent-date-picker" selected={dates} onChange={(nextDates) => form.setValue('dates', nextDates, { shouldValidate: true, shouldDirty: true })} min={timezone ? cityToday(timezone) : undefined} error={form.formState.errors.dates?.message} disabled={save.isPending} /></fieldset>
        <ChoiceField title="Время" error={form.formState.errors.time_slots?.message} options={[['morning', 'Утро'], ['day', 'День'], ['evening', 'Вечер'], ['night', 'Ночь']]} selected={timeSlots} onToggle={(value) => form.setValue('time_slots', toggleValue(timeSlots, value as IntentForm['time_slots'][number]), { shouldValidate: true })} />
        <ChoiceField title="Что интересно" error={form.formState.errors.category_slugs?.message} options={CATEGORY_OPTIONS.map(({ slug, label }) => [slug, label] as [CategorySlug, string])} selected={categories} onToggle={(value) => form.setValue('category_slugs', toggleValue(categories, value as CategorySlug), { shouldValidate: true })} />
        <div className={intentStyles.rangeField}><MoneyInput id="room-budget" label="Бюджет, ₽" suffix="₽" value={budgetText} required invalid={Boolean(form.formState.errors.budget)} describedBy="room-budget-help" onChange={(value) => { setBudgetText(value); const minor = decimalToMinor(value); form.setValue('budget', minor === null ? Number.NaN : minor / 100, { shouldValidate: true, shouldDirty: true }) }} /><span id="room-budget-help" className={styles.subtitle}>До {Number(budget || 0).toLocaleString('ru', { maximumFractionDigits: 2 })} ₽ для вас</span>{form.formState.errors.budget ? <span className={intentStyles.fieldError} role="alert">{form.formState.errors.budget.message}</span> : null}</div>
        <label className={intentStyles.textField} htmlFor="room-free-text">Дополнительное пожелание<textarea id="room-free-text" className={styles.textarea} placeholder="Например: хочется спокойного места" aria-invalid={Boolean(form.formState.errors.free_text)} maxLength={300} {...form.register('free_text')} />{form.formState.errors.free_text ? <span className={intentStyles.fieldError} role="alert">{form.formState.errors.free_text.message}</span> : null}</label>
        {save.isError ? <AuthRecoveryAction error={save.error} /> : null}{save.isError ? <p className={`${styles.error} ${intentStyles.formErrors}`} role="alert">{roomErrorMessage(save.error, 'Не удалось сохранить предпочтения.')}</p> : null}
        {confirmRemote.isError ? <><AuthRecoveryAction error={confirmRemote.error} /><p className={styles.error} role="alert">{roomErrorMessage(confirmRemote.error, 'Не удалось подтвердить условия. Повторите попытку или измените их.')}</p></> : null}
        <div className={`${styles.footer} ${intentStyles.intentFooter}`}>{flow.remoteChanged && !remotePreparation ? <RemoteAdoptCard title={remoteChangeTitle(sourceRoom)} onContinue={() => flow.adopt()} /> : remotePreparation && !remoteEditing ? <RemoteChangeCard pending={confirmRemote.isPending} onKeep={() => confirmRemote.mutate()} onEdit={() => setRemoteEditing(true)} /> : <Button type="submit" disabled={save.isPending}>{save.isPending ? 'Сохраняем приватно…' : confirmingPublicPlan ? 'Подтвердить пожелания' : 'Сохранить предпочтения'}</Button>}</div>
      </form>
    </PageContent></PageShell>
  )
}

function WaitingScreen({ room, refetch = noRoomRefetch }: { room: RoomSnapshot; refetch?: () => unknown }) {
  const navigate = useNavigate()
  const flow = useRoomFlow()
  const reducedMotion = useReducedMotion()
  const invite = useRoomInvite(room, refetch)
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>Раунд {room.round_no} из 3</p><h1 className={styles.title}>{room.state === 'ranking' ? 'Формируем общий пул…' : 'Ждём второго участника'}</h1><p className={styles.subtitle}>{room.state === 'voting' ? 'Вы просмотрели все события. Ждём, когда второй участник завершит выбор.' : 'Ваши пожелания сохранены. Подборка появится, когда оба участника будут готовы.'}</p>
      <div className={`${styles.statusList} ${styles.waitingParticipants}`} aria-live="polite"><AnimatePresence initial={false}>{room.participants.map((participant) => <motion.div className={styles.statusRow} key={participant.id} layout={!reducedMotion} initial={reducedMotion ? false : { opacity: 0, y: 8, scale: .92 }} animate={{ opacity: 1, y: 0, scale: 1 }} exit={reducedMotion ? undefined : { opacity: 0, y: -8, scale: .92 }} transition={{ duration: reducedMotion ? 0 : .24 }}><span className={styles.smallAvatar}>{participantInitials(participant.displayName)}</span><span><strong>{participant.displayName}</strong><small>{room.state === 'voting' ? 'Участник совместного выбора' : participant.intentReady ? '✓ Предпочтения заполнены' : 'Заполняет предпочтения…'}</small></span></motion.div>)}</AnimatePresence><div className={styles.participantCount}><svg className={styles.participantCountIcon} viewBox="0 0 24 24" aria-hidden="true" focusable="false"><circle cx="9" cy="8" r="3" /><path d="M3.5 19v-2a5.5 5.5 0 0 1 11 0v2M16 5.3a3 3 0 0 1 0 5.4M20.5 19v-2a5.5 5.5 0 0 0-3.2-5" /></svg><span>{room.participants.length} / 2 участника</span></div></div>
      {invite.canInvite ? <section className={styles.waitingInvite} aria-label="Приглашение в комнату"><p className={styles.eyebrow}>ПРИГЛАШЕНИЕ В КОМНАТУ</p><div className={styles.inviteLink} data-testid="invite-url">{invite.rawUrl}</div><RoomInviteActions {...invite} className={styles.waitingInviteActions} showShareOpenedStatus={false} /></section> : null}
      {room.state !== 'voting' ? <div className={styles.explain}><strong>Как формируется подборка</strong><span>✓ Учитываем постоянные интересы</span><span>✓ Добавляем текущие условия</span><span>{room.participants.length === 2 ? '○ Ждём готовности обоих' : '○ Ждём присоединения друга'}</span></div> : null}
      {flow.remoteChanged ? <RemoteAdoptCard title={flow.live?.state === 'matched' || flow.live?.state === 'closed' ? 'Подборка завершена' : 'Подборка обновилась'} onContinue={() => flow.adopt()} /> : null}
    </PageContent></PageShell>
  )
}

function VoteScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const { captureNavigation } = useRoomNavigationGuard()
  const flow = useRoomFlow()
  const queryClient = useQueryClient()
  const events = useRoomEvents(room.id, true, room.round_no, room.pool?.version)
  const swipeSessionTracked = useRef(false)
  const [matchPreview, setMatchPreview] = useState(false)
  const dragX = useMotionValue(0)
  const rotation = useTransform(dragX, [-260, 0, 260], [-10, 0, 10])
  const likeOpacity = useTransform(dragX, [0, 90], [0, 1])
  const dislikeOpacity = useTransform(dragX, [-90, 0], [1, 0])
  const reducedMotion = useReducedMotion()
  const vote = useMutation({
    onMutate: captureNavigation,
    mutationFn: ({ eventId, value }: { eventId: string; value: VoteValue }) => apiClient.vote(room.id, eventId, { pool_version: room.pool?.version ?? room.version, vote: value }),
    onSuccess: async (result, _variables, canNavigate) => {
      if (result.match) {
        setMatchPreview(true)
        await new Promise<void>((resolve) => window.setTimeout(resolve, reducedMotion ? 0 : 650))
        setMatchPreview(false)
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['room', room.id] }),
        queryClient.invalidateQueries({ queryKey: ['room-events', room.id] }),
        queryClient.invalidateQueries({ queryKey: ['home-feed'] }),
      ])
      const current = queryClient.getQueryData<RoomSnapshot>(['room', room.id])
      // A late vote response may acknowledge only its displayed pool, never a
      // peer's subsequent preparation/round that appeared during the request.
      if (canNavigate?.() && current && current.round_no === room.round_no && current.pool?.version === room.pool?.version && !current.next_round &&
        (current.state === 'voting' || result.match && current.state === 'matched' || result.room_exhausted && current.state === 'exhausted')) flow.adopt(current)
      dragX.set(0)
    },
    onError: async (error) => { dragX.set(0); if ((error instanceof ApiError && error.status === 409) || isRoomError(error, 'STALE_POOL_VERSION') || isRoomError(error, 'VOTE_ALREADY_CAST') || isRoomError(error, 'ALREADY_MATCHED')) { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); await queryClient.invalidateQueries({ queryKey: ['room-events', room.id] }) } },
  })
  const item = events.data?.items[0]
  const itemEventId = item?.event.id
  const itemPosition = item?.position
  const itemCategory = item?.event.category_slug
  const itemHasImage = Boolean(item?.event.imageUrl)
  const itemHasPrice = item?.event.priceFromMinor !== null && item?.event.priceFromMinor !== undefined
  const poolSize = room.pool?.total ?? events.data?.total ?? 0
  useEffect(() => {
    if (!events.data || swipeSessionTracked.current) return
    swipeSessionTracked.current = true
    track('swipe_session_started', { roomId: room.id, properties: { pool_size: room.pool?.total ?? events.data.total } })
  }, [events.data, room.id, room.pool?.total])
  useEffect(() => {
    if (!itemEventId || itemPosition === undefined || !itemCategory) return
    track('event_swipe_impression', { roomId: room.id, eventId: itemEventId, properties: {
      position: itemPosition,
      recommendation_rank: itemPosition + 1,
      pool_size: poolSize,
      category: itemCategory,
      has_image: itemHasImage,
      has_price: itemHasPrice,
    } })
  }, [itemCategory, itemEventId, itemHasImage, itemHasPrice, itemPosition, poolSize, room.id])
  useEffect(() => {
    if (events.data && (room.pool?.room_exhausted || room.state === 'exhausted')) {
      track('swipe_pool_exhausted', { roomId: room.id })
    }
  }, [events.data, room.id, room.pool?.room_exhausted, room.pool?.voted_by_me, room.state])
  if (events.isPending) return <ScreenSkeleton variant="room" label="Загружаем общий пул…" />
  if (events.isError) {
    if (flow.remoteChanged) return <PageShell><TopBar title="Совместный выбор" onBack={() => navigate('/')} /><PageContent className={styles.narrow}><RemoteAdoptCard title={remoteChangeTitle(flow.live ?? room)} onContinue={() => flow.adopt()} /><Button tone="ghost" onClick={() => navigate('/')}>На главную</Button></PageContent></PageShell>
    if (isRoomError(events.error, 'POOL_EXHAUSTED')) return <WaitingScreen room={room} />
    return <RoomReadError title={isRoomError(events.error, 'POOL_NOT_READY') ? 'Пул пока не готов' : 'Не удалось загрузить подборку'} error={events.error} retry={() => void events.refetch()} pending={events.isFetching} />
  }
  const votedByMe = room.pool?.voted_by_me ?? 0
  const poolTotal = room.pool?.total ?? events.data.total
  if (!item) return <WaitingScreen room={room} />
  const cast = (value: VoteValue) => { if (!vote.isPending && !flow.remoteChanged) vote.mutate({ eventId: item.event.id, value }) }
  return (
    <PageShell><TopBar title="Совместный выбор" onBack={() => navigate('/')} right={<span>{Math.min(votedByMe + 1, poolTotal)} / {poolTotal}</span>} /><PageContent className={styles.poolWrap}>
      <p className={styles.eyebrow}>Раунд {room.round_no} из 3</p>
      {poolTotal > 0 && poolTotal <= 2 ? <p role="status" className={styles.subtitle}>В подборке всего {poolTotal === 1 ? '1 событие' : '2 события'}. Можно продолжить выбор.</p> : null}
      <AnimatePresence mode="wait">
        <motion.article key={item.event.id} className={styles.poolCard} style={{ position: 'relative', x: dragX, rotate: rotation }} drag={vote.isPending || flow.remoteChanged ? false : 'x'} dragConstraints={{ left: 0, right: 0 }} dragElastic={.75} initial={{ opacity: 0, scale: .96, y: 18 }} animate={{ opacity: 1, scale: 1, y: 0 }} exit={{ opacity: 0, scale: .9 }} transition={reducedMotion ? { duration: 0 } : { type: 'spring', stiffness: 240, damping: 24 }} onDragEnd={(_, info) => { const direction = resolveSwipeIntent(info.offset.x, info.velocity.x); if (direction) cast(direction); else dragX.set(0) }}>
          <motion.span aria-hidden="true" style={{ opacity: likeOpacity, position: 'absolute', inset: 16, zIndex: 1, pointerEvents: 'none', border: '2px solid #e77888', borderRadius: 18, color: '#c74d63', padding: 12, fontWeight: 'var(--app-type-semibold)' }}>ХОЧУ ПОЙТИ</motion.span>
          <motion.span aria-hidden="true" style={{ opacity: dislikeOpacity, position: 'absolute', inset: 16, zIndex: 1, pointerEvents: 'none', border: '2px solid #b8aaa0', borderRadius: 18, color: '#776a60', padding: 12, fontWeight: 'var(--app-type-semibold)' }}>НЕ ПОДХОДИТ</motion.span>
          <EventImage className={styles.poolImage} src={eventImage(item.event.imageUrl, item.event.category_slug, 768)} fallbackSrc={eventImageFallback(item.event.category_slug)} alt={item.event.title} />
          <div className={styles.poolCopy}><p className={styles.eyebrow}>{eventCategoryLabel(item.event.category_slug)}</p><h2>{item.event.title}</h2><p className={styles.subtitle}>{item.event.subtitle}</p><div className={styles.poolMeta}><RoomEventDate label={item.event.date_label} otherOccurrencesCount={item.event.other_occurrences_count} /><span>⌖ {item.event.venue_name}</span><strong>{item.event.price_label}</strong></div></div>
        </motion.article>
      </AnimatePresence>
      <AnimatePresence>{matchPreview && <motion.div role="status" aria-live="polite" initial={{ opacity: 0, scale: .88 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: .24 }} style={{ position: 'absolute', inset: '28% 12% auto', zIndex: 4, padding: 24, borderRadius: 24, background: 'rgba(255,255,255,.96)', boxShadow: '0 18px 50px rgba(70,48,35,.2)', textAlign: 'center' }}><strong style={{ display: 'block', fontSize: 32 }}>♥</strong><strong>Это мэтч!</strong><span style={{ display: 'block', marginTop: 6 }}>Событие понравилось вам обоим</span></motion.div>}</AnimatePresence>
      <div className={styles.voteActions}><button type="button" className={styles.voteAction} disabled={vote.isPending || flow.remoteChanged} onClick={() => cast('dislike')}><span className={styles.voteCircle} aria-hidden="true">×</span>Не подходит</button><button type="button" className={styles.voteAction} disabled={vote.isPending || flow.remoteChanged} onClick={() => cast('like')}><span className={`${styles.voteCircle} ${styles.like}`} aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 21s-8.2-4.9-10.5-10C-.5 4.7 6.6.6 12 6c5.4-5.4 12.5-1.3 10.5 5C20.2 16.1 12 21 12 21Z" /></svg></span>Хочу пойти</button></div>
      {flow.remoteChanged ? <RemoteAdoptCard title={flow.live?.state === 'matched' || flow.live?.state === 'closed' ? 'Подборка завершена' : 'Подборка обновилась'} onContinue={() => flow.adopt()} /> : null}
      {vote.isError ? <AuthRecoveryAction error={vote.error} /> : null}{vote.isError ? <p className={styles.error} role="alert">{roomErrorMessage(vote.error, 'Голос не сохранился. Повторите действие.')}</p> : null}
    </PageContent></PageShell>
  )
}

function MatchScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const event = useEventDetail(room.match?.event_id)
  const loadedMatchEventId = event.data?.id
  const matchedEventId = room.match?.event_id
  const matchId = room.match?.id
  useEffect(() => { if (loadedMatchEventId && matchedEventId) track('match_shown', { roomId: room.id, eventId: matchedEventId }) }, [loadedMatchEventId, matchId, matchedEventId, room.id])
  const ticket = useMutation({ mutationFn: async () => {
    const { external_url } = await withMinimumDuration(apiClient.recordTicketClick(room.match!.event_id, { source: 'match', room_id: room.id }), 120)
    if (!await maxPlatform.openTicketLink(external_url)) throw new Error('Ticket link could not be opened')
  }, onError: (error) => trackFailure('ticket_redirect_failed', error, { eventId: room.match?.event_id, roomId: room.id }) })
  if (event.isPending) return <ScreenSkeleton variant="event" label="Открываем ваш мэтч…" />
  if (event.isError) return <RoomReadError title="Мэтч найден, но событие не загрузилось" error={event.error} retry={() => void event.refetch()} pending={event.isFetching} />
  return (
    <PageShell><TopBar title="Совпадение" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <Suspense fallback={<ScreenSkeleton variant="event" inline label="Готовим сюрприз…" />}><MatchCelebration event={event.data} participants={room.match?.participants.map((participant) => ({ id: participant.id, displayName: participant.display_name, avatarUrl: participant.avatar_url, role: participant.role, intentReady: participant.intent_ready }))} /></Suspense>
      <div className={`${styles.footer} ${styles.matchActions}`}><Button onClick={() => { track('match_opened', { roomId: room.id, eventId: event.data.id }); navigate(`/events/${event.data.id}`) }}>Открыть событие</Button><Button tone="secondary" disabled={ticket.isPending || !event.data.ticketAvailable} state={ticket.isPending ? 'loading' : 'idle'} loadingLabel="Открываем…" onClick={() => ticket.mutate()}>{event.data.dataProvenance.source === 'user' ? event.data.ticketAvailable ? 'Открыть сайт мероприятия' : 'Ссылка не указана' : event.data.ticketAvailable ? 'К билетам' : 'Билеты недоступны'}</Button></div>
      {ticket.isError ? <AuthRecoveryAction error={ticket.error} /> : null}{ticket.isError ? <p className={styles.error} role="alert">{roomErrorMessage(ticket.error, 'Не удалось открыть билетный сервис. Повторите попытку.')}</p> : null}
    </PageContent></PageShell>
  )
}

function RecoveryScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const { captureNavigation } = useRoomNavigationGuard()
  const flow = useRoomFlow()
  const queryClient = useQueryClient()
  const submitIntent = useIntentSubmission()
  const bootstrap = useBootstrap()
  const recoveryDraftKey = `room-recovery-v1:${bootstrap.data?.user.id ?? 'pending'}:${room.id}:${room.round_no}`
  const [draft] = useState(() => readRecoveryDraft(recoveryDraftKey))
  const [selected, setSelected] = useState<Relaxation['id'][]>(() => normalizeRecoverySelections(draft.selected))
  const [selectedDate, setSelectedDate] = useState<string | null>(draft.selectedDate ?? null)
  const [selectedCategory, setSelectedCategory] = useState<CategorySlug | null>(draft.selectedCategory ?? null)
  const [selectedBudget, setSelectedBudget] = useState<number | null>(draft.selectedBudget ?? null)
  const [review, setReview] = useState(Boolean(draft.review))
  const restartLocked = useRef(false)
  useEffect(() => {
    try { window.sessionStorage.setItem(recoveryDraftKey, JSON.stringify({ selected, selectedDate, selectedCategory, selectedBudget, review })) } catch { /* Storage may be unavailable in a WebView. */ }
  }, [recoveryDraftKey, review, selected, selectedBudget, selectedCategory, selectedDate])
  const timezone = roomCity(room.city_id)?.timezone
  const zeroPool = room.pool?.total === 0
  const suggestions = (timezone ? availableRelaxations(room.myIntent, new Date(), timezone) : []).filter((item) => !zeroPool || item.id !== 'category')
  const selectedSuggestions = suggestions.filter((item) => selected.includes(item.id))
  const nextIntent = (() => {
    try {
      if (!selected.length || selectedSuggestions.length !== selected.length) return null
      const changes: RelaxationChanges = {}
      if (selected.includes('budget')) {
        if (selectedBudget === null) return null
        changes.budget = selectedBudget
      }
      if (selected.includes('category')) {
        if (!selectedCategory) return null
        changes.category = selectedCategory
      }
      if (selected.includes('date')) {
        if (!selectedDate) return null
        changes.date = selectedDate
      }
      if (selected.includes('time')) changes.time = true
      return combinedRelaxedIntent(room.myIntent, changes, new Date(), timezone)
    } catch { return null } // A saved choice may expire while this screen remains open.
  })()
  const restart = useMutation({ onMutate: captureNavigation, mutationFn: () => {
    if (!nextIntent) throw new Error('No room intent change selected')
    const source = remoteEditing ? flow.live ?? room : room
    if (flow.remoteChanged && !remoteEditing) throw new Error('Confirm the updated room first')
    return submitIntent(source, nextIntent)
  }, onSuccess: async (data, _values, canNavigate) => { await Promise.all([queryClient.invalidateQueries({ queryKey: ['room', room.id] }), queryClient.invalidateQueries({ queryKey: ['home-feed'] })]); if (!canNavigate?.() || !flow.adopt(data)) return; if (data.state !== 'exhausted') navigate(`/rooms/${room.id}/waiting`, { replace: true }) }, onError: async (error) => { if (error instanceof ApiError && error.status === 409) await queryClient.invalidateQueries({ queryKey: ['room', room.id] }) }, onSettled: () => { restartLocked.current = false } })
  const [remoteNoticeOpen, setRemoteNoticeOpen] = useState(false)
  const [remoteEditing, setRemoteEditing] = useState(false)
  const sourceRoom = flow.live ?? room
  const remotePreparation = canConfirmPreparation(sourceRoom)
  const preparedWaiting = Boolean(room.next_round?.my_confirmed)
  const confirmPrepared = useMutation({ onMutate: captureNavigation, mutationFn: () => {
    const prepared = sourceRoom.next_round?.my_intent ?? sourceRoom.myIntent
    if (!prepared) throw new Error('No own intent available for next-round confirmation')
    return submitIntent(sourceRoom, fromSavedIntent(prepared))
  }, onSuccess: async (data, _values, canNavigate) => { await Promise.all([queryClient.invalidateQueries({ queryKey: ['room', room.id] }), queryClient.invalidateQueries({ queryKey: ['home-feed'] })]); if (!canNavigate?.()) return; flow.adopt(data) }, onError: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }) } })
  if (!room.allowed_actions.includes('restart_with_new_intent') && !preparedWaiting && !remotePreparation && !flow.remoteChanged) {
    return (
      <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
        <p className={styles.eyebrow}>Раунд {room.round_no} из 3</p><h1 className={styles.title}>{room.round_no >= 3 ? 'Лимит подборок исчерпан' : 'Раунды этой комнаты завершены'}</h1><p className={styles.subtitle}>{room.pool?.total === 0 ? 'В этом раунде общих событий не нашлось. Голосование не начиналось. Доступны три попытки подбора; можно создать новую комнату.' : 'События закончились, общего лайка нет. Создайте новую комнату, когда появятся другие планы.'}</p>
        <PrivacyNote>Предпочтения второго участника по-прежнему не раскрываются.</PrivacyNote>
        <div className={`${styles.footer} ${reliabilityStyles.actions}`}><Button tone="secondary" onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button><Button onClick={() => navigate('/')}>На главную</Button></div>
      </PageContent></PageShell>
    )
  }
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>Раунд {room.round_no} из 3</p>{preparedWaiting ? <p className={styles.subtitle}>Подготовка раунда {room.round_no + 1}</p> : null}
      {!review ? <>
        <h1 className={styles.title}>{zeroPool ? 'Общих событий пока нет' : room.pool && room.pool.total > 0 ? 'События закончились, общего лайка нет' : 'Подборка завершена'}</h1><p className={styles.subtitle}>{zeroPool ? 'По вашим условиям не нашлось общих событий. Голосование в этом раунде не начиналось.' : room.pool && room.pool.total > 0 ? 'Доступные карточки закончились. Можно пересмотреть собственные условия.' : 'Можно пересмотреть собственные условия.'} Изменения не гарантируют больше событий.</p><p className={styles.subtitle}>Вы меняете только свои пожелания. Каждый участник самостоятельно пересматривает и подтверждает свои условия.</p>
        <section className={styles.section}><h2 className={styles.sectionTitle}>Что изменить в подборке?</h2><p className={styles.subtitle}>Можно выбрать несколько изменений</p>{suggestions.map((item) => {
          const checked = selected.includes(item.id)
          return <div key={item.id} className={`${recoveryStyles.option} ${checked ? recoveryStyles.selected : ''}`}>
            <label className={recoveryStyles.toggle}>
              <span><strong>{item.title}</strong>{item.id === 'budget' ? <small>Сейчас до {((room.myIntent?.budget_max_minor ?? 0) / 100).toLocaleString('ru')} ₽ на человека</small> : item.id === 'category' ? <small>Изменит порядок рекомендаций, но не гарантирует больше событий</small> : item.id === 'time' ? <small>Добавим утро, день и ночь</small> : <small>Вы сами выберете конкретный день</small>}</span>
              <input type="checkbox" aria-label={item.title} checked={checked} onChange={() => setSelected((current) => toggleValue(current, item.id))} />
            </label>
            {checked && item.id === 'budget' ? <div className={recoveryStyles.details}><label className={recoveryStyles.label} htmlFor="recovery-budget">Новый лимит стоимости</label><select id="recovery-budget" className={recoveryStyles.select} value={selectedBudget ?? ''} onChange={(event) => setSelectedBudget(event.target.value ? Number(event.target.value) : null)}><option value="">Выберите сумму</option>{item.budgets?.map((amount) => <option key={amount} value={amount}>до {amount.toLocaleString('ru')} ₽</option>)}</select></div> : null}
            {checked && item.id === 'category' ? <div className={recoveryStyles.details}><h3 className={recoveryStyles.label}>Выберите категорию</h3><ChipGroup>{item.categories?.map(({ slug, label }) => <Chip key={slug} selected={selectedCategory === slug} onClick={() => setSelectedCategory((current) => current === slug ? null : slug)}>{label}</Chip>)}</ChipGroup></div> : null}
            {checked && item.id === 'date' ? <div className={recoveryStyles.details}><label className={recoveryStyles.label} htmlFor="recovery-date">Дополнительная дата</label><select id="recovery-date" className={recoveryStyles.select} value={selectedDate ?? ''} onChange={(event) => setSelectedDate(event.target.value || null)}><option value="">Выберите дату</option>{item.dates?.map((date) => <option key={date} value={date}>{formatRoomDate(date)}</option>)}</select></div> : null}
          </div>
        })}
        {suggestions.length === 0 ? <p className={styles.subtitle}>{!timezone ? 'Не удалось определить часовой пояс города. Вернитесь на главную или создайте новую комнату с выбранным городом.' : 'Больше нечего изменить в текущих условиях. Попробуйте позже или создайте новую комнату.'}</p> : null}
      </section></> : <section>
        <p className={styles.eyebrow}>ВАШИ УСЛОВИЯ</p><h1 className={styles.title}>Проверьте изменения</h1><p className={styles.subtitle}>Вы подтвердите только свои условия. Для подбора нужно независимое подтверждение каждого участника.</p>
        <ul className={recoveryStyles.summary} aria-label="Выбранные изменения">{selectedSuggestions.map((item) => <li key={item.id}><strong>{item.title}</strong><span>{item.id === 'budget' ? `До ${((room.myIntent?.budget_max_minor ?? 0) / 100).toLocaleString('ru')} ₽ → до ${selectedBudget?.toLocaleString('ru') ?? ''} ₽` : item.id === 'category' ? `${item.categories?.find(({ slug }) => slug === selectedCategory)?.label ?? ''} — в дополнение к выбранным` : item.id === 'date' ? selectedDate ? formatRoomDate(selectedDate) : '' : 'Утро, день, вечер и ночь'}</span></li>)}</ul>
        <p className={styles.subtitle}>Остальные ваши условия сохранятся</p>
      </section>}
      {restart.isError ? <AuthRecoveryAction error={restart.error} /> : null}{restart.isError ? <p className={styles.error} role="alert">{roomErrorMessage(restart.error, 'Не удалось обновить подборку.')}</p> : null}
      {confirmPrepared.isError ? <><AuthRecoveryAction error={confirmPrepared.error} /><p className={styles.error} role="alert">{roomErrorMessage(confirmPrepared.error, 'Не удалось подтвердить условия. Повторите попытку или измените их.')}</p></> : null}
      <div className={`${styles.footer} ${reliabilityStyles.actions}`}>{flow.remoteChanged && !remotePreparation ? <RemoteAdoptCard title={remoteChangeTitle(sourceRoom)} onContinue={() => flow.adopt()} /> : preparedWaiting ? <section style={{ width: '100%', padding: '12px 14px', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', background: 'var(--color-paper)' }}><strong>Условия сохранены</strong><p style={{ margin: '5px 0 0', fontSize: 14 }}>Ждём, когда второй участник подтвердит условия следующей подборки.</p></section> : remotePreparation && !remoteEditing && !remoteNoticeOpen ? <section style={{ width: '100%', padding: '12px 14px', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', background: 'var(--color-paper)' }}><strong style={{ display: 'block', marginBottom: 5, fontSize: 15 }}>Друг предложил обновить подбор</strong><p style={{ margin: '0 0 10px', fontSize: 14 }}>Вы можете оставить свои условия или изменить их перед следующим раундом</p><Button onClick={() => setRemoteNoticeOpen(true)} style={{ width: 'auto', minHeight: 44 }}>Продолжить</Button></section> : remotePreparation && !remoteEditing ? <section style={{ width: '100%', padding: '12px 14px', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', background: 'var(--color-paper)' }}><strong style={{ display: 'block', marginBottom: 5, fontSize: 15 }}>Как продолжить?</strong><p style={{ margin: '0 0 10px', fontSize: 14 }}>Выберите действие перед следующим раундом.</p><p style={{ margin: '0 0 10px', fontSize: 14 }}>Если общих событий не найдётся, эта попытка всё равно будет использована</p><Button disabled={confirmPrepared.isPending} onClick={() => confirmPrepared.mutate()}>Оставить мои условия</Button><Button tone="secondary" disabled={confirmPrepared.isPending} onClick={() => setRemoteEditing(true)}>Изменить условия</Button></section> : review ? <><p className={styles.subtitle}>Если общих событий не найдётся, эта попытка всё равно будет использована</p><Button disabled={restart.isPending || !nextIntent} state={restart.isPending ? 'loading' : 'idle'} loadingLabel="Сохраняем…" onClick={() => { if (!restart.isPending && !restartLocked.current) { restartLocked.current = true; restart.mutate() } }}>Подтвердить мои условия</Button><Button tone="secondary" disabled={restart.isPending} onClick={() => setReview(false)}>Назад к изменениям</Button><Button tone="secondary" disabled={restart.isPending} onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button><Button tone="ghost" onClick={() => navigate('/')}>На главную</Button></> : <><Button disabled={!nextIntent} onClick={() => { setReview(true); restart.reset() }}>Проверить изменения</Button><Button tone="secondary" onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button><Button tone="ghost" onClick={() => navigate('/')}>На главную</Button></>}</div>
    </PageContent></PageShell>
  )
}

function AuthRecoveryAction({ error }: { error: unknown }) {
  return error instanceof ApiError && error.status === 401 ? <Button type="button" tone="secondary" onClick={() => window.location.reload()}>Восстановить вход в MAX</Button> : null
}

function RoomReadError({ title, error, retry, pending = false }: { title: string; error: unknown; retry: () => void; pending?: boolean }) {
  const navigate = useNavigate()
  const auth = error instanceof ApiError && error.status === 401
  return <Empty title={title} description={roomErrorMessage(error)} action={<div className={`${styles.footer} ${reliabilityStyles.actions}`}>
    <Button disabled={pending} onClick={auth ? () => window.location.reload() : retry}>{auth ? 'Восстановить вход в MAX' : 'Повторить'}</Button>
    <Button tone="secondary" onClick={() => navigate('/')}>На главную</Button>
  </div>} />
}

function ClosedScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const closer = room.closed_by?.display_name
  return <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
    <p className={styles.eyebrow}>КОМНАТА ЗАВЕРШЕНА</p><h1 className={styles.title}>Подборка закрыта</h1><p className={styles.subtitle}>{closer ? `Комнату завершил участник «${closer}».` : 'Эта комната больше не активна.'}{room.closed_at ? ` ${new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(room.closed_at))}` : ''}</p>
    <div className={styles.footer}><Button tone="secondary" onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button><Button onClick={() => navigate('/')}>На главную</Button></div>
  </PageContent></PageShell>
}

function ChoiceField<T extends string>({ title, options, selected, onToggle, error }: { title: string; options: Array<[T, string]>; selected: T[]; onToggle: (value: T) => void; error?: string }) {
  return <fieldset className={intentStyles.choiceField} aria-invalid={Boolean(error)}><legend>{title}</legend><ChoiceChips className={intentStyles.choiceOptions} options={options} selected={selected} onToggle={onToggle} />{error ? <span className={intentStyles.fieldError} role="alert">{error}</span> : null}</fieldset>
}

const noRoomRefetch = () => undefined

function expectedScreen(room: RoomSnapshot) {
  if (room.state === 'matched') return 'match'
  if (room.state === 'exhausted') return 'recovery'
  if (room.state === 'voting') return room.pool?.my_pool_finished || !room.allowed_actions.includes('vote') ? 'waiting' : 'vote'
  if (room.state === 'ranking') return 'waiting'
  return room.allowed_actions.includes('edit_intent') ? 'intent' : 'waiting'
}
function roomPhaseKey(room: RoomSnapshot) { return `${room.round_no}:${room.state}:${room.pool?.version ?? ''}:${room.pool?.my_pool_finished ?? ''}:${room.next_round?.round_no ?? ''}:${room.next_round?.my_confirmed ?? ''}:${expectedScreen(room)}` }

function toIntent(values: IntentForm): RoomIntentRequestDto {
  return { dates: values.dates, day_types: [], time_slots: values.time_slots, category_slugs: values.category_slugs, budget_max_minor: Math.round(values.budget * 100), radius_m: null, exclusion_slugs: [], location: null, free_text: values.free_text.trim() || null }
}
function RemoteChangeCard({ onKeep, onEdit, pending }: { onKeep: () => void; onEdit: () => void; pending: boolean }) {
  const [open, setOpen] = useState(false)
  return !open ? <section style={{ width: '100%', padding: '12px 14px', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', background: 'var(--color-paper)' }}><strong style={{ display: 'block', marginBottom: 5, fontSize: 15 }}>Друг предложил обновить подбор</strong><p style={{ margin: '0 0 10px', fontSize: 14 }}>Вы можете оставить свои условия или изменить их перед следующим раундом</p><Button type="button" style={{ width: 'auto', minHeight: 44 }} onClick={() => setOpen(true)}>Продолжить</Button></section> : <section style={{ width: '100%', padding: '12px 14px', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', background: 'var(--color-paper)' }}><strong style={{ display: 'block', marginBottom: 5, fontSize: 15 }}>Как продолжить?</strong><p style={{ margin: '0 0 10px', fontSize: 14 }}>Если общих событий не найдётся, эта попытка всё равно будет использована</p><Button type="button" disabled={pending} onClick={onKeep}>Оставить мои условия</Button><Button type="button" tone="secondary" disabled={pending} onClick={onEdit}>Изменить условия</Button></section>
}
function RemoteAdoptCard({ title, onContinue }: { title: string; onContinue: () => void }) {
  return <section style={{ width: '100%', marginTop: 16, padding: '12px 14px', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', background: 'var(--color-paper)' }}><strong style={{ display: 'block', marginBottom: 5, fontSize: 15 }}>{title}</strong><p style={{ margin: '0 0 10px', fontSize: 14 }}>Продолжите, чтобы открыть актуальное состояние комнаты.</p><Button type="button" style={{ width: 'auto', minHeight: 44 }} onClick={onContinue}>Продолжить</Button></section>
}
function fromSavedIntent(intent: NonNullable<RoomSnapshot['myIntent']>): RoomIntentRequestDto {
  const { dates, day_types, time_slots, category_slugs, budget_max_minor, radius_m, exclusion_slugs, location, free_text } = intent
  return { dates, day_types, time_slots, category_slugs, budget_max_minor, radius_m, exclusion_slugs, location, free_text }
}
function canConfirmPreparation(room: RoomSnapshot) { return room.state === 'exhausted' && Boolean(room.next_round && !room.next_round.my_confirmed && room.allowed_actions.includes('confirm_next_round')) }
function remoteChangeTitle(room: RoomSnapshot) { return room.state === 'closed' ? 'Комната закрыта' : room.state === 'matched' ? 'Совпадение найдено' : 'Подборка обновилась' }
function useIntentSubmission() {
  const attempt = useRef<{ fingerprint: string; key: string } | null>(null)
  return (room: RoomSnapshot, input: RoomIntentRequestDto) => {
    const fingerprint = JSON.stringify([room.id, room.round_no, room.version, input])
    if (attempt.current?.fingerprint !== fingerprint) attempt.current = { fingerprint, key: crypto.randomUUID() }
    return apiClient.replaceMyIntent(room.id, input, { expectedRoundNo: room.round_no, expectedRoomVersion: room.version, idempotencyKey: attempt.current.key })
  }
}
type RecoveryDraft = { selected?: Relaxation['id'] | Relaxation['id'][]; selectedDate?: string | null; selectedCategory?: CategorySlug | null; selectedBudget?: number | null; review?: boolean }
function readIntentDraft(key: string, fallback: IntentForm): IntentForm {
  try { const parsed = intentSchema().safeParse(JSON.parse(window.sessionStorage.getItem(key) ?? 'null')); return parsed.success ? parsed.data : fallback } catch { return fallback }
}
function readRecoveryDraft(key: string): RecoveryDraft {
  try { return JSON.parse(window.sessionStorage.getItem(key) ?? '{}') ?? {} } catch { return {} }
}
function normalizeRecoverySelections(value: RecoveryDraft['selected']): Relaxation['id'][] {
  const values = Array.isArray(value) ? value : value ? [value] : []
  return values.filter((item): item is Relaxation['id'] => item === 'budget' || item === 'category' || item === 'date' || item === 'time')
}

function toggleValue<T>(items: T[], value: T) { return items.includes(value) ? items.filter((item) => item !== value) : [...items, value] }

function localDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function formatRoomDate(date: string) { return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', weekday: 'long' }).format(new Date(`${date}T12:00:00`)) }

function intentDefaults(room: RoomSnapshot, fallbackDate: string): IntentForm {
  const intent = room.myIntent
  return intent ? {
    dates: [...intent.dates],
    time_slots: [...intent.time_slots],
    category_slugs: [...intent.category_slugs],
    budget: intent.budget_max_minor / 100,
    free_text: intent.free_text ?? '',
  } : room.public_plan ? { dates: [...room.public_plan.dates], time_slots: [...room.public_plan.time_slots], category_slugs: [...room.public_plan.category_slugs], budget: room.public_plan.budget_max_minor / 100, free_text: '' } : { dates: [fallbackDate], time_slots: ['evening'], category_slugs: ['concerts', 'standup'], budget: 3_000, free_text: '' }
}

function maxAppUrl(token: string) {
  const base = import.meta.env.VITE_MAX_APP_URL ?? 'https://max.ru'
  return `${base}${base.includes('?') ? '&' : '?'}startapp=${encodeURIComponent(token)}`
}
