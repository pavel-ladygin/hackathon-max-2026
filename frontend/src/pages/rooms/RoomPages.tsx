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
import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, Chip, ChipGroup, Empty, EventImage, PageContent, PageShell, PrivacyNote, ScreenSkeleton, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'
import intentStyles from './intent.module.css'
import { track, trackFailure } from '../../shared/analytics/client'

const MatchCelebration = lazy(() => import('../../features/rooms/MatchCelebration').then((module) => ({ default: module.MatchCelebration })))
const MOSCOW_CITY_ID = 'a0f625ee-2154-5a45-8afe-37adf955ec24'

const roomSchema = z.object({ name: z.string().trim().min(1, 'Введите название комнаты').max(80, 'Не больше 80 символов') })
type RoomForm = z.infer<typeof roomSchema>

export function NewRoomPage() {
  const navigate = useNavigate()
  const reducedMotion = useReducedMotion()
  const bootstrap = useBootstrap()
  const [createSuccess, setCreateSuccess] = useState(false)
  const form = useForm<RoomForm>({ resolver: zodResolver(roomSchema), defaultValues: { name: 'Куда идём в субботу?' } })
  useEffect(() => { track('room_creation_started') }, [])
  const create = useMutation({
    mutationFn: (values: RoomForm) => apiClient.createRoom({ name: values.name.trim(), city_id: bootstrap.data?.preferences?.cityId ?? bootstrap.data?.user.cityId ?? MOSCOW_CITY_ID }),
    onSuccess: async ({ room }) => { setCreateSuccess(true); await new Promise<void>((resolve) => window.setTimeout(resolve, reducedMotion ? 0 : 350)); navigate(`/rooms/${room.id}/invite`) },
    onError: (error) => {
      trackFailure('room_creation_failed', error)
      if (error instanceof ApiError && error.fieldErrors.name) form.setError('name', { message: error.fieldErrors.name }, { shouldFocus: true })
    },
  })
  return (
    <PageShell><TopBar title="Новая комната" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ВМЕСТЕ ЛЕГЧЕ ВЫБРАТЬ</p><h1 className={styles.title}>Создать комнату</h1><p className={styles.subtitle}>Пригласите друга. Каждый отдельно укажет пожелания, а система найдёт честное пересечение.</p>
      <form className={styles.formStack} onSubmit={form.handleSubmit((values) => create.mutate(values))}>
        <label className={styles.fieldLabel} htmlFor="room-name">Название комнаты<input id="room-name" className={`${styles.input} ${styles.roomNameInput}`} aria-invalid={Boolean(form.formState.errors.name)} {...form.register('name')} />{form.formState.errors.name ? <span className={styles.error} role="alert">{form.formState.errors.name.message}</span> : null}</label>
        <div className={styles.avatars} aria-label="Комната для двух участников"><span className={styles.avatar}>И</span><span className={styles.avatar}>+</span></div>
        <PrivacyNote>Сначала вы пригласите друга, затем каждый приватно заполнит свои условия.</PrivacyNote>
        {create.isError ? <p className={styles.error} role="alert">{roomErrorMessage(create.error, 'Не удалось создать комнату.')}</p> : null}
        <div className={styles.footer}><Button type="submit" disabled={create.isPending || createSuccess} state={createSuccess ? 'success' : create.isPending ? 'loading' : 'idle'} loadingLabel="Создаём…" successLabel="Комната создана">Создать комнату</Button></div>
      </form>
    </PageContent></PageShell>
  )
}

export function InvitePage() {
  const { roomId } = useParams()
  const navigate = useNavigate()
  const room = useRoom(roomId)
  const loadedRoomId = room.data?.id
  useEffect(() => { if (loadedRoomId) track('invite_opened', { roomId: loadedRoomId }) }, [loadedRoomId])
  const [inviteStatus, setInviteStatus] = useState<'idle' | 'sharing' | 'shared' | 'share-error' | 'copied' | 'copy-error'>('idle')
  if (room.isPending) return <ScreenSkeleton variant="room" label="Готовим приглашение…" />
  if (room.isError || !room.data) return <Empty title="Комната не найдена" description={room.isError ? roomErrorMessage(room.error) : undefined} action={<Button onClick={() => navigate('/')}>На главную</Button>} />
  const rawUrl = room.data.invite?.url ?? ''
  const maxLink = room.data.invite?.max_deep_link ?? rawUrl
  const share = async () => {
    if (!maxLink || inviteStatus === 'sharing') return
    setInviteStatus('sharing')
    const ok = await maxPlatform.shareInvite({ text: `Присоединяйся к комнате «${room.data.name}»`, link: maxLink })
    setInviteStatus(ok ? 'shared' : 'share-error')
    if (ok) track('invite_shared', { roomId: room.data.id })
    else trackFailure('invite_share_failed', new Error('Invite sharing failed'), { roomId: room.data.id })
  }
  const copy = async () => {
    if (!rawUrl) return
    const ok = await maxPlatform.copyText(rawUrl)
    setInviteStatus(ok ? 'copied' : 'copy-error')
  }
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>И</span><span className={styles.avatar}>{room.data.participants.length > 1 ? 'А' : '+'}</span></div>
      <h1 className={styles.title}>Пригласите друга</h1><p className={styles.subtitle}>Отправьте приглашение в MAX. После подключения каждый отдельно заполнит пожелания.</p>
      <div className={styles.inviteLink} data-testid="invite-url">{rawUrl || 'Ссылка появится после создания комнаты'}</div>
      <PrivacyNote>Предпочтения и оценки останутся скрытыми до взаимного лайка.</PrivacyNote>
      <div aria-live="polite">{inviteStatus === 'shared' ? <p>✓ Приглашение отправлено</p> : inviteStatus === 'copied' ? <p>✓ Ссылка скопирована</p> : inviteStatus === 'share-error' ? <p className={styles.error} role="alert">Не удалось отправить приглашение. Попробуйте ещё раз или скопируйте ссылку.</p> : inviteStatus === 'copy-error' ? <p className={styles.error} role="alert">Не удалось скопировать ссылку. Вы можете скопировать её вручную выше.</p> : null}</div>
      <div className={styles.footer}><Button disabled={!maxLink || inviteStatus === 'sharing'} state={inviteStatus === 'sharing' ? 'loading' : 'idle'} loadingLabel="Открываем MAX…" onClick={() => void share()}>Пригласить через MAX</Button><Button tone="secondary" disabled={!rawUrl || inviteStatus === 'sharing'} onClick={() => void copy()}>Скопировать ссылку</Button><Button tone="ghost" onClick={() => navigate(`/rooms/${roomId}/intent`)}>Перейти к моим пожеланиям</Button></div>
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
  useEffect(() => { if (inviteStatus) track('invite_link_opened', { roomId: context?.room_id }) }, [context?.room_id, inviteStatus])
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
  }, onError: (error) => trackFailure('room_join_failed', error, { roomId: context?.room_id }) })
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>{context?.inviter.display_name.slice(0, 1) ?? 'И'}</span><span className={styles.avatar}>+</span></div>
      <p className={styles.eyebrow}>СОВМЕСТНЫЙ ВЫБОР</p><h1 className={styles.title}>{context ? `Вас приглашает ${context.inviter.display_name}` : 'Вас пригласили выбрать, куда сходить'}</h1><p className={styles.subtitle}>{context ? `Комната «${context.room_name}». ` : ''}После подключения каждый самостоятельно укажет свои условия.</p>
      <PrivacyNote />{join.isError ? <p className={styles.error} role="alert">{roomErrorMessage(join.error, 'Ссылка недействительна или комната уже заполнена.')}</p> : null}
      {context?.status === 'expired' ? <p className={styles.error} role="alert">Срок приглашения истёк.</p> : context?.status === 'full' ? <p className={styles.error} role="alert">Комната уже заполнена.</p> : null}
      <div className={styles.footer}><Button disabled={join.isPending || context?.status === 'expired' || context?.status === 'full'} state={join.isPending ? 'loading' : 'idle'} loadingLabel="Подключаем…" onClick={() => { track('room_join_started', { roomId: context?.room_id }); join.mutate() }}>{context?.already_joined ? 'Продолжить в комнате' : 'Присоединиться'}</Button>{!maxPlatform.isMax && inviteToken ? <Button tone="secondary" onClick={() => void maxPlatform.openMaxLink(maxAppUrl(inviteToken))}>Открыть в MAX</Button> : null}</div>
    </PageContent></PageShell>
  )
}

export function RoomFlowPage() {
  const { roomId, roomScreen } = useParams()
  const room = useRoom(roomId)
  const loadedRoomId = room.data?.id
  useEffect(() => { if (loadedRoomId) track('room_opened', { roomId: loadedRoomId }) }, [loadedRoomId])
  if (room.isPending) return <ScreenSkeleton variant="room" label="Восстанавливаем комнату…" />
  if (room.isError || !room.data) return <Empty title="Комната недоступна" description={room.isError ? roomErrorMessage(room.error) : 'Возможно, приглашение истекло.'} />
  const expected = expectedScreen(room.data)
  if (room.data.state === 'closed') return <ClosedScreen room={room.data} />
  if (roomScreen !== expected) return <Navigate to={`/rooms/${roomId}/${expected}`} replace />
  if (expected === 'intent') return <IntentScreen room={room.data} />
  if (expected === 'waiting') return <WaitingScreen room={room.data} />
  if (expected === 'vote') return <VoteScreen room={room.data} />
  if (expected === 'match') return <MatchScreen room={room.data} />
  return <RecoveryScreen room={room.data} />
}

const intentSchema = z.object({
  dates: z.array(z.string()).min(1, 'Выберите хотя бы одну дату').max(14).refine((dates) => new Set(dates).size === dates.length && dates.every((date) => date >= localDate(new Date())), 'Выберите будущие даты без повторов'),
  day_types: z.array(z.enum(['weekday', 'weekend'])),
  time_slots: z.array(z.enum(['morning', 'day', 'evening', 'night'])),
  category_slugs: z.array(z.enum(['concerts', 'cinema', 'theatre', 'standup', 'exhibitions', 'sports', 'food', 'parties', 'festivals', 'walks', 'other'])).min(1),
  budget: z.coerce.number().min(0).max(1_000_000),
  free_text: z.string().max(300),
})
type IntentForm = z.infer<typeof intentSchema>

function IntentScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const dateChoices = upcomingDates(7)
  const form = useForm<IntentForm>({ resolver: zodResolver(intentSchema), defaultValues: intentDefaults(room, dateChoices[0][0]) })
  const dates = useWatch({ control: form.control, name: 'dates' })
  const dayTypes = useWatch({ control: form.control, name: 'day_types' })
  const timeSlots = useWatch({ control: form.control, name: 'time_slots' })
  const categories = useWatch({ control: form.control, name: 'category_slugs' })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const save = useMutation({
    mutationFn: (values: IntentForm) => apiClient.replaceMyIntent(room.id, toIntent(values)),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); navigate(`/rooms/${room.id}/waiting`, { replace: true }) },
    onError: (error) => {
      if (!(error instanceof ApiError)) return
      const mapping: Record<string, keyof IntentForm> = { dates: 'dates', day_types: 'day_types', time_slots: 'time_slots', category_slugs: 'category_slugs', budget_max_minor: 'budget', free_text: 'free_text' }
      for (const [field, message] of Object.entries(error.fieldErrors)) if (mapping[field]) form.setError(mapping[field], { message })
    },
  })
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${intentStyles.intentPage}`}>
      <p className={`${styles.eyebrow} ${intentStyles.introEyebrow}`}>ВАШИ УСЛОВИЯ ДЛЯ ЭТОЙ ВСТРЕЧИ</p><h1 className={`${styles.title} ${intentStyles.introTitle}`}>Мои предпочтения</h1><p className={`${styles.subtitle} ${intentStyles.introSubtitle}`}>Укажите, что подходит именно сейчас. Другой участник не увидит ответы.</p><PrivacyNote />
      <form className={`${styles.formStack} ${intentStyles.intentForm}`} onSubmit={form.handleSubmit((values) => save.mutate(values))}>
        <ChoiceField title="Когда удобно" error={form.formState.errors.dates?.message} options={dateChoices} selected={dates} onToggle={(value) => form.setValue('dates', toggleValue(dates, value), { shouldValidate: true })} />
        <ChoiceField title="Тип дня (необязательно)" error={form.formState.errors.day_types?.message} options={[['weekday', 'Будни'], ['weekend', 'Выходные']]} selected={dayTypes} onToggle={(value) => form.setValue('day_types', toggleValue(dayTypes, value as IntentForm['day_types'][number]), { shouldValidate: true })} />
        <ChoiceField title="Время" error={form.formState.errors.time_slots?.message} options={[['morning', 'Утро'], ['day', 'День'], ['evening', 'Вечер'], ['night', 'Ночь']]} selected={timeSlots} onToggle={(value) => form.setValue('time_slots', toggleValue(timeSlots, value as IntentForm['time_slots'][number]), { shouldValidate: true })} />
        <ChoiceField title="Что интересно" error={form.formState.errors.category_slugs?.message} options={[['concerts', 'Концерты'], ['theatre', 'Театр'], ['standup', 'Стендап'], ['exhibitions', 'Выставки'], ['cinema', 'Кино'], ['food', 'Еда']]} selected={categories} onToggle={(value) => form.setValue('category_slugs', toggleValue(categories, value as CategorySlug), { shouldValidate: true })} />
        <label className={intentStyles.rangeField} htmlFor="room-budget">Бюджет · до {budget.toLocaleString('ru')} ₽<input id="room-budget" className={styles.range} type="range" min="0" max="10000" step="500" aria-invalid={Boolean(form.formState.errors.budget)} {...form.register('budget')} />{form.formState.errors.budget ? <span className={intentStyles.fieldError} role="alert">{form.formState.errors.budget.message}</span> : null}</label>
        <label className={intentStyles.textField} htmlFor="room-free-text">Дополнительное пожелание<textarea id="room-free-text" className={styles.textarea} placeholder="Например: хочется спокойного места" aria-invalid={Boolean(form.formState.errors.free_text)} maxLength={300} {...form.register('free_text')} />{form.formState.errors.free_text ? <span className={intentStyles.fieldError} role="alert">{form.formState.errors.free_text.message}</span> : null}</label>
        {Object.keys(form.formState.errors).length ? <p className={`${styles.error} ${intentStyles.formErrors}`} role="alert">Проверьте выбранные даты, время и интересы.</p> : null}
        {save.isError ? <p className={`${styles.error} ${intentStyles.formErrors}`} role="alert">{roomErrorMessage(save.error, 'Не удалось сохранить предпочтения.')}</p> : null}
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
      <p className={styles.eyebrow}>ОБЩАЯ ПОДБОРКА</p><h1 className={styles.title}>{room.state === 'ranking' ? 'Формируем общий пул…' : 'Ждём второго участника'}</h1><p className={styles.subtitle}>Ваши пожелания сохранены. Подборка появится, когда оба участника будут готовы.</p>
      <div className={styles.statusList} aria-live="polite"><AnimatePresence initial={false}>{room.participants.map((participant) => <motion.div className={styles.statusRow} key={participant.id} layout={!reducedMotion} initial={reducedMotion ? false : { opacity: 0, y: 8, scale: .92 }} animate={{ opacity: 1, y: 0, scale: 1 }} exit={reducedMotion ? undefined : { opacity: 0, y: -8, scale: .92 }} transition={{ duration: reducedMotion ? 0 : .24 }}><span className={styles.smallAvatar}>{participantInitials(participant.displayName)}</span><span><strong>{participant.displayName}</strong><small>{participant.intentReady ? '✓ Предпочтения заполнены' : 'Заполняет предпочтения…'}</small></span></motion.div>)}</AnimatePresence></div>
      <p aria-live="polite" style={{ minHeight: 20, textAlign: 'center', color: 'var(--color-muted)' }}>{room.participants.length} / 2 участника</p>
      <div className={styles.explain}><strong>Как формируется подборка</strong><span>✓ Учитываем постоянные интересы</span><span>✓ Добавляем текущие условия</span><span>{room.participants.length === 2 ? '○ Ждём готовности обоих' : '○ Ждём присоединения друга'}</span></div><PrivacyNote />
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
    onError: async (error) => { dragX.set(0); if (isRoomError(error, 'STALE_POOL_VERSION') || isRoomError(error, 'VOTE_ALREADY_CAST') || isRoomError(error, 'ALREADY_MATCHED')) { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); await queryClient.invalidateQueries({ queryKey: ['room-events', room.id] }) } },
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
    return <Empty title={isRoomError(events.error, 'POOL_NOT_READY') ? 'Пул пока не готов' : 'Не удалось загрузить подборку'} description={roomErrorMessage(events.error)} action={<Button onClick={() => void events.refetch()}>Повторить</Button>} />
  }
  const votedByMe = room.pool?.voted_by_me ?? 0
  const poolTotal = room.pool?.total ?? events.data.total
  if (!item) return <WaitingScreen room={room} />
  const cast = (value: VoteValue) => { if (!vote.isPending) vote.mutate({ eventId: item.event.id, value }) }
  return (
    <PageShell><TopBar title="Совместный выбор" onBack={() => navigate('/')} right={<span>{Math.min(votedByMe + 1, poolTotal)} / {poolTotal}</span>} /><PageContent className={styles.poolWrap}>
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
      {vote.isError ? <p className={styles.error} role="alert">{roomErrorMessage(vote.error, 'Голос не сохранился. Повторите действие.')}</p> : null}
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
  if (event.isError) return <Empty title="Мэтч найден, но событие не загрузилось" />
  return (
    <PageShell><TopBar title="Совпадение" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <Suspense fallback={<ScreenSkeleton variant="event" inline label="Готовим сюрприз…" />}><MatchCelebration event={event.data} participants={room.match?.participants.map((participant) => ({ id: participant.id, displayName: participant.display_name, avatarUrl: participant.avatar_url, role: participant.role, intentReady: participant.intent_ready }))} /></Suspense>
      <div className={styles.footer}><Button onClick={() => { track('match_opened', { roomId: room.id, eventId: event.data.id }); navigate(`/events/${event.data.id}`) }}>Открыть событие</Button><Button tone="secondary" disabled={ticket.isPending} state={ticket.isPending ? 'loading' : 'idle'} loadingLabel="Открываем…" onClick={() => ticket.mutate()}>К билетам</Button></div>
      {ticket.isError ? <p className={styles.error} role="alert">{roomErrorMessage(ticket.error, 'Не удалось открыть билетный сервис. Повторите попытку.')}</p> : null}
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
  const suggestions = availableRelaxations(room.myIntent)
  const selectedSuggestion = suggestions.find((item) => item.id === selected)
  const nextIntent = selectedSuggestion?.id === 'date' && selectedDate ? relaxedIntent(room.myIntent, 'date', new Date(), selectedDate)
    : selectedSuggestion?.id === 'category' && selectedCategory ? relaxedIntent(room.myIntent, 'category', new Date(), selectedCategory)
    : selectedSuggestion?.id === 'budget' && selectedBudget !== null ? relaxedIntent(room.myIntent, 'budget', new Date(), selectedBudget)
    : selectedSuggestion?.intent ?? null
  const restart = useMutation({ mutationFn: () => {
    if (!nextIntent) throw new Error('No room intent change selected')
    return apiClient.replaceMyIntent(room.id, nextIntent)
  }, onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); navigate(`/rooms/${room.id}/waiting`, { replace: true }) } })
  if (!room.allowed_actions.includes('restart_with_new_intent')) {
    return (
      <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
        <p className={styles.eyebrow}>{room.round_no >= 3 ? 'ТРЕТИЙ РАУНД ЗАВЕРШЁН' : 'ПУЛ ЗАКОНЧИЛСЯ'}</p><h1 className={styles.title}>На сегодня вариантов больше нет</h1><p className={styles.subtitle}>{room.round_no >= 3 ? 'Мы уже расширили условия в трёх приватных раундах. Создайте новую комнату, когда появятся другие планы.' : 'Для этой комнаты больше нельзя запускать новый раунд. Создайте новую комнату, когда появятся другие планы.'}</p>
        <PrivacyNote>Предпочтения второго участника по-прежнему не раскрываются.</PrivacyNote>
        <div className={styles.footer}><Button onClick={() => navigate('/')}>На главную</Button><Button tone="secondary" onClick={() => navigate('/rooms/new')}>Создать новую комнату</Button></div>
      </PageContent></PageShell>
    )
  }
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ПУЛ ЗАКОНЧИЛСЯ</p><h1 className={styles.title}>Пока не совпали</h1><p className={styles.subtitle}>Сейчас нет событий по выбранным условиям. Можно изменить свои условия и запустить новый приватный раунд.</p>
      {!review ? <section className={styles.section}><h2 className={styles.sectionTitle}>Что изменить в подборке?</h2>{suggestions.map((item) => <button type="button" key={item.id} className={`${styles.suggestion} ${selectedSuggestion?.id === item.id ? styles.suggestionSelected : ''}`} aria-pressed={selected === item.id} onClick={() => { setSelected(item.id); setSelectedDate(null); setSelectedCategory(null); setSelectedBudget(null) }}><span><strong>{item.title}</strong>{item.id === 'budget' ? <small>Вы сами выберете новый лимит</small> : item.id === 'category' ? <small>Вы сами выберете новую категорию</small> : item.id === 'time' ? <small>Добавим утро, день и ночь</small> : <small>Вы сами выберете конкретный день</small>}</span><span aria-hidden="true">{selected === item.id ? '✓' : '○'}</span></button>)}
        {selectedSuggestion?.id === 'date' ? <div className={styles.dateChoices}><h3>Выберите дату</h3><ChipGroup>{selectedSuggestion.dates?.map((date) => <Chip key={date} selected={selectedDate === date} onClick={() => setSelectedDate(date)}>{formatRoomDate(date)}</Chip>)}</ChipGroup></div> : null}
        {selectedSuggestion?.id === 'category' ? <div className={styles.dateChoices}><h3>Выберите категорию</h3><ChipGroup>{selectedSuggestion.categories?.map(({ slug, label }) => <Chip key={slug} selected={selectedCategory === slug} onClick={() => setSelectedCategory(slug)}>{label}</Chip>)}</ChipGroup></div> : null}
        {selectedSuggestion?.id === 'budget' ? <div className={styles.dateChoices}><h3>Новый лимит стоимости</h3><p className={styles.subtitle}>Сейчас до {((room.myIntent?.budget_max_minor ?? 0) / 100).toLocaleString('ru')} ₽. Выберите сумму, до которой готовы потратить.</p><div className={styles.recoveryChoiceGrid}>{selectedSuggestion.budgets?.map((amount) => <Chip key={amount} selected={selectedBudget === amount} onClick={() => setSelectedBudget(amount)}>до {amount.toLocaleString('ru')} ₽</Chip>)}</div></div> : null}
        {suggestions.length === 0 ? <p className={styles.subtitle}>Больше нечего изменить в текущих условиях. Попробуйте позже или создайте новую комнату.</p> : null}
      </section> : <section className={styles.section}><h2 className={styles.sectionTitle}>Проверьте изменение</h2><p className={styles.subtitle}>{selectedSuggestion?.id === 'date' ? `Добавим дату: ${selectedDate ? formatRoomDate(selectedDate) : ''}` : selectedSuggestion?.id === 'category' ? `Добавим категорию: ${selectedSuggestion.categories?.find(({ slug }) => slug === selectedCategory)?.label ?? ''}` : selectedSuggestion?.id === 'budget' ? `Лимит стоимости увеличится до ${selectedBudget?.toLocaleString('ru') ?? ''} ₽.` : 'Можно будет выбрать событие в любое время суток.'}</p><p>После подтверждения начнётся новый раунд подбора.</p></section>}
      {restart.isError ? <p className={styles.error} role="alert">{roomErrorMessage(restart.error, 'Не удалось обновить подборку.')}</p> : null}
      <div className={styles.footer}>{review ? <><Button disabled={restart.isPending || !nextIntent} state={restart.isPending ? 'loading' : 'idle'} loadingLabel="Обновляем…" onClick={() => restart.mutate()}>Подтвердить и обновить</Button><Button tone="secondary" disabled={restart.isPending} onClick={() => setReview(false)}>Назад к изменениям</Button></> : <Button disabled={!nextIntent} onClick={() => { setReview(true); restart.reset() }}>Проверить изменение</Button>}</div>
    </PageContent></PageShell>
  )
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
  return { dates: values.dates, day_types: values.day_types, time_slots: values.time_slots, category_slugs: values.category_slugs, budget_max_minor: values.budget * 100, radius_m: null, exclusion_slugs: [], location: null, free_text: values.free_text.trim() || null }
}

function toggleValue<T>(items: T[], value: T) { return items.includes(value) ? items.filter((item) => item !== value) : [...items, value] }

function addDays(date: Date, amount: number) { const next = new Date(date); next.setDate(next.getDate() + amount); return next }
function localDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function formatRoomDate(date: string) { return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', weekday: 'long' }).format(new Date(`${date}T12:00:00`)) }
function upcomingDates(count: number): Array<[string, string]> { return Array.from({ length: count }, (_, index) => { const date = addDays(new Date(), index); return [localDate(date), new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', weekday: 'short' }).format(date)] }) }

function intentDefaults(room: RoomSnapshot, fallbackDate: string): IntentForm {
  const intent = room.myIntent
  return intent ? {
    dates: intent.dates,
    day_types: intent.day_types,
    time_slots: intent.time_slots,
    category_slugs: intent.category_slugs,
    budget: Math.round(intent.budget_max_minor / 100),
    free_text: intent.free_text ?? '',
  } : { dates: [fallbackDate], day_types: [], time_slots: ['evening'], category_slugs: ['concerts', 'standup'], budget: 3_000, free_text: '' }
}

function maxAppUrl(token: string) {
  const base = import.meta.env.VITE_MAX_APP_URL ?? 'https://max.ru'
  return `${base}${base.includes('?') ? '&' : '?'}startapp=${encodeURIComponent(token)}`
}
