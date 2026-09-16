import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'motion/react'
import { lazy, Suspense, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { Navigate, useNavigate, useParams } from 'react-router-dom'
import { z } from 'zod'
import { useEventDetail } from '../../features/discovery/queries'
import { useRoom, useRoomEvents } from '../../features/rooms/queries'
import { apiClient } from '../../shared/api/client'
import type { CategorySlug, RoomIntentRequestDto, RoomSnapshot, VoteValue } from '../../shared/api/types'
import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, Chip, ChipGroup, Empty, Loading, PageContent, PageShell, PrivacyNote, TopBar } from '../../shared/ui/index'
import styles from '../pages.module.css'

const MatchCelebration = lazy(() => import('../../features/rooms/MatchCelebration').then((module) => ({ default: module.MatchCelebration })))
const CITY_ID = 'aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa'

const roomSchema = z.object({ name: z.string().trim().min(2, 'Введите название комнаты').max(80) })
type RoomForm = z.infer<typeof roomSchema>

export function NewRoomPage() {
  const navigate = useNavigate()
  const form = useForm<RoomForm>({ resolver: zodResolver(roomSchema), defaultValues: { name: 'Куда идём в субботу?' } })
  const create = useMutation({
    mutationFn: (values: RoomForm) => apiClient.createRoom({ name: values.name, city_id: CITY_ID }),
    onSuccess: ({ room }) => navigate(mockRoute(`/rooms/${room.id}/invite`)),
  })
  return (
    <PageShell><TopBar title="Новая комната" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ВМЕСТЕ ЛЕГЧЕ ВЫБРАТЬ</p><h1 className={styles.title}>Создать комнату</h1><p className={styles.subtitle}>Пригласите друга. Каждый отдельно укажет пожелания, а система найдёт честное пересечение.</p>
      <form className={styles.formStack} onSubmit={form.handleSubmit((values) => create.mutate(values))}>
        <label className={styles.fieldLabel}>Название комнаты<input className={styles.input} {...form.register('name')} />{form.formState.errors.name ? <span className={styles.error}>{form.formState.errors.name.message}</span> : null}</label>
        <div className={styles.avatars} aria-label="Комната для двух участников"><span className={styles.avatar}>И</span><span className={styles.avatar}>+</span></div>
        <PrivacyNote>Сначала вы пригласите друга, затем каждый приватно заполнит свои условия.</PrivacyNote>
        {create.isError ? <p className={styles.error}>Не удалось создать комнату.</p> : null}
        <div className={styles.footer}><Button type="submit" disabled={create.isPending}>{create.isPending ? 'Создаём…' : 'Создать комнату'}</Button></div>
      </form>
    </PageContent></PageShell>
  )
}

export function InvitePage() {
  const { roomId } = useParams()
  const navigate = useNavigate()
  const room = useRoom(roomId)
  const [shared, setShared] = useState(false)
  if (room.isPending) return <Loading label="Готовим приглашение…" />
  if (room.isError || !room.data) return <Empty title="Комната не найдена" action={<Button onClick={() => navigate('/')}>На главную</Button>} />
  const rawUrl = room.data.invite?.url ?? ''
  const demoUrl = mockInviteUrl(rawUrl)
  const share = async () => {
    const ok = await maxPlatform.shareInvite({ text: `Присоединяйся к комнате «${room.data.name}»`, link: demoUrl })
    setShared(ok)
  }
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>И</span><span className={styles.avatar}>{room.data.participants.length > 1 ? 'А' : '+'}</span></div>
      <h1 className={styles.title}>Пригласите друга</h1><p className={styles.subtitle}>Отправьте приглашение в MAX. После подключения каждый отдельно заполнит пожелания.</p>
      <div className={styles.inviteLink}>{demoUrl || 'Ссылка появится после создания комнаты'}</div>
      <PrivacyNote>Предпочтения и оценки останутся скрытыми до взаимного лайка.</PrivacyNote>
      {shared ? <p>✓ Приглашение отправлено или скопировано</p> : null}
      <div className={styles.footer}><Button onClick={() => void share()}>Пригласить через MAX</Button><Button tone="secondary" onClick={() => navigate(mockRoute(`/rooms/${roomId}/intent`))}>Перейти к моим пожеланиям</Button></div>
    </PageContent></PageShell>
  )
}

export function JoinPage() {
  const { inviteToken } = useParams()
  const navigate = useNavigate()
  const join = useMutation({ mutationFn: () => apiClient.joinRoom(inviteToken!), onSuccess: (room) => navigate(mockRoute(`/rooms/${room.id}/intent`), { replace: true }) })
  return (
    <PageShell><TopBar title="Приглашение" onBack={() => navigate('/')} /><PageContent className={`${styles.narrow} ${styles.center}`}>
      <div className={styles.avatars}><span className={styles.avatar}>И</span><span className={styles.avatar}>А</span></div>
      <p className={styles.eyebrow}>СОВМЕСТНЫЙ ВЫБОР</p><h1 className={styles.title}>Вас пригласили выбрать, куда сходить</h1><p className={styles.subtitle}>После подключения каждый самостоятельно укажет свои условия.</p>
      <PrivacyNote />{join.isError ? <p className={styles.error}>Ссылка недействительна или комната уже заполнена.</p> : null}
      <div className={styles.footer}><Button disabled={join.isPending} onClick={() => join.mutate()}>{join.isPending ? 'Подключаем…' : 'Присоединиться'}</Button></div>
    </PageContent></PageShell>
  )
}

export function RoomFlowPage() {
  const { roomId, roomScreen } = useParams()
  const room = useRoom(roomId)
  if (room.isPending) return <Loading label="Восстанавливаем комнату…" />
  if (room.isError || !room.data) return <Empty title="Комната недоступна" description="Возможно, приглашение истекло." />
  const expected = expectedScreen(room.data)
  if (roomScreen !== expected) return <Navigate to={mockRoute(`/rooms/${roomId}/${expected}`)} replace />
  if (expected === 'intent') return <IntentScreen room={room.data} />
  if (expected === 'waiting') return <WaitingScreen room={room.data} />
  if (expected === 'vote') return <VoteScreen room={room.data} />
  if (expected === 'match') return <MatchScreen room={room.data} />
  return <RecoveryScreen room={room.data} />
}

const intentSchema = z.object({
  dates: z.array(z.string()).min(1, 'Выберите хотя бы одну дату'),
  time_slots: z.array(z.enum(['morning', 'day', 'evening', 'night'])).min(1),
  category_slugs: z.array(z.enum(['concerts', 'cinema', 'theatre', 'standup', 'exhibitions', 'sports', 'food', 'parties', 'festivals', 'walks', 'other'])).min(1),
  budget: z.coerce.number().min(500).max(10_000),
  radius: z.coerce.number().min(1_000).max(10_000),
  free_text: z.string().max(300),
})
type IntentForm = z.infer<typeof intentSchema>

function IntentScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const form = useForm<IntentForm>({ resolver: zodResolver(intentSchema), defaultValues: { dates: ['2026-09-19'], time_slots: ['evening'], category_slugs: ['concerts', 'standup'], budget: 3_000, radius: 5_000, free_text: '' } })
  const dates = useWatch({ control: form.control, name: 'dates' })
  const timeSlots = useWatch({ control: form.control, name: 'time_slots' })
  const categories = useWatch({ control: form.control, name: 'category_slugs' })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const radius = useWatch({ control: form.control, name: 'radius' })
  const save = useMutation({
    mutationFn: (values: IntentForm) => apiClient.replaceMyIntent(room.id, toIntent(values)),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); navigate(mockRoute(`/rooms/${room.id}/waiting`), { replace: true }) },
  })
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ВАШИ УСЛОВИЯ ДЛЯ ЭТОЙ ВСТРЕЧИ</p><h1 className={styles.title}>Мои предпочтения</h1><p className={styles.subtitle}>Укажите, что подходит именно сейчас. Другой участник не увидит ответы.</p><PrivacyNote />
      <form className={styles.formStack} onSubmit={form.handleSubmit((values) => save.mutate(values))}>
        <ChoiceField title="Когда удобно" options={[['2026-09-19', '19 сентября'], ['2026-09-20', '20 сентября'], ['2026-09-21', '21 сентября']]} selected={dates} onToggle={(value) => form.setValue('dates', toggleValue(dates, value))} />
        <ChoiceField title="Время" options={[['morning', 'Утро'], ['day', 'День'], ['evening', 'Вечер'], ['night', 'Ночь']]} selected={timeSlots} onToggle={(value) => form.setValue('time_slots', toggleValue(timeSlots, value as IntentForm['time_slots'][number]))} />
        <ChoiceField title="Что интересно" options={[['concerts', 'Концерты'], ['theatre', 'Театр'], ['standup', 'Стендап'], ['exhibitions', 'Выставки'], ['cinema', 'Кино'], ['food', 'Еда']]} selected={categories} onToggle={(value) => form.setValue('category_slugs', toggleValue(categories, value as CategorySlug))} />
        <label className={styles.fieldLabel}>Бюджет · до {budget.toLocaleString('ru')} ₽<input className={styles.range} type="range" min="500" max="10000" step="500" {...form.register('budget')} /></label>
        <label className={styles.fieldLabel}>Радиус · до {(radius / 1000).toFixed(0)} км<input className={styles.range} type="range" min="1000" max="10000" step="1000" {...form.register('radius')} /></label>
        <label className={styles.fieldLabel}>Дополнительное пожелание<textarea className={styles.textarea} placeholder="Например: хочется спокойного места" {...form.register('free_text')} /></label>
        {Object.keys(form.formState.errors).length ? <p className={styles.error}>Проверьте выбранные даты, время и интересы.</p> : null}
        {save.isError ? <p className={styles.error}>Не удалось сохранить предпочтения.</p> : null}
        <div className={styles.footer}><Button type="submit" disabled={save.isPending}>{save.isPending ? 'Сохраняем приватно…' : 'Сохранить предпочтения'}</Button></div>
      </form>
    </PageContent></PageShell>
  )
}

function WaitingScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ОБЩАЯ ПОДБОРКА</p><h1 className={styles.title}>{room.state === 'ranking' ? 'Формируем общий пул…' : 'Ждём второго участника'}</h1><p className={styles.subtitle}>Ваши пожелания сохранены. Подборка появится, когда оба участника будут готовы.</p>
      <div className={styles.statusList}>{room.participants.map((participant) => <div className={styles.statusRow} key={participant.id}><span className={styles.smallAvatar}>{participant.displayName.slice(0, 1)}</span><span><strong>{participant.displayName}</strong><small>{participant.intentReady ? '✓ Предпочтения заполнены' : 'Заполняет предпочтения…'}</small></span></div>)}</div>
      <div className={styles.explain}><strong>Как формируется подборка</strong><span>✓ Учитываем постоянные интересы</span><span>✓ Добавляем текущие условия</span><span>{room.participants.length === 2 ? '○ Ждём готовности обоих' : '○ Ждём присоединения друга'}</span></div><PrivacyNote />
    </PageContent></PageShell>
  )
}

function VoteScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const events = useRoomEvents(room.id, true)
  const [pendingVote, setPendingVote] = useState<VoteValue | null>(null)
  const vote = useMutation({
    mutationFn: ({ eventId, value }: { eventId: string; value: VoteValue }) => apiClient.vote(room.id, eventId, { pool_version: room.pool?.version ?? room.version, vote: value }),
    onSuccess: async () => { setPendingVote(null); await queryClient.invalidateQueries({ queryKey: ['room', room.id] }) },
    onError: () => setPendingVote(null),
  })
  if (events.isPending) return <Loading label="Загружаем общий пул…" />
  if (events.isError) return <Empty title="Пул пока не готов" action={<Button onClick={() => void events.refetch()}>Повторить</Button>} />
  const index = room.pool?.voted_by_me ?? 0
  const item = events.data.items[index]
  if (!item) return <WaitingScreen room={room} />
  const cast = (value: VoteValue) => { if (!vote.isPending) { setPendingVote(value); vote.mutate({ eventId: item.event.id, value }) } }
  return (
    <PageShell><TopBar title="Совместный выбор" onBack={() => navigate('/')} right={<span>{index + 1} / {events.data.total}</span>} /><PageContent className={styles.poolWrap}>
      <p className={`${styles.subtitle} ${styles.center}`}>Один и тот же пул, независимые оценки</p>
      <AnimatePresence mode="wait">
        <motion.article key={item.event.id} className={styles.poolCard} drag={vote.isPending ? false : 'x'} dragConstraints={{ left: 0, right: 0 }} dragElastic={.75} initial={{ opacity: 0, scale: .96, y: 18 }} animate={{ opacity: 1, scale: 1, y: 0, x: pendingVote ? (pendingVote === 'like' ? 520 : -520) : 0, rotate: pendingVote ? (pendingVote === 'like' ? 12 : -12) : 0 }} exit={{ opacity: 0, scale: .9 }} transition={{ type: 'spring', stiffness: 240, damping: 24 }} onDragEnd={(_, info) => { if (info.offset.x > 90) cast('like'); else if (info.offset.x < -90) cast('dislike') }}>
          <img className={styles.poolImage} src={item.event.imageUrl ?? '/events/concert-singer.png'} alt={item.event.title} />
          <div className={styles.poolCopy}><p className={styles.eyebrow}>{item.event.category_slug}</p><h2>{item.event.title}</h2><p className={styles.subtitle}>{item.event.subtitle}</p><div className={styles.poolMeta}><span>◷ {item.event.date_label}</span><span>⌖ {item.event.venue_name}</span><strong>{item.event.price_label}</strong></div></div>
        </motion.article>
      </AnimatePresence>
      <div className={styles.explain}><strong>Почему в подборке</strong>{item.event.reasons.map((reason) => <span key={reason.code}>✓ {reason.text}</span>)}</div>
      <div className={styles.voteActions}><button className={styles.voteAction} disabled={vote.isPending} onClick={() => cast('dislike')}><span className={styles.voteCircle}>×</span>Не подходит</button><button className={styles.voteAction} disabled={vote.isPending} onClick={() => cast('like')}><span className={`${styles.voteCircle} ${styles.like}`}>♥</span>Хочу пойти</button></div>
      {vote.isError ? <p className={styles.error}>Голос не сохранился. Повторите действие.</p> : null}<PrivacyNote title="Выбор скрыт">Друг узнает о вашем лайке только при взаимном совпадении.</PrivacyNote>
    </PageContent></PageShell>
  )
}

function MatchScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const event = useEventDetail(room.match?.event_id)
  const ticket = useMutation({ mutationFn: () => apiClient.recordTicketClick(room.match!.event_id, { source: 'match', room_id: room.id }), onSuccess: ({ external_url }) => void maxPlatform.openExternalLink(external_url) })
  if (event.isPending) return <Loading label="Открываем ваш мэтч…" />
  if (event.isError) return <Empty title="Мэтч найден, но событие не загрузилось" />
  return (
    <PageShell><TopBar title="Совпадение" onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <Suspense fallback={<Loading label="Готовим сюрприз…" />}><MatchCelebration event={event.data} /></Suspense>
      <div className={styles.footer}><Button onClick={() => navigate(`/events/${event.data.id}`)}>Открыть событие</Button><Button tone="secondary" disabled={ticket.isPending} onClick={() => ticket.mutate()}>К билетам</Button></div>
    </PageContent></PageShell>
  )
}

function RecoveryScreen({ room }: { room: RoomSnapshot }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState('budget')
  const restart = useMutation({ mutationFn: () => apiClient.replaceMyIntent(room.id, relaxedIntent(room.myIntent, selected)), onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['room', room.id] }); navigate(mockRoute(`/rooms/${room.id}/waiting`), { replace: true }) } })
  const suggestions = [{ id: 'budget', title: 'Увеличить бюджет до 3 500 ₽', meta: '+8 подходящих событий' }, { id: 'date', title: 'Добавить пятницу вечером', meta: '+5 событий' }, { id: 'radius', title: 'Увеличить радиус до 10 км', meta: '+7 событий' }, { id: 'category', title: 'Добавить выставки', meta: '+4 события' }]
  return (
    <PageShell><TopBar title={room.name} onBack={() => navigate('/')} /><PageContent className={styles.narrow}>
      <p className={styles.eyebrow}>ПУЛ ЗАКОНЧИЛСЯ</p><h1 className={styles.title}>Пока не совпали</h1><p className={styles.subtitle}>Можно немного расширить только ваши условия и запустить новый приватный раунд.</p>
      <section className={styles.section}><h2 className={styles.sectionTitle}>Что ограничило подборку</h2><div className={styles.reason}><strong>Бюджет и время</strong><p>Пересечение получилось небольшим, а часть подходящих событий немного дороже.</p></div><div className={styles.reason}><strong>Категории и расстояние</strong><p>В выбранном радиусе мало событий с общими интересами.</p></div></section>
      <section className={styles.section}><h2 className={styles.sectionTitle}>Что можно изменить?</h2>{suggestions.map((item) => <button key={item.id} className={`${styles.suggestion} ${selected === item.id ? styles.suggestionSelected : ''}`} onClick={() => setSelected(item.id)}><span><strong>{item.title}</strong><small>{item.meta}</small></span><span>{selected === item.id ? '✓' : '○'}</span></button>)}</section>
      <PrivacyNote>Это только общие причины — ответы второго участника не раскрываются.</PrivacyNote>
      <div className={styles.footer}><Button disabled={restart.isPending} onClick={() => restart.mutate()}>{restart.isPending ? 'Обновляем…' : 'Применить и обновить подборку'}</Button></div>
    </PageContent></PageShell>
  )
}

function ChoiceField<T extends string>({ title, options, selected, onToggle }: { title: string; options: Array<[T, string]>; selected: T[]; onToggle: (value: T) => void }) {
  return <fieldset className={styles.fieldLabel}><legend>{title}</legend><ChipGroup>{options.map(([value, label]) => <Chip key={value} selected={selected.includes(value)} onClick={() => onToggle(value)}>{label}</Chip>)}</ChipGroup></fieldset>
}

function expectedScreen(room: RoomSnapshot) {
  if (room.state === 'matched') return 'match'
  if (room.state === 'exhausted') return 'recovery'
  if (room.state === 'voting') return 'vote'
  if (room.state === 'ranking') return 'waiting'
  return room.myIntent ? 'waiting' : 'intent'
}

function toIntent(values: IntentForm): RoomIntentRequestDto {
  return { dates: values.dates, day_types: [], time_slots: values.time_slots, category_slugs: values.category_slugs, budget_max_minor: values.budget * 100, radius_m: values.radius, exclusion_slugs: [], location: null, free_text: values.free_text || null }
}

function relaxedIntent(current: RoomSnapshot['myIntent'], kind: string): RoomIntentRequestDto {
  const base: RoomIntentRequestDto = current ? { dates: current.dates, day_types: current.day_types, time_slots: current.time_slots, category_slugs: current.category_slugs, budget_max_minor: current.budget_max_minor, radius_m: current.radius_m, exclusion_slugs: current.exclusion_slugs, location: current.location, free_text: current.free_text } : { dates: ['2026-09-19'], day_types: [], time_slots: ['evening'], category_slugs: ['concerts'], budget_max_minor: 300_000, radius_m: 5_000, exclusion_slugs: [], location: null, free_text: null }
  if (kind === 'budget') base.budget_max_minor = 350_000
  if (kind === 'date') base.dates = [...new Set([...base.dates, '2026-09-18'])]
  if (kind === 'radius') base.radius_m = 10_000
  if (kind === 'category') base.category_slugs = [...new Set([...base.category_slugs, 'exhibitions' as CategorySlug])]
  return base
}

function toggleValue<T>(items: T[], value: T) { return items.includes(value) ? items.filter((item) => item !== value) : [...items, value] }

function mockInviteUrl(raw: string) {
  if (!raw || (import.meta.env.VITE_API_MODE ?? 'mock') !== 'mock') return raw
  const url = new URL(raw, window.location.origin)
  url.searchParams.set('mockUser', 'anna')
  return url.toString()
}

function mockRoute(path: string) {
  if ((import.meta.env.VITE_API_MODE ?? 'mock') !== 'mock') return path
  const queryUser = new URLSearchParams(window.location.search).get('mockUser')
  const user = queryUser === 'anna' || queryUser === 'ivan' ? queryUser : sessionStorage.getItem('max-together-mock-user') ?? 'ivan'
  const url = new URL(path, window.location.origin)
  url.searchParams.set('mockUser', user)
  return `${url.pathname}${url.search}`
}
