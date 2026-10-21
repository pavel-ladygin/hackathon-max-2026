import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { Link, useNavigate } from 'react-router-dom'
import { z } from 'zod'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { apiClient, type EventSubmissionInput, type EventSubmissionRow } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import { Button, ErrorState, PageContent, PageShell, ScreenSkeleton, TopBar } from '../../shared/ui'
import styles from './submissions.module.css'

const maxMinor = 100_000_000
function parseRubles(value: string): number | null {
  if (!/^\d+(?:[.,]\d{1,2})?$/.test(value.trim())) return null
  const minor = Math.round(Number(value.replace(',', '.')) * 100)
  return Number.isSafeInteger(minor) && minor > 0 && minor <= maxMinor ? minor : null
}
function cityLocalMinute(timezone: string): string {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date())
  const value = Object.fromEntries(parts.map(({ type, value }) => [type, value]))
  return `${value.year}-${value.month}-${value.day}T${value.hour}:${value.minute}`
}
function localDraftLabel(value: string) { const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value); return match ? `${match[3]}.${match[2]}.${match[1]}, ${match[4]}:${match[5]}` : value }
const schema = z.object({
  title: z.string().trim().min(3, 'Добавьте название события.').max(200, 'Не более 200 символов.'),
  description: z.string().trim().min(10, 'Расскажите о событии подробнее.').max(10_000, 'Не более 10 000 символов.'),
  city_id: z.string().min(1, 'Выберите город.'),
  venue_name: z.string().trim().min(2, 'Укажите площадку.').max(200, 'Не более 200 символов.'),
  venue_address: z.string().trim().min(3, 'Укажите адрес.').max(500, 'Не более 500 символов.'),
  starts_at_local: z.string().min(1, 'Укажите дату и время.'),
  ends_at_local: z.string().optional(),
  price_mode: z.enum(['free', 'paid', 'unknown']),
  price_from: z.string().optional(),
  price_to: z.string().optional(),
  ticket_url: z.string().max(2048).optional(),
  category_slug: z.string().min(1, 'Выберите категорию.'),
  image_url: z.string().max(2048).optional(),
  age_rating: z.string().max(12).optional(),
}).superRefine((value, ctx) => {
  const startValid = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value.starts_at_local)
  if (!startValid) ctx.addIssue({ code: 'custom', path: ['starts_at_local'], message: 'Укажите дату и время.' })
  if (value.ends_at_local) {
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value.ends_at_local) || (startValid && value.ends_at_local <= value.starts_at_local)) ctx.addIssue({ code: 'custom', path: ['ends_at_local'], message: 'Окончание должно быть позже начала.' })
  }
  if (value.price_mode === 'paid') {
    const from = parseRubles(value.price_from ?? '')
    const to = value.price_to ? parseRubles(value.price_to) : from
    if (from === null) ctx.addIssue({ code: 'custom', path: ['price_from'], message: 'Укажите цену до 1 000 000 ₽, можно с копейками.' })
    if (value.price_to && (to === null || (from !== null && to < from))) ctx.addIssue({ code: 'custom', path: ['price_to'], message: 'Максимум должен быть не меньше минимума.' })
  }
  if (value.ticket_url) {
    try { if (new URL(value.ticket_url).protocol !== 'https:') throw new Error() } catch { ctx.addIssue({ code: 'custom', path: ['ticket_url'], message: 'Укажите ссылку с HTTPS.' }) }
  }
  if (value.image_url) {
    try { if (new URL(value.image_url).protocol !== 'https:') throw new Error() } catch { ctx.addIssue({ code: 'custom', path: ['image_url'], message: 'Укажите ссылку на афишу с HTTPS.' }) }
  }
  if (value.price_mode !== 'free' && !value.ticket_url?.trim()) ctx.addIssue({ code: 'custom', path: ['ticket_url'], message: 'Для платного события укажите ссылку на билеты.' })
})
type Values = z.infer<typeof schema>
const defaults: Values = { title: '', description: '', city_id: '', venue_name: '', venue_address: '', starts_at_local: '', ends_at_local: '', price_mode: 'free', price_from: '', price_to: '', ticket_url: '', category_slug: '', image_url: '', age_rating: '' }

const labelForCategory = (slug: string) => CATEGORY_OPTIONS.find((item) => item.slug === slug)?.label ?? slug
const priceLabel = (row: EventSubmissionRow) => row.payload.price_mode === 'free' ? 'Бесплатно' : row.payload.price_mode === 'unknown' ? 'Цена уточняется' : `${Number(row.payload.price_from_minor ?? 0) / 100} ₽`
const localLabel = (value: string, timezone?: string) => { try { return new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short', ...(timezone ? { timeZone: timezone } : {}) }).format(new Date(value)) } catch { return value } }
function submissionErrorText(error: unknown) {
  if (error instanceof ApiError && error.kind === 'auth') return 'Сессия MAX истекла. Откройте приложение заново, затем повторите отправку.'
  if (error instanceof ApiError && error.kind === 'rateLimit') return 'Вы уже отправили несколько заявок. Попробуйте позже.'
  if (error instanceof ApiError && error.kind === 'conflict') return 'Заявка уже отправлена с другим содержимым. Проверьте историю заявок.'
  if (error instanceof ApiError && error.code === 'DOMAIN_NOT_APPROVED') return 'Ссылка или афиша пока не разрешена. Можно убрать её из заявки и отправить событие без ресурса.'
  if (error instanceof ApiError && error.kind === 'validation') return 'Проверьте сведения заявки и исправьте отмеченные поля.'
  return 'Не удалось отправить заявку. Проверьте соединение и попробуйте ещё раз.'
}

const fieldLabels: Partial<Record<keyof Values, string>> = { title: 'Название', description: 'Описание', city_id: 'Город', venue_name: 'Место', venue_address: 'Адрес', starts_at_local: 'Начало', ends_at_local: 'Окончание', price_from: 'Цена от', price_to: 'Цена до', ticket_url: 'Ссылка на событие', image_url: 'Афиша', category_slug: 'Категория' }

export function EventSubmissionPage() {
  const navigate = useNavigate()
  const bootstrap = useBootstrap()
  const queryClient = useQueryClient()
  const options = useQuery({ queryKey: ['event-submission-options'], queryFn: () => apiClient.getEventSubmissionOptions() })
  const [review, setReview] = useState(false)
  const [submittedId, setSubmittedId] = useState<string | null>(null)
  const keyRef = useRef<{ payload: string; key: string } | null>(null)
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { ...defaults, city_id: bootstrap.data?.user.cityId ?? '' } })
  const values = useWatch({ control: form.control }) as Values
  const mutation = useMutation({
    mutationFn: async (data: Values) => {
      const payload: EventSubmissionInput = {
        title: data.title.trim(), description: data.description.trim(), city_id: data.city_id,
        venue_name: data.venue_name.trim(), venue_address: data.venue_address.trim(), starts_at_local: data.starts_at_local,
        ...(data.ends_at_local ? { ends_at_local: data.ends_at_local } : {}), price_mode: data.price_mode,
        price_from_minor: data.price_mode === 'free' ? 0 : data.price_mode === 'unknown' ? null : parseRubles(data.price_from ?? ''),
        price_to_minor: data.price_mode === 'paid' && data.price_to ? parseRubles(data.price_to) : undefined,
        ...(data.ticket_url?.trim() ? { ticket_url: data.ticket_url.trim() } : {}), category_slug: data.category_slug,
        ...(data.image_url?.trim() ? { image_url: data.image_url.trim() } : {}), ...(data.age_rating ? { age_rating: data.age_rating } : {}),
      }
      const serialized = JSON.stringify(payload)
      if (!keyRef.current || keyRef.current.payload !== serialized) keyRef.current = { payload: serialized, key: crypto.randomUUID() }
      return apiClient.createEventSubmission(payload, keyRef.current.key)
    },
    onSuccess: async (result) => { setSubmittedId(result.id); await queryClient.invalidateQueries({ queryKey: ['my-event-submissions'] }) },
    onError: (error) => {
      if (error instanceof ApiError) for (const [field, message] of Object.entries(error.fieldErrors)) {
        const formField = field === 'price_from_minor' ? 'price_from' : field === 'price_to_minor' ? 'price_to' : field
        if (formField in form.getValues()) form.setError(formField as keyof Values, { message }, { shouldFocus: true })
      }
    },
  })
  if (bootstrap.isPending || options.isPending) return <ScreenSkeleton variant="form" label="Готовим форму мероприятия…" />
  if (bootstrap.isError || options.isError || !bootstrap.data || !options.data) return <ErrorState title="Не удалось загрузить форму" description="Проверьте соединение и повторите попытку." action={<Button onClick={() => { void bootstrap.refetch(); void options.refetch() }}>Повторить</Button>} />
  if (submittedId) return <PageShell><TopBar title="Мероприятие" onBack={() => navigate('/preferences')} /><PageContent className={styles.narrow}><div className={styles.success}><span aria-hidden="true">✓</span><p className={styles.eyebrow}>ЗАЯВКА ОТПРАВЛЕНА</p><h1>Отправлено на модерацию</h1><p>После модерации оно появится в афише. Статус можно посмотреть в истории заявок.</p><Button onClick={() => navigate('/my/event-submissions')}>Мои заявки</Button></div></PageContent></PageShell>

  const field = (name: keyof Values) => form.formState.errors[name]?.message
  const mapLocalErrors = (data: Values) => {
    const selectedCity = options.data.cities.find((item) => item.id === data.city_id)
    if (selectedCity && data.starts_at_local <= cityLocalMinute(selectedCity.timezone)) { form.setError('starts_at_local', { message: 'Выберите время в будущем по часовому поясу города.' }, { shouldFocus: true }); return }
    if (!review) { setReview(true); return }
    mutation.mutate(data)
  }
  const city = options.data.cities.find((item) => item.id === values.city_id)
  const currentCategory = options.data.categories.find((item) => item.slug === values.category_slug)?.label ?? labelForCategory(values.category_slug)
  return <PageShell><TopBar title={review ? 'Предпросмотр' : 'Предложить событие'} onBack={() => review ? setReview(false) : navigate(-1)} /><PageContent className={styles.narrow}>
    <p className={styles.eyebrow}>СОБЫТИЯ ОТ ГОРОЖАН</p><h1 className={styles.title}>{review ? 'Проверьте детали' : 'Добавьте событие в афишу'}</h1><p className={styles.lead}>Заявка появится в каталоге после проверки модератором. Бесплатное событие можно предложить без ссылки.</p>
    {!review ? <form className={styles.form} onSubmit={form.handleSubmit(mapLocalErrors)} noValidate>
      <label>Название<input maxLength={200} autoComplete="off" {...form.register('title')} aria-invalid={Boolean(field('title'))} />{field('title') && <small role="alert">{field('title')}</small>}</label>
      <label>Описание<textarea rows={5} maxLength={10_000} {...form.register('description')} aria-invalid={Boolean(field('description'))} />{field('description') && <small role="alert">{field('description')}</small>}</label>
      <label>Город<select {...form.register('city_id')}><option value="">Выберите город</option>{options.data.cities.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select>{field('city_id') && <small role="alert">{field('city_id')}</small>}</label>
      <div className={styles.two}><label>Место<input maxLength={200} {...form.register('venue_name')} />{field('venue_name') && <small role="alert">{field('venue_name')}</small>}</label><label>Адрес<input maxLength={500} {...form.register('venue_address')} />{field('venue_address') && <small role="alert">{field('venue_address')}</small>}</label></div>
      <p className={styles.helper}>{city ? `Время указано по часовому поясу ${city.timezone}.` : 'Сначала выберите город, чтобы показать его часовой пояс.'}</p>
      <div className={styles.two}><label>Начало<input type="datetime-local" {...form.register('starts_at_local')} />{field('starts_at_local') && <small role="alert">{field('starts_at_local')}</small>}</label><label>Окончание · необязательно<input type="datetime-local" {...form.register('ends_at_local')} />{field('ends_at_local') && <small role="alert">{field('ends_at_local')}</small>}</label></div>
      <label>Категория<select {...form.register('category_slug')}><option value="">Выберите категорию</option>{options.data.categories.map((item) => <option value={item.slug} key={item.slug}>{item.label}</option>)}</select>{field('category_slug') && <small role="alert">{field('category_slug')}</small>}</label>
      <fieldset><legend>Стоимость</legend><div className={styles.choices}>{([['free', 'Бесплатно'], ['paid', 'Есть цена'], ['unknown', 'Цена уточняется']] as const).map(([value, label]) => <label key={value}><input type="radio" value={value} {...form.register('price_mode')} />{label}</label>)}</div></fieldset>
      {values.price_mode === 'paid' && <div className={styles.two}><label>Цена от, ₽<input type="number" min="0.01" max="1000000" step="0.01" inputMode="decimal" {...form.register('price_from')} />{field('price_from') && <small role="alert">{field('price_from')}</small>}</label><label>Цена до, ₽ · необязательно<input type="number" min="0.01" max="1000000" step="0.01" inputMode="decimal" {...form.register('price_to')} />{field('price_to') && <small role="alert">{field('price_to')}</small>}</label></div>}
      <label>Ссылка на событие или билеты{values.price_mode !== 'free' && <em>обязательно</em>}<input type="url" inputMode="url" placeholder="https://…" maxLength={2048} {...form.register('ticket_url')} />{field('ticket_url') && <small role="alert">{field('ticket_url')}</small>}</label>
      <label>Ссылка на афишу · необязательно<input type="url" inputMode="url" placeholder="https://…" maxLength={2048} {...form.register('image_url')} />{field('image_url') && <small role="alert">{field('image_url')}</small>}<span>Фото появится после проверки источника. В предпросмотре мы его не загружаем.</span></label>
      <label>Возрастной рейтинг · необязательно<select {...form.register('age_rating')}><option value="">Не указан</option><option value="0+">0+</option><option value="6+">6+</option><option value="12+">12+</option><option value="16+">16+</option><option value="18+">18+</option></select></label>
      {mutation.isError && <div className={styles.alert} role="alert">{submissionErrorText(mutation.error)} {mutation.error instanceof ApiError && mutation.error.kind === 'auth' && <a href="/">Открыть приложение MAX</a>}</div>}
      <Button type="submit" disabled={mutation.isPending} state={mutation.isPending ? 'loading' : 'idle'} loadingLabel="Отправляем…">Проверить перед отправкой</Button>
      <Link className={styles.secondaryLink} to="/my/event-submissions">Мои заявки</Link>
    </form> : <div className={styles.preview}>
      <div className={styles.imagePlaceholder} aria-label="Предпросмотр афиши"><span>Фото появится после модерации</span></div>
      <p className={styles.eyebrow}>{currentCategory}</p><h2>{values.title}</h2><p>{values.description}</p>
      <dl><div><dt>Когда</dt><dd>{localDraftLabel(values.starts_at_local)}{values.ends_at_local ? ` — ${localDraftLabel(values.ends_at_local)}` : ''} · {city?.timezone}</dd></div><div><dt>Где</dt><dd>{values.venue_name}, {values.venue_address}, {city?.name}</dd></div><div><dt>Цена</dt><dd>{values.price_mode === 'free' ? 'Бесплатно' : values.price_mode === 'unknown' ? 'Цена уточняется' : `${values.price_from} ₽${values.price_to ? ` — ${values.price_to} ₽` : ''}`}</dd></div><div><dt>Ссылка</dt><dd>{values.ticket_url || 'Не указана'}</dd></div></dl>
      {mutation.isError && <div className={styles.alert} role="alert"><p>{submissionErrorText(mutation.error)} Все введённые данные сохранены.</p>{Object.entries(form.formState.errors).map(([name, error]) => error?.message ? <span key={name}>{fieldLabels[name as keyof Values] ?? name}: {error.message}</span> : null)}{mutation.error instanceof ApiError && mutation.error.kind === 'auth' && <a href="/">Открыть приложение MAX</a>}</div>}
      <Button disabled={mutation.isPending} state={mutation.isPending ? 'loading' : 'idle'} loadingLabel="Отправляем…" onClick={() => form.handleSubmit((data) => mutation.mutate(data))()}>Отправить на модерацию</Button><Button tone="secondary" disabled={mutation.isPending} onClick={() => setReview(false)}>Вернуться к форме</Button>
    </div>}
  </PageContent></PageShell>
}

export function MyEventSubmissionsPage() {
  const navigate = useNavigate()
  const [page, setPage] = useState(0)
  const pageSize = 20
  const query = useQuery({ queryKey: ['my-event-submissions', page], queryFn: () => apiClient.getMyEventSubmissions({ limit: pageSize, offset: page * pageSize }) })
  if (query.isPending) return <ScreenSkeleton variant="form" label="Загружаем ваши заявки…" />
  if (query.isError) return <ErrorState title="Не удалось загрузить заявки" description={query.error instanceof ApiError && query.error.kind === 'auth' ? 'Сессия истекла. Откройте приложение MAX заново.' : 'Проверьте соединение и повторите попытку.'} action={<Button onClick={() => void query.refetch()}>Повторить</Button>} />
  return <PageShell><TopBar title="Мои заявки" onBack={() => navigate(-1)} /><PageContent className={styles.narrow}><p className={styles.eyebrow}>ВАШИ СОБЫТИЯ</p><h1 className={styles.title}>История заявок</h1><p className={styles.lead}>Здесь видны статус проверки и результат модерации.</p>{query.data.items.length === 0 && page === 0 ? <div className={styles.empty}><p>Пока нет предложенных событий.</p><Button onClick={() => navigate('/events/suggest')}>Предложить событие</Button></div> : <div className={styles.list}>{query.data.items.map((row) => <article className={styles.submission} key={row.id}><div className={styles.rowHead}><span className={`${styles.badge} ${styles[row.status]}`}>{statusLabel(row.status)}</span><time>{localLabel(row.created_at)}</time></div><h2>{row.payload.title}</h2><p>{row.payload.venue_name} · {row.payload.venue_address}</p><p>{localLabel(row.starts_at, row.timezone)} · {priceLabel(row)}</p>{row.status === 'rejected' && row.rejection_reason && <p className={styles.rejection}><strong>Причина:</strong> {row.rejection_reason}</p>}{row.status === 'approved' && row.event_id && <Button tone="secondary" onClick={() => navigate(`/events/${row.event_id}`)}>Открыть в афише</Button>}</article>)}</div>}{query.data.total > pageSize && <div className={styles.pager}><Button tone="secondary" disabled={page === 0} onClick={() => setPage((value) => Math.max(0, value - 1))}>Назад</Button><span>{page + 1} / {Math.ceil(query.data.total / pageSize)}</span><Button tone="secondary" disabled={(page + 1) * pageSize >= query.data.total} onClick={() => setPage((value) => value + 1)}>Дальше</Button></div>}<Button tone="secondary" onClick={() => navigate('/events/suggest')}>Предложить ещё одно событие</Button></PageContent></PageShell>
}

function statusLabel(status: EventSubmissionRow['status']) { return status === 'pending' ? 'На проверке' : status === 'approved' ? 'Одобрено' : 'Отклонено' }
