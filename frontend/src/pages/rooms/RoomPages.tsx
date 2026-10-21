import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AnimatePresence, motion, useMotionValue, useReducedMotion, useTransform } from 'motion/react'
import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { Navigate, useNavigate, useParams } from 'react-router-dom'
import { z } from 'zod'
import { useEventDetail } from '../../features/discovery/queries'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { useRoom, useRoomEvents } from '../../features/rooms/queries'
import { RoomEventDate } from '../../features/rooms/RoomEventDate'
import { availableRelaxations, relaxedIntent } from '../../features/rooms/relaxation'
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
import styles from '../pages.module.css'
import intentStyles from './intent.module.css'
import reliabilityStyles from './reliability.module.css'
import { track, trackFailure } from '../../shared/analytics/client'
import { CreateRoomForm } from './CreateRoomForm'
import { PlanSummary } from './PlanSummary'
import { cityToday, isCanonicalDate, roomCity, sharePlanText } from './public-plan'

const MatchCelebration = lazy(() => import('../../features/rooms/MatchCelebration').then((module) => ({ default: module.MatchCelebration })))
export function NewRoomPage() {
  const navigate = useNavigate()
  const reducedMotion = useReducedMotion()
  const bootstrap = useBootstrap()
  const queryClient = useQueryClient()
  const [createSuccess, setCreateSuccess] = useState(false)
  const createAttempt = useRef<{ body: string; key: ReturnType<typeof crypto.randomUUID> } | null>(null)
  const draftKey = `room-create-v1:${bootstrap.data?.user.id ?? 'pending'}`
  useEffect(() => { track('room_creation_started') }, [])
  const create = useMutation({
    mutationFn: (values: Parameters<typeof apiClient.createRoom>[0]) => {
      const body = JSON.stringify(values)
      if (createAttempt.current?.body !== body) createAttempt.current = { body, key: crypto.randomUUID() }
      return apiClient.createRoom(values, createAttempt.current.key)
    },
    onSuccess: async ({ room }) => {
      queryClient.setQueryData(['room', room.id], room)
      setCreateSuccess(true)
      try { window.sessionStorage.removeItem(draftKey) } catch { /* Storage may be unavailable in a WebView. */ }
      await new Promise<void>((resolve) => window.setTimeout(resolve, reducedMotion ? 0 : 350))
      navigate(`/rooms/${room.id}/invite`)
    },
    onError: (error) => trackFailure('room_creation_failed', error),
  })
  if (bootstrap.isPending) return <ScreenSkeleton variant="room" label="Загружаем профиль…" />
  return (
    <PageShell><TopBar title="Новая комната" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ВМЕСТЕ ЛЕГЧЕ ВЫБРАТЬ</p><h1 className={styles.title}>Создать комнату</h1><p className={styles.subtitle}>Выберите город и предложите план встречи. Каждый отдельно подтвердит свои пожелания.</p>
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
  const [inviteStatus, setInviteStatus] = useState<'idle' | 'sharing' | 'copying' | 'opened' | 'cancelled' | 'share-error' | 'copied' | 'copy-error'>('idle')
  const expiresAt = room.data?.invite?.expires_at
  const expired = useInviteExpired(expiresAt, room.refetch)
  if (room.isPending) return <ScreenSkeleton variant="room" label="Готовим приглашение…" />
  if (room.isError || !room.data) return <RoomReadError title="Комната недоступна" error={room.error} retry={() => void room.refetch()} pending={room.isFetching} />
  const rawUrl = room.data.invite?.url ?? ''
  const maxLink = room.data.invite?.max_deep_link ?? rawUrl
  const canInvite = Boolean(room.data.invite) && room.data.allowed_actions.includes('invite') && room.data.participants.length < 2 && !expired && ['collecting_intents', 'ranking'].includes(room.data.state)
  const busy = inviteStatus === 'sharing' || inviteStatus === 'copying'
  const share = async () => {
    if (!canInvite || !maxLink || busy) return
    setInviteStatus('sharing')
    const result = await maxPlatform.shareInvite({ text: sharePlanText(room.data), link: maxLink })
    setInviteStatus(result.status === 'opened' ? 'opened' : result.status === 'cancelled' ? 'cancelled' : 'share-error')
    // invite_shared requires an agreed metric definition; opening a sheet is not delivery.
    if (result.status === 'failed' || result.status === 'unavailable') trackFailure('invite_share_failed', new Error('Invite sharing failed'), { roomId: room.data.id })
  }
  const copy = async () => {
    if (!canInvite || !rawUrl || busy) return
    setInviteStatus('copying')
    const ok = await maxPlatform.copyText(rawUrl)
    setInviteStatus(ok ? 'copied' : 'copy-error')
  }
  if (!canInvite) return <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
    <h1 className={styles.title}>Приглашение больше недоступно</h1><p className={styles.subtitle}>{expired ? 'Срок приглашения истёк.' : room.data.participants.length >= 2 ? 'Друг уже присоединился. Продолжите совместный выбор в комнате.' : 'Вступление в эту комнату завершено.'}</p>
    <div className={styles.footer}><Button onClick={() => navigate(`/rooms/${room.data.id}/${expectedScreen(room.data)}`)}>Перейти в комнату</Button><Button tone="secondary" onClick={() => navigate('/')}>На главную</Button></div>
  </PageContent></PageShell>
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>И</span><span className={styles.avatar}>{room.data.participants.length > 1 ? 'А' : '+'}</span></div>
      <h1 className={styles.title}>Пригласите друга</h1><p className={styles.subtitle}>Отправьте приглашение в MAX. После подключения каждый отдельно заполнит пожелания.</p>
      <PlanSummary plan={room.data.public_plan} cityId={room.data.city_id} />
      <div className={styles.inviteLink} data-testid="invite-url">{rawUrl || 'Ссылка появится после создания комнаты'}</div>
      <PrivacyNote>Предпочтения и оценки останутся скрытыми до взаимного лайка.</PrivacyNote>
      <div aria-live="polite">{inviteStatus === 'opened' ? <p>Открыли отправку в MAX</p> : inviteStatus === 'cancelled' ? <p>Отправка отменена. Можно попробовать снова или скопировать ссылку.</p> : inviteStatus === 'copied' ? <p>✓ Ссылка скопирована</p> : inviteStatus === 'share-error' ? <p className={styles.error} role="alert">Не удалось открыть отправку. Попробуйте ещё раз или скопируйте ссылку.</p> : inviteStatus === 'copy-error' ? <p className={styles.error} role="alert">Не удалось скопировать ссылку. Вы можете скопировать её вручную выше.</p> : null}</div>
      <div className={styles.footer}><Button disabled={!maxLink || busy} state={inviteStatus === 'sharing' ? 'loading' : 'idle'} loadingLabel="Открываем MAX…" onClick={() => void share()}>Пригласить через MAX</Button><Button tone="secondary" disabled={!rawUrl || busy} state={inviteStatus === 'copying' ? 'loading' : 'idle'} loadingLabel="Копируем…" onClick={() => void copy()}>Скопировать ссылку</Button><Button tone="ghost" onClick={() => navigate(`/rooms/${roomId}/intent`)}>Перейти к моим пожеланиям</Button></div>
    </PageContent></PageShell>
  )
}

export function JoinPage() {
  const { inviteToken } = useParams()
  const navigate = useNavigate()
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
  const join = useMutation({ mutationFn: () => {
    if (!inviteToken) throw new Error('Invite token is missing')
    return apiClient.joinRoom(inviteToken)
  }, onSuccess: async (room) => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['room', room.id] }),
      queryClient.invalidateQueries({ queryKey: ['room-events', room.id] }),
      queryClient.invalidateQueries({ queryKey: ['home-feed'] }),
    ])
    navigate(`/rooms/${room.id}/intent`, { replace: true })
  }, onError: async (error) => {
    trackFailure('room_join_failed', error, { roomId: context?.room_id })
    if (error instanceof ApiError && (error.status === 409 || error.code === 'INVITE_EXPIRED')) await queryClient.invalidateQueries({ queryKey: ['bootstrap'] })
  } })
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>{context?.inviter.display_name.slice(0, 1) ?? 'И'}</span><span className={styles.avatar}>+</span></div>
      <p className={styles.eyebrow}>СОВМЕСТНЫЙ ВЫБОР</p><h1 className={styles.title}>{context ? `Вас приглашает ${context.inviter.display_name}` : 'Вас пригласили выбрать, куда сходить'}</h1><p className={styles.subtitle}>{context ? `Комната «${context.room_name}». ` : ''}После подключения каждый самостоятельно укажет свои условия.</p>
      {context ? <PlanSummary plan={context.public_plan} cityId={context.city_id} /> : null}
      <PrivacyNote />{join.isError ? <AuthRecoveryAction error={join.error} /> : null}{join.isError ? <p className={styles.error} role="alert">{roomErrorMessage(join.error, 'Ссылка недействительна или комната уже заполнена.')}</p> : null}
      {context?.status === 'expired' || expired ? <p className={styles.error} role="alert">Срок приглашения истёк.</p> : context?.status === 'full' ? <p className={styles.error} role="alert">Комната уже заполнена.</p> : null}
      <div className={styles.footer}><Button disabled={join.isPending || joinBlocked} state={join.isPending ? 'loading' : 'idle'} loadingLabel="Подключаем…" onClick={() => { if (context?.already_joined) { navigate(`/rooms/${context.room_id}/intent`, { replace: true }); return } if (!join.isPending && !joinBlocked) { track('room_join_started', { roomId: context?.room_id }); join.mutate() } }}>{context?.already_joined ? 'Продолжить в комнате' : 'Присоединиться'}</Button><Button tone="ghost" onClick={() => navigate('/')}>На главную</Button>{!maxPlatform.isMax && inviteToken ? <Button tone="secondary" onClick={() => void maxPlatform.openMaxLink(maxAppUrl(inviteToken))}>Открыть в MAX</Button> : null}</div>
    </PageContent></PageShell>
  )
}

export function RoomFlowPage() {
  const { roomId, roomScreen } = useParams()
  const room = useRoom(roomId)
  const loadedRoomId = room.data?.id
  useEffect(() => { if (loadedRoomId) track('room_opened', { roomId: loadedRoomId }) }, [loadedRoomId])
  if (room.isPending) return <ScreenSkeleton variant="room" label="Восстанавливаем комнату…" />
  if (!room.data) return <RoomReadError title="Комната недоступна" error={room.error} retry={() => void room.refetch()} pending={room.isFetching} />
  const expected = expectedScreen(room.data)
  let content
  if (room.data.state === 'closed') content = <ClosedScreen room={room.data} />
  else if (roomScreen !== expected) content = <Navigate to={`/rooms/${roomId}/${expected}`} replace />
  else if (expected === 'intent') content = <IntentScreen key={`${room.data.id}:${room.data.round_no}`} room={room.data} />
  else if (expected === 'waiting') content = <WaitingScreen room={room.data} />
  else if (expected === 'vote') content = <VoteScreen room={room.data} />
  else if (expected === 'match') content = <MatchScreen room={room.data} />
  else content = <RecoveryScreen key={`${room.data.id}:${room.data.round_no}`} room={room.data} />
  return <>
    {room.isError ? <RoomReadError title="Не удалось обновить комнату" error={room.error} retry={() => void room.refetch()} pending={room.isFetching} /> : null}
    <fieldset disabled={room.isError} style={{ display: room.isError ? 'none' : 'contents', border: 0, padding: 0, margin: 0 }}>{content}</fieldset>
  </>
}

const intentSchema = (timezone?: string) => z.object({
  dates: z.array(z.string()).min(1, 'Выберите хотя бы одну дату').max(14, 'Не больше 14 дат').refine((dates) => new Set(dates).size === dates.length && dates.every((date) => isCanonicalDate(date) && (!timezone || date >= cityToday(timezone))), 'Выберите актуальные даты без повторов; прошедшие даты нужно изменить самостоятельно'),
  day_types: z.array(z.enum(['weekday', 'weekend'])),
  time_slots: z.array(z.enum(['morning', 'day', 'evening', 'night'])),
  category_slugs: z.array(z.enum(CATEGORY_SLUGS)).min(1),
  budget: z.number({ invalid_type_error: 'Введите бюджет' }).min(0).max(1_000_000).multipleOf(0.01, 'Укажите сумму с точностью до копейки'),
  free_text: z.string().max(300),
})
type IntentForm = z.infer<ReturnType<typeof intentSchema>>

function IntentScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const city = roomCity(room.city_id)
  const timezone = city?.timezone
  const initialDates = room.myIntent?.dates ?? room.public_plan?.dates ?? []
  const [additionalDates, setAdditionalDates] = useState<string[]>([])
  const [dateToAdd, setDateToAdd] = useState('')
  const [dateError, setDateError] = useState('')
  const errorSummary = useRef<HTMLParagraphElement>(null)
  const upcoming = timezone ? upcomingCityDates(7, timezone) : room.public_plan ? [] : upcomingDates(7)
  const form = useForm<IntentForm>({ resolver: zodResolver(intentSchema(timezone)), defaultValues: intentDefaults(room, upcoming[0]?.[0] ?? '') })
  const dates = useWatch({ control: form.control, name: 'dates' })
  const dateChoices: Array<[string, string]> = [...new Set([...initialDates, ...dates, ...upcoming.map(([date]) => date), ...additionalDates])].map((date) => [date, formatRoomDate(date)])
  const dayTypes = useWatch({ control: form.control, name: 'day_types' })
  const timeSlots = useWatch({ control: form.control, name: 'time_slots' })
  const categories = useWatch({ control: form.control, name: 'category_slugs' })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const save = useMutation({
    mutationFn: (values: IntentForm) => apiClient.replaceMyIntent(room.id, toIntent(values)),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); navigate(`/rooms/${room.id}/waiting`, { replace: true }) },
    onError: async (error) => {
      if (error instanceof ApiError && error.status === 409) await queryClient.invalidateQueries({ queryKey: ['room', room.id] })
      if (!(error instanceof ApiError)) return
      const mapping: Record<string, keyof IntentForm> = { dates: 'dates', day_types: 'day_types', time_slots: 'time_slots', category_slugs: 'category_slugs', budget_max_minor: 'budget', free_text: 'free_text' }
      for (const [field, message] of Object.entries(error.fieldErrors)) if (mapping[field]) form.setError(mapping[field], { message })
    },
  })
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${intentStyles.intentPage}`}>
      <p className={`${styles.eyebrow} ${intentStyles.introEyebrow}`}>ВАШИ УСЛОВИЯ ДЛЯ ЭТОЙ ВСТРЕЧИ</p><h1 className={`${styles.title} ${intentStyles.introTitle}`}>Мои предпочтения</h1><p className={`${styles.subtitle} ${intentStyles.introSubtitle}`}>Укажите, что подходит именно сейчас. Другой участник не увидит ответы.</p>
      {room.public_plan && !room.myIntent ? <p className={styles.subtitle}>Поля заполнены из публичного предложения. Измените их для себя и подтвердите свои предпочтения. Другой участник подтверждает их отдельно.</p> : null}
      <p className={styles.subtitle}>{city ? `${city.name} · ${city.timezone}` : 'Часовой пояс города проверит сервер.'}</p><PrivacyNote />
      <form className={`${styles.formStack} ${intentStyles.intentForm}`} onSubmit={(event) => { void form.handleSubmit((values) => { if (!save.isPending) save.mutate(values) }, () => window.requestAnimationFrame(() => errorSummary.current?.focus()))(event) }}>
        {Object.keys(form.formState.errors).length ? <p ref={errorSummary} tabIndex={-1} className={`${styles.error} ${intentStyles.formErrors}`} role="alert">Проверьте выбранные даты, время, бюджет и интересы.</p> : null}
        <ChoiceField title="Когда удобно" error={form.formState.errors.dates?.message} options={dateChoices} selected={dates} onToggle={(value) => form.setValue('dates', toggleValue(dates, value), { shouldValidate: true })} />
        <div className={intentStyles.dateEntry}>
          <label className={intentStyles.textField} htmlFor="intent-add-date">Другая дата<input id="intent-add-date" className={styles.input} type="date" min={timezone ? cityToday(timezone) : undefined} value={dateToAdd} onChange={(event) => { setDateToAdd(event.target.value); setDateError('') }} aria-invalid={Boolean(dateError)} aria-describedby={dateError ? 'intent-date-error' : undefined} /></label>
          <Button type="button" tone="secondary" disabled={save.isPending || dates.length >= 14} onClick={() => {
            if (!isCanonicalDate(dateToAdd) || (timezone && dateToAdd < cityToday(timezone))) { setDateError('Выберите актуальную дату в часовом поясе города.'); return }
            setAdditionalDates((values) => values.includes(dateToAdd) ? values : [...values, dateToAdd])
            if (!dates.includes(dateToAdd)) form.setValue('dates', [...dates, dateToAdd], { shouldValidate: true, shouldDirty: true })
            setDateToAdd('')
          }}>Добавить дату</Button>
          {dateError ? <p id="intent-date-error" className={intentStyles.fieldError} role="alert">{dateError}</p> : null}
        </div>
        <ChoiceField title="Тип дня (необязательно)" error={form.formState.errors.day_types?.message} options={[['weekday', 'Будни'], ['weekend', 'Выходные']]} selected={dayTypes} onToggle={(value) => form.setValue('day_types', toggleValue(dayTypes, value as IntentForm['day_types'][number]), { shouldValidate: true })} />
        <ChoiceField title="Время" error={form.formState.errors.time_slots?.message} options={[['morning', 'Утро'], ['day', 'День'], ['evening', 'Вечер'], ['night', 'Ночь']]} selected={timeSlots} onToggle={(value) => form.setValue('time_slots', toggleValue(timeSlots, value as IntentForm['time_slots'][number]), { shouldValidate: true })} />
        <ChoiceField title="Что интересно" error={form.formState.errors.category_slugs?.message} options={CATEGORY_OPTIONS.map(({ slug, label }) => [slug, label] as [CategorySlug, string])} selected={categories} onToggle={(value) => form.setValue('category_slugs', toggleValue(categories, value as CategorySlug), { shouldValidate: true })} />
        <label className={intentStyles.rangeField} htmlFor="room-budget">Бюджет, ₽<input id="room-budget" aria-label="Бюджет, ₽" aria-describedby="room-budget-help" className={styles.input} type="number" inputMode="decimal" min="0" max="1000000" step="0.01" required aria-invalid={Boolean(form.formState.errors.budget)} {...form.register('budget', { valueAsNumber: true })} /><span id="room-budget-help" className={styles.subtitle}>До {Number(budget || 0).toLocaleString('ru', { maximumFractionDigits: 2 })} ₽ для вас</span>{form.formState.errors.budget ? <span className={intentStyles.fieldError} role="alert">{form.formState.errors.budget.message}</span> : null}</label>
        <label className={intentStyles.textField} htmlFor="room-free-text">Дополнительное пожелание<textarea id="room-free-text" className={styles.textarea} placeholder="Например: хочется спокойного места" aria-invalid={Boolean(form.formState.errors.free_text)} maxLength={300} {...form.register('free_text')} />{form.formState.errors.free_text ? <span className={intentStyles.fieldError} role="alert">{form.formState.errors.free_text.message}</span> : null}</label>
        {save.isError ? <AuthRecoveryAction error={save.error} /> : null}{save.isError ? <p className={`${styles.error} ${intentStyles.formErrors}`} role="alert">{roomErrorMessage(save.error, 'Не удалось сохранить предпочтения.')}</p> : null}
        <div className={`${styles.footer} ${intentStyles.intentFooter}`}><Button type="submit" disabled={save.isPending}>{save.isPending ? 'Сохраняем приватно…' : 'Сохранить предпочтения'}</Button></div>
      </form>
    </PageContent></PageShell>
  )
}

function WaitingScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const reducedMotion = useReducedMotion()
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ОБЩАЯ ПОДБОРКА</p><h1 className={styles.title}>{room.state === 'ranking' ? 'Формируем общий пул…' : 'Ждём второго участника'}</h1><p className={styles.subtitle}>{room.state === 'voting' ? 'Вы просмотрели все события. Ждём, когда второй участник завершит выбор.' : 'Ваши пожелания сохранены. Подборка появится, когда оба участника будут готовы.'}</p>
      <div className={styles.statusList} aria-live="polite"><AnimatePresence initial={false}>{room.participants.map((participant) => <motion.div className={styles.statusRow} key={participant.id} layout={!reducedMotion} initial={reducedMotion ? false : { opacity: 0, y: 8, scale: .92 }} animate={{ opacity: 1, y: 0, scale: 1 }} exit={reducedMotion ? undefined : { opacity: 0, y: -8, scale: .92 }} transition={{ duration: reducedMotion ? 0 : .24 }}><span className={styles.smallAvatar}>{participantInitials(participant.displayName)}</span><span><strong>{participant.displayName}</strong><small>{room.state === 'voting' ? 'Участник совместного выбора' : participant.intentReady ? '✓ Предпочтения заполнены' : 'Заполняет предпочтения…'}</small></span></motion.div>)}</AnimatePresence></div>
      <p aria-live="polite" style={{ minHeight: 20, textAlign: 'center', color: 'var(--color-muted)' }}>{room.participants.length} / 2 участника</p>
      {room.state !== 'voting' ? <div className={styles.explain}><strong>Как формируется подборка</strong><span>✓ Учитываем постоянные интересы</span><span>✓ Добавляем текущие условия</span><span>{room.participants.length === 2 ? '○ Ждём готовности обоих' : '○ Ждём присоединения друга'}</span></div> : null}<PrivacyNote />
    </PageContent></PageShell>
  )
}

function VoteScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const events = useRoomEvents(room.id, true)
  const swipeSessionTracked = useRef(false)
  const [matchPreview, setMatchPreview] = useState(false)
  const dragX = useMotionValue(0)
  const rotation = useTransform(dragX, [-260, 0, 260], [-10, 0, 10])
  const likeOpacity = useTransform(dragX, [0, 90], [0, 1])
  const dislikeOpacity = useTransform(dragX, [-90, 0], [1, 0])
  const reducedMotion = useReducedMotion()
  const vote = useMutation({
    mutationFn: ({ eventId, value }: { eventId: string; value: VoteValue }) => apiClient.vote(room.id, eventId, { pool_version: room.pool?.version ?? room.version, vote: value }),
    onSuccess: async (result) => {
      if (result.match) {
        setMatchPreview(true)
        await new Promise<void>((resolve) => window.setTimeout(resolve, reducedMotion ? 0 : 650))
        setMatchPreview(false)
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['room', room.id] }),
        queryClient.invalidateQueries({ queryKey: ['room-events', room.id] }),
      ])
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
    if (isRoomError(events.error, 'POOL_EXHAUSTED')) return <WaitingScreen room={room} />
    return <RoomReadError title={isRoomError(events.error, 'POOL_NOT_READY') ? 'Пул пока не готов' : 'Не удалось загрузить подборку'} error={events.error} retry={() => void events.refetch()} pending={events.isFetching} />
  }
  const votedByMe = room.pool?.voted_by_me ?? 0
  const poolTotal = room.pool?.total ?? events.data.total
  if (!item) return <WaitingScreen room={room} />
  const cast = (value: VoteValue) => { if (!vote.isPending) vote.mutate({ eventId: item.event.id, value }) }
  return (
    <PageShell><TopBar title="Совместный выбор" onBack={() => navigate('/')} right={<span>{Math.min(votedByMe + 1, poolTotal)} / {poolTotal}</span>} /><PageContent className={styles.poolWrap}>
      {poolTotal > 0 && poolTotal <= 2 ? <p role="status" className={styles.subtitle}>В подборке всего {poolTotal === 1 ? '1 событие' : '2 события'}. Можно продолжить выбор.</p> : null}
      <AnimatePresence mode="wait">
        <motion.article key={item.event.id} className={styles.poolCard} style={{ position: 'relative', x: dragX, rotate: rotation }} drag={vote.isPending ? false : 'x'} dragConstraints={{ left: 0, right: 0 }} dragElastic={.75} initial={{ opacity: 0, scale: .96, y: 18 }} animate={{ opacity: 1, scale: 1, y: 0 }} exit={{ opacity: 0, scale: .9 }} transition={reducedMotion ? { duration: 0 } : { type: 'spring', stiffness: 240, damping: 24 }} onDragEnd={(_, info) => { const direction = resolveSwipeIntent(info.offset.x, info.velocity.x); if (direction) cast(direction); else dragX.set(0) }}>
          <motion.span aria-hidden="true" style={{ opacity: likeOpacity, position: 'absolute', inset: 16, zIndex: 1, pointerEvents: 'none', border: '2px solid #e77888', borderRadius: 18, color: '#c74d63', padding: 12, fontWeight: 800 }}>ХОЧУ ПОЙТИ</motion.span>
          <motion.span aria-hidden="true" style={{ opacity: dislikeOpacity, position: 'absolute', inset: 16, zIndex: 1, pointerEvents: 'none', border: '2px solid #b8aaa0', borderRadius: 18, color: '#776a60', padding: 12, fontWeight: 800 }}>НЕ ПОДХОДИТ</motion.span>
          <EventImage className={styles.poolImage} src={eventImage(item.event.imageUrl, item.event.category_slug, 768)} fallbackSrc={eventImageFallback(item.event.category_slug)} alt={item.event.title} />
          <div className={styles.poolCopy}><p className={styles.eyebrow}>{eventCategoryLabel(item.event.category_slug)}</p><h2>{item.event.title}</h2><p className={styles.subtitle}>{item.event.subtitle}</p><div className={styles.poolMeta}><RoomEventDate label={item.event.date_label} otherOccurrencesCount={item.event.other_occurrences_count} /><span>⌖ {item.event.venue_name}</span><strong>{item.event.price_label}</strong></div></div>
        </motion.article>
      </AnimatePresence>
      <AnimatePresence>{matchPreview && <motion.div role="status" aria-live="polite" initial={{ opacity: 0, scale: .88 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: .24 }} style={{ position: 'absolute', inset: '28% 12% auto', zIndex: 4, padding: 24, borderRadius: 24, background: 'rgba(255,255,255,.96)', boxShadow: '0 18px 50px rgba(70,48,35,.2)', textAlign: 'center' }}><strong style={{ display: 'block', fontSize: 32 }}>♥</strong><strong>Это мэтч!</strong><span style={{ display: 'block', marginTop: 6 }}>Событие понравилось вам обоим</span></motion.div>}</AnimatePresence>
      <div className={styles.voteActions}><button type="button" className={styles.voteAction} disabled={vote.isPending} onClick={() => cast('dislike')}><span className={styles.voteCircle} aria-hidden="true">×</span>Не подходит</button><button type="button" className={styles.voteAction} disabled={vote.isPending} onClick={() => cast('like')}><span className={`${styles.voteCircle} ${styles.like}`} aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 21s-8.2-4.9-10.5-10C-.5 4.7 6.6.6 12 6c5.4-5.4 12.5-1.3 10.5 5C20.2 16.1 12 21 12 21Z" /></svg></span>Хочу пойти</button></div>
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
      <div className={styles.footer}><Button onClick={() => { track('match_opened', { roomId: room.id, eventId: event.data.id }); navigate(`/events/${event.data.id}`) }}>Открыть событие</Button><Button tone="secondary" disabled={ticket.isPending || !event.data.ticketAvailable} state={ticket.isPending ? 'loading' : 'idle'} loadingLabel="Открываем…" onClick={() => ticket.mutate()}>{event.data.dataProvenance.source === 'user' ? event.data.ticketAvailable ? 'Открыть сайт мероприятия' : 'Ссылка не указана' : event.data.ticketAvailable ? 'К билетам' : 'Билеты недоступны'}</Button></div>
      {ticket.isError ? <AuthRecoveryAction error={ticket.error} /> : null}{ticket.isError ? <p className={styles.error} role="alert">{roomErrorMessage(ticket.error, 'Не удалось открыть билетный сервис. Повторите попытку.')}</p> : null}
    </PageContent></PageShell>
  )
}

function RecoveryScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<string | null>(null)
  const [selectedDate, setSelectedDate] = useState<string | null>(null)
  const [selectedCategory, setSelectedCategory] = useState<CategorySlug | null>(null)
  const [selectedBudget, setSelectedBudget] = useState<number | null>(null)
  const [review, setReview] = useState(false)
  const timezone = roomCity(room.city_id)?.timezone
  const zeroPool = room.pool?.total === 0
  const suggestions = (timezone ? availableRelaxations(room.myIntent, new Date(), timezone) : []).filter((item) => !zeroPool || item.id !== 'category')
  const selectedSuggestion = suggestions.find((item) => item.id === selected)
  const nextIntent = (() => {
    try {
      return selectedSuggestion?.id === 'date' && selectedDate ? relaxedIntent(room.myIntent, 'date', new Date(), selectedDate, timezone)
        : selectedSuggestion?.id === 'category' && selectedCategory ? relaxedIntent(room.myIntent, 'category', new Date(), selectedCategory, timezone)
        : selectedSuggestion?.id === 'budget' && selectedBudget !== null ? relaxedIntent(room.myIntent, 'budget', new Date(), selectedBudget, timezone)
        : selectedSuggestion?.intent ?? null
    } catch { return null } // A saved choice may expire while this screen remains open.
  })()
  const restart = useMutation({ mutationFn: () => {
    if (!nextIntent) throw new Error('No room intent change selected')
    return apiClient.replaceMyIntent(room.id, nextIntent)
  }, onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); navigate(`/rooms/${room.id}/waiting`, { replace: true }) }, onError: async (error) => { if (error instanceof ApiError && error.status === 409) await queryClient.invalidateQueries({ queryKey: ['room', room.id] }) } })
  if (!room.allowed_actions.includes('restart_with_new_intent')) {
    return (
      <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
        <p className={styles.eyebrow}>{room.round_no >= 3 ? 'ТРЕТИЙ РАУНД ЗАВЕРШЁН' : 'ПУЛ ЗАКОНЧИЛСЯ'}</p><h1 className={styles.title}>Раунды этой комнаты завершены</h1><p className={styles.subtitle}>{room.round_no >= 3 ? 'Третий раунд завершён. Создайте новую комнату, когда появятся другие планы.' : 'Для этой комнаты больше нельзя запускать новый раунд. Создайте новую комнату, когда появятся другие планы.'}</p>
        <PrivacyNote>Предпочтения второго участника по-прежнему не раскрываются.</PrivacyNote>
        <div className={`${styles.footer} ${reliabilityStyles.actions}`}><Button onClick={() => navigate('/')}>На главную</Button><Button tone="secondary" onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button></div>
      </PageContent></PageShell>
    )
  }
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>РАУНД ЗАВЕРШЁН</p><h1 className={styles.title}>{zeroPool ? 'Нет событий по вашим условиям' : room.pool && room.pool.total > 0 ? 'Вы пока не выбрали одно событие' : 'Подборка завершена'}</h1><p className={styles.subtitle}>{zeroPool ? 'В этом раунде сервер не нашёл событий по вашим условиям.' : room.pool && room.pool.total > 0 ? 'Вы оба завершили выбор, но общего лайка пока нет.' : 'Можно пересмотреть собственные условия.'} Изменения не гарантируют больше событий.</p><p className={styles.subtitle}>Вы меняете только свои пожелания. Каждый участник самостоятельно пересматривает и подтверждает свои условия.</p>
      {!review ? <section className={styles.section}><h2 className={styles.sectionTitle}>Что изменить в подборке?</h2>{suggestions.map((item) => <button type="button" key={item.id} className={`${styles.suggestion} ${selectedSuggestion?.id === item.id ? styles.suggestionSelected : ''}`} aria-pressed={selected === item.id} onClick={() => { setSelected(item.id); setSelectedDate(null); setSelectedCategory(null); setSelectedBudget(null) }}><span><strong>{item.title}</strong>{item.id === 'budget' ? <small>Вы сами выберете новый лимит</small> : item.id === 'category' ? <small>Изменит порядок рекомендаций, но не гарантирует больше событий</small> : item.id === 'time' ? <small>Добавим утро, день и ночь</small> : <small>Вы сами выберете конкретный день</small>}</span><span aria-hidden="true">{selected === item.id ? '✓' : '○'}</span></button>)}
        {selectedSuggestion?.id === 'date' ? <div className={styles.dateChoices}><h3>Выберите дату</h3><ChipGroup>{selectedSuggestion.dates?.map((date) => <Chip key={date} selected={selectedDate === date} onClick={() => setSelectedDate(date)}>{formatRoomDate(date)}</Chip>)}</ChipGroup></div> : null}
        {selectedSuggestion?.id === 'category' ? <div className={styles.dateChoices}><h3>Выберите категорию</h3><ChipGroup>{selectedSuggestion.categories?.map(({ slug, label }) => <Chip key={slug} selected={selectedCategory === slug} onClick={() => setSelectedCategory(slug)}>{label}</Chip>)}</ChipGroup></div> : null}
        {selectedSuggestion?.id === 'budget' ? <div className={styles.dateChoices}><h3>Новый лимит стоимости</h3><p className={styles.subtitle}>Сейчас до {((room.myIntent?.budget_max_minor ?? 0) / 100).toLocaleString('ru')} ₽. Выберите сумму, до которой готовы потратить.</p><div className={styles.recoveryChoiceGrid}>{selectedSuggestion.budgets?.map((amount) => <Chip key={amount} selected={selectedBudget === amount} onClick={() => setSelectedBudget(amount)}>до {amount.toLocaleString('ru')} ₽</Chip>)}</div></div> : null}
        {suggestions.length === 0 ? <p className={styles.subtitle}>{!timezone ? 'Не удалось определить часовой пояс города. Вернитесь на главную или создайте новую комнату с выбранным городом.' : 'Больше нечего изменить в текущих условиях. Попробуйте позже или создайте новую комнату.'}</p> : null}
      </section> : <section className={styles.section}><h2 className={styles.sectionTitle}>Проверьте изменение</h2><p className={styles.subtitle}>{selectedSuggestion?.id === 'date' ? `Добавим дату: ${selectedDate ? formatRoomDate(selectedDate) : ''}` : selectedSuggestion?.id === 'category' ? `Добавим категорию: ${selectedSuggestion.categories?.find(({ slug }) => slug === selectedCategory)?.label ?? ''}` : selectedSuggestion?.id === 'budget' ? `Лимит стоимости увеличится до ${selectedBudget?.toLocaleString('ru') ?? ''} ₽.` : 'Можно будет выбрать событие в любое время суток.'}</p><p>Вы подтвердите только свои условия. Для подбора нужно независимое подтверждение каждого участника.</p></section>}
      {restart.isError ? <AuthRecoveryAction error={restart.error} /> : null}{restart.isError ? <p className={styles.error} role="alert">{roomErrorMessage(restart.error, 'Не удалось обновить подборку.')}</p> : null}
      <div className={`${styles.footer} ${reliabilityStyles.actions}`}>{review ? <><Button disabled={restart.isPending || !nextIntent} state={restart.isPending ? 'loading' : 'idle'} loadingLabel="Сохраняем…" onClick={() => { if (!restart.isPending) restart.mutate() }}>Подтвердить мои условия</Button><Button tone="secondary" disabled={restart.isPending} onClick={() => setReview(false)}>Назад к изменениям</Button></> : <Button disabled={!nextIntent} onClick={() => { setReview(true); restart.reset() }}>Проверить изменение</Button>}<Button tone="ghost" onClick={() => navigate('/')}>На главную</Button><Button tone="secondary" disabled={restart.isPending} onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button></div>
    </PageContent></PageShell>
  )
}

function useInviteExpired(expiresAt: string | undefined, refetch: () => unknown) {
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

function AuthRecoveryAction({ error }: { error: unknown }) {
  return error instanceof ApiError && error.status === 401 ? <Button tone="secondary" onClick={() => window.location.reload()}>Восстановить вход в MAX</Button> : null
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
    <div className={styles.footer}><Button onClick={() => navigate('/')}>На главную</Button><Button tone="secondary" onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button></div>
  </PageContent></PageShell>
}

function ChoiceField<T extends string>({ title, options, selected, onToggle, error }: { title: string; options: Array<[T, string]>; selected: T[]; onToggle: (value: T) => void; error?: string }) {
  return <fieldset className={intentStyles.choiceField} aria-invalid={Boolean(error)}><legend>{title}</legend><ChipGroup className={intentStyles.choiceOptions}>{options.map(([value, label]) => <Chip key={value} selected={selected.includes(value)} onClick={() => onToggle(value)}>{label}</Chip>)}</ChipGroup>{error ? <span className={intentStyles.fieldError} role="alert">{error}</span> : null}</fieldset>
}

function expectedScreen(room: RoomSnapshot) {
  if (room.state === 'matched') return 'match'
  if (room.state === 'exhausted') return 'recovery'
  if (room.state === 'voting') return room.pool?.my_pool_finished || !room.allowed_actions.includes('vote') ? 'waiting' : 'vote'
  if (room.state === 'ranking') return 'waiting'
  return room.allowed_actions.includes('edit_intent') ? 'intent' : 'waiting'
}

function toIntent(values: IntentForm): RoomIntentRequestDto {
  return { dates: values.dates, day_types: values.day_types, time_slots: values.time_slots, category_slugs: values.category_slugs, budget_max_minor: Math.round(values.budget * 100), radius_m: null, exclusion_slugs: [], location: null, free_text: values.free_text.trim() || null }
}

function toggleValue<T>(items: T[], value: T) { return items.includes(value) ? items.filter((item) => item !== value) : [...items, value] }

function addDays(date: Date, amount: number) { const next = new Date(date); next.setDate(next.getDate() + amount); return next }
function localDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function formatRoomDate(date: string) { return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', weekday: 'long' }).format(new Date(`${date}T12:00:00`)) }
function upcomingDates(count: number): Array<[string, string]> { return Array.from({ length: count }, (_, index) => { const date = addDays(new Date(), index); return [localDate(date), new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', weekday: 'short' }).format(date)] }) }

function upcomingCityDates(count: number, timezone: string): Array<[string, string]> {
  const today = cityToday(timezone)
  return Array.from({ length: count }, (_, index) => {
    const date = new Date(`${today}T12:00:00Z`)
    date.setUTCDate(date.getUTCDate() + index)
    const value = date.toISOString().slice(0, 10)
    return [value, formatRoomDate(value)]
  })
}

function intentDefaults(room: RoomSnapshot, fallbackDate: string): IntentForm {
  const intent = room.myIntent
  return intent ? {
    dates: [...intent.dates],
    day_types: [...intent.day_types],
    time_slots: [...intent.time_slots],
    category_slugs: [...intent.category_slugs],
    budget: intent.budget_max_minor / 100,
    free_text: intent.free_text ?? '',
  } : room.public_plan ? { dates: [...room.public_plan.dates], day_types: [], time_slots: [...room.public_plan.time_slots], category_slugs: [...room.public_plan.category_slugs], budget: room.public_plan.budget_max_minor / 100, free_text: '' } : { dates: [fallbackDate], day_types: [], time_slots: ['evening'], category_slugs: ['concerts', 'standup'], budget: 3_000, free_text: '' }
}

function maxAppUrl(token: string) {
  const base = import.meta.env.VITE_MAX_APP_URL ?? 'https://max.ru'
  return `${base}${base.includes('?') ? '&' : '?'}startapp=${encodeURIComponent(token)}`
}
