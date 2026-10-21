import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { Link, useNavigate } from 'react-router-dom'
import { z } from 'zod'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { apiClient, type EventSubmissionInput, type EventSubmissionRow } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import { CustomSelect } from '../../shared/ui/CustomSelect'
import { DatePicker } from '../../shared/ui/date-picker'
import { MoneyInput } from '../../shared/ui/MoneyInput'
import { decimalToMinor } from '../../shared/ui/money'
import { ChoiceChips } from '../../shared/ui/ChoiceChips'
import { Button, ErrorState, PageContent, PageShell, ScreenSkeleton, TopBar } from '../../shared/ui'
import styles from './submissions.module.css'

const maxMinor = 100_000_000
function parseRubles(value: string): number | null {
  const minor = decimalToMinor(value)
  return minor !== null && Number.isSafeInteger(minor) && minor > 0 && minor <= maxMinor ? minor : null
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
  start_date: z.string().optional(), start_time: z.string().optional(), end_date: z.string().optional(), end_time: z.string().optional(),
  price_mode: z.enum(['free', 'paid', 'unknown']),
  price_from: z.string().optional(),
  price_to: z.string().optional(),
  ticket_url: z.string().max(2048).optional(),
  category_slug: z.string().min(1, 'Выберите категорию.'),
  age_rating: z.string().max(12).optional(),
}).superRefine((value, ctx) => {
  const validTime = (time: string) => /^(?:[01]\d|2[0-3]):[0-5]\d$/.test(time)
  const startValid = isCanonicalDate(value.start_date ?? '') && validTime(value.start_time ?? '')
  if (!isCanonicalDate(value.start_date ?? '')) ctx.addIssue({ code: 'custom', path: ['start_date'], message: 'Укажите дату начала.' })
  if (!validTime(value.start_time ?? '')) ctx.addIssue({ code: 'custom', path: ['start_time'], message: 'Введите время от 00:00 до 23:59.' })
  const hasEnd = Boolean(value.end_date || value.end_time)
  const endValid = isCanonicalDate(value.end_date ?? '') && validTime(value.end_time ?? '')
  if (hasEnd && !isCanonicalDate(value.end_date ?? '')) ctx.addIssue({ code: 'custom', path: ['end_date'], message: 'Укажите дату окончания.' })
  if (hasEnd && !validTime(value.end_time ?? '')) ctx.addIssue({ code: 'custom', path: ['end_time'], message: 'Введите время от 00:00 до 23:59.' })
  if (startValid && endValid && `${value.end_date}T${value.end_time}` <= `${value.start_date}T${value.start_time}`) ctx.addIssue({ code: 'custom', path: ['end_time'], message: 'Окончание должно быть позже начала.' })
  if (value.price_mode === 'paid') {
    const from = parseRubles(value.price_from ?? '')
    const to = value.price_to ? parseRubles(value.price_to) : from
    if (from === null) ctx.addIssue({ code: 'custom', path: ['price_from'], message: 'Укажите цену до 1 000 000 ₽, можно с копейками.' })
    if (value.price_to && (to === null || (from !== null && to < from))) ctx.addIssue({ code: 'custom', path: ['price_to'], message: 'Максимум должен быть не меньше минимума.' })
  }
  if (value.ticket_url) {
    try { if (new URL(value.ticket_url).protocol !== 'https:') throw new Error() } catch { ctx.addIssue({ code: 'custom', path: ['ticket_url'], message: 'Укажите ссылку с HTTPS.' }) }
  }
  if (value.price_mode !== 'free' && !value.ticket_url?.trim()) ctx.addIssue({ code: 'custom', path: ['ticket_url'], message: 'Для платного события укажите ссылку на билеты.' })
})
type Values = z.infer<typeof schema>
const defaults: Values = { title: '', description: '', city_id: '', venue_name: '', venue_address: '', start_date: '', start_time: '', end_date: '', end_time: '', price_mode: 'free', price_from: '', price_to: '', ticket_url: '', category_slug: '', age_rating: '' }

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

const fieldLabels: Partial<Record<keyof Values, string>> = { title: 'Название', description: 'Описание', city_id: 'Город', venue_name: 'Место', venue_address: 'Адрес', start_date: 'Дата начала', start_time: 'Время начала', end_date: 'Дата окончания', end_time: 'Время окончания', price_from: 'Цена от', price_to: 'Цена до', ticket_url: 'Ссылка на событие', category_slug: 'Категория' }

function isCanonicalDate(value: string) { if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false; const [y, m, d] = value.split('-').map(Number); const date = new Date(y, m - 1, d); return date.getFullYear() === y && date.getMonth() === m - 1 && date.getDate() === d }

export function EventSubmissionPage() {
  const navigate = useNavigate()
  const bootstrap = useBootstrap()
  const queryClient = useQueryClient()
  const options = useQuery({ queryKey: ['event-submission-options'], queryFn: () => apiClient.getEventSubmissionOptions() })
  const [review, setReview] = useState(false)
  const [submittedId, setSubmittedId] = useState<string | null>(null)
  const [poster, setPoster] = useState<{ file: File | null; previewUrl: string; imageId: string | null; progress: number; error: string }>({ file: null, previewUrl: '', imageId: null, progress: 0, error: '' })
  const [posterDragging, setPosterDragging] = useState(false)
  const keyRef = useRef<{ payload: string; key: string } | null>(null)
  const uploadSequence = useRef(0)
  const uploadAbort = useRef<AbortController | null>(null)
  const posterInput = useRef<HTMLInputElement>(null)
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { ...defaults, city_id: bootstrap.data?.user.cityId ?? '' } })
  const values = useWatch({ control: form.control }) as Values
  const mutation = useMutation({
    mutationFn: async (data: Values) => {
      const payload: EventSubmissionInput = {
        title: data.title.trim(), description: data.description.trim(), city_id: data.city_id,
        venue_name: data.venue_name.trim(), venue_address: data.venue_address.trim(), starts_at_local: `${data.start_date}T${data.start_time}`,
        ...(data.end_date && data.end_time ? { ends_at_local: `${data.end_date}T${data.end_time}` } : {}), price_mode: data.price_mode,
        price_from_minor: data.price_mode === 'free' ? 0 : data.price_mode === 'unknown' ? null : parseRubles(data.price_from ?? ''),
        price_to_minor: data.price_mode === 'paid' && data.price_to ? parseRubles(data.price_to) : undefined,
        ...(data.ticket_url?.trim() ? { ticket_url: data.ticket_url.trim() } : {}), category_slug: data.category_slug,
        ...(poster.imageId ? { image_id: poster.imageId } : {}), ...(data.age_rating ? { age_rating: data.age_rating } : {}),
      }
      const serialized = JSON.stringify(payload)
      if (!keyRef.current || keyRef.current.payload !== serialized) keyRef.current = { payload: serialized, key: crypto.randomUUID() }
      return apiClient.createEventSubmission(payload, keyRef.current.key)
    },
    onSuccess: async (result) => { setSubmittedId(result.id); await queryClient.invalidateQueries({ queryKey: ['my-event-submissions'] }) },
    onError: (error) => {
      if (error instanceof ApiError) for (const [field, message] of Object.entries(error.fieldErrors)) {
        if (field === 'image_id') {
          setPoster((current) => ({ ...current, imageId: null, error: message }))
          setReview(false)
          continue
        }
        const formField = field === 'price_from_minor' ? 'price_from' : field === 'price_to_minor' ? 'price_to' : field
        if (formField in form.getValues()) form.setError(formField as keyof Values, { message }, { shouldFocus: true })
      }
    },
  })
  useEffect(() => () => { uploadSequence.current += 1; uploadAbort.current?.abort() }, [])
  useEffect(() => () => { if (poster.previewUrl) URL.revokeObjectURL(poster.previewUrl) }, [poster.previewUrl])
  const uploadPoster = async (file: File) => {
    uploadAbort.current?.abort()
    const sequence = ++uploadSequence.current
    const controller = new AbortController()
    uploadAbort.current = controller
    const error = !['image/jpeg', 'image/png', 'image/webp'].includes(file.type) ? 'Выберите изображение JPEG, PNG или WebP.' : file.size > 10 * 1024 * 1024 ? 'Размер афиши не должен превышать 10 МиБ.' : ''
    const previewUrl = error ? '' : poster.file === file && poster.previewUrl ? poster.previewUrl : URL.createObjectURL(file)
    if (poster.previewUrl && poster.previewUrl !== previewUrl) URL.revokeObjectURL(poster.previewUrl)
    setPoster({ file, previewUrl, imageId: null, progress: 0, error })
    if (error) return
    try {
      const result = await apiClient.uploadEventSubmissionImage(file, { signal: controller.signal, onProgress: (progress) => { if (uploadSequence.current === sequence) setPoster((current) => ({ ...current, progress })) } })
      if (uploadSequence.current === sequence) setPoster((current) => ({ ...current, imageId: result.image_id, progress: 100, error: '' }))
    } catch {
      if (uploadSequence.current === sequence && !controller.signal.aborted) setPoster((current) => ({ ...current, error: 'Не удалось загрузить афишу. Попробуйте ещё раз.' }))
    }
  }
  const clearPoster = () => { uploadSequence.current += 1; uploadAbort.current?.abort(); setPoster({ file: null, previewUrl: '', imageId: null, progress: 0, error: '' }); if (posterInput.current) posterInput.current.value = '' }
  const cancelPosterUpload = () => { uploadSequence.current += 1; uploadAbort.current?.abort(); setPoster((current) => ({ ...current, error: 'Загрузка отменена. Попробуйте ещё раз.' })) }
  if (bootstrap.isPending || options.isPending) return <ScreenSkeleton variant="form" label="Готовим форму мероприятия…" />
  if (bootstrap.isError || options.isError || !bootstrap.data || !options.data) return <ErrorState title="Не удалось загрузить форму" description="Проверьте соединение и повторите попытку." action={<Button onClick={() => { void bootstrap.refetch(); void options.refetch() }}>Повторить</Button>} />
  if (submittedId) return <PageShell><TopBar title="Мероприятие" onBack={() => navigate('/preferences')} /><PageContent className={styles.narrow}><div className={styles.success}><span aria-hidden="true">✓</span><p className={styles.eyebrow}>ЗАЯВКА ОТПРАВЛЕНА</p><h1>Отправлено на модерацию</h1><p>После модерации оно появится в афише. Статус можно посмотреть в истории заявок.</p><Button onClick={() => navigate('/my/event-submissions')}>Мои заявки</Button></div></PageContent></PageShell>

  const field = (name: keyof Values) => form.formState.errors[name]?.message
  const mapLocalErrors = (data: Values) => {
    const selectedCity = options.data.cities.find((item) => item.id === data.city_id)
    if (selectedCity && `${data.start_date}T${data.start_time}` <= cityLocalMinute(selectedCity.timezone)) { form.setError('start_time', { message: 'Выберите время в будущем по часовому поясу города.' }, { shouldFocus: true }); return }
    if (poster.file && (!poster.imageId || poster.error)) return
    if (!review) { setReview(true); return }
    mutation.mutate(data)
  }
  const city = options.data.cities.find((item) => item.id === values.city_id)
  const currentCategory = options.data.categories.find((item) => item.slug === values.category_slug)?.label ?? labelForCategory(values.category_slug)
  const startsAt = values.start_date && values.start_time ? `${values.start_date}T${values.start_time}` : ''
  const endsAt = values.end_date && values.end_time ? `${values.end_date}T${values.end_time}` : ''
  return <PageShell><TopBar title={review ? 'Предпросмотр' : 'Предложить событие'} onBack={() => review ? setReview(false) : navigate(-1)} /><PageContent className={styles.narrow}>
    <p className={styles.eyebrow}>СОБЫТИЯ ОТ ГОРОЖАН</p><h1 className={styles.title}>{review ? 'Проверьте детали' : 'Добавьте событие в афишу'}</h1><p className={styles.lead}>Заявка появится в каталоге после проверки модератором. Бесплатное событие можно предложить без ссылки.</p>
    {!review ? <form className={styles.form} onSubmit={form.handleSubmit(mapLocalErrors)} noValidate>
      <label>Название<input maxLength={200} autoComplete="off" {...form.register('title')} aria-invalid={Boolean(field('title'))} />{field('title') && <small role="alert">{field('title')}</small>}</label>
      <label>Описание<textarea rows={5} maxLength={10_000} {...form.register('description')} aria-invalid={Boolean(field('description'))} />{field('description') && <small role="alert">{field('description')}</small>}</label>
      <CustomSelect label="Город" value={values.city_id} placeholder="Выберите город" options={options.data.cities.map((item) => ({ value: item.id, label: item.name }))} error={field('city_id')} onChange={(value) => form.setValue('city_id', value, { shouldValidate: true, shouldDirty: true })} />
      <div className={styles.two}><label>Место<input maxLength={200} {...form.register('venue_name')} />{field('venue_name') && <small role="alert">{field('venue_name')}</small>}</label><label>Адрес<input maxLength={500} {...form.register('venue_address')} />{field('venue_address') && <small role="alert">{field('venue_address')}</small>}</label></div>
      <p className={styles.helper}>{city ? `Время указано по часовому поясу ${city.timezone}.` : 'Сначала выберите город, чтобы показать его часовой пояс.'}</p>
      <div className={styles.two}>
        <div className={styles.dateTimeField}><DatePicker id="event-start-date" label="Начало" value={values.start_date} min={city ? cityLocalMinute(city.timezone).slice(0, 10) : undefined} invalid={Boolean(field('start_date'))} describedBy="event-start-time-help" onChange={(value) => form.setValue('start_date', value ?? '', { shouldValidate: true, shouldDirty: true })} /><label htmlFor="event-start-time">Время<input id="event-start-time" type="text" inputMode="text" placeholder="ЧЧ:ММ" autoComplete="off" maxLength={5} aria-label="Время начала, ЧЧ:ММ" aria-invalid={Boolean(field('start_time'))} aria-describedby={field('start_time') ? 'event-start-time-error' : 'event-start-time-help'} {...form.register('start_time')} />{field('start_time') && <small id="event-start-time-error" role="alert">{field('start_time')}</small>}<span id="event-start-time-help">24-часовой формат</span></label>{field('start_date') && <small role="alert">{field('start_date')}</small>}</div>
        <div className={styles.dateTimeField}><DatePicker id="event-end-date" label="Окончание" describedBy="event-end-help" value={values.end_date} min={values.start_date || undefined} invalid={Boolean(field('end_date') || field('end_time'))} onChange={(value) => form.setValue('end_date', value ?? '', { shouldValidate: true, shouldDirty: true })} /><label htmlFor="event-end-time">Время<input id="event-end-time" type="text" inputMode="text" placeholder="ЧЧ:ММ" autoComplete="off" maxLength={5} aria-label="Время окончания, ЧЧ:ММ" aria-invalid={Boolean(field('end_time'))} aria-describedby={field('end_time') ? 'event-end-time-error' : undefined} {...form.register('end_time')} />{field('end_time') && <small id="event-end-time-error" role="alert">{field('end_time')}</small>}</label>{field('end_date') && <small role="alert">{field('end_date')}</small>}<p id="event-end-help" className={styles.helper}>Окончание указывать необязательно.</p></div>
      </div>
      <CustomSelect label="Категория" value={values.category_slug} placeholder="Выберите категорию" options={options.data.categories.map((item) => ({ value: item.slug, label: item.label }))} error={field('category_slug')} onChange={(value) => form.setValue('category_slug', value, { shouldValidate: true, shouldDirty: true })} />
      <fieldset><legend>Стоимость</legend><ChoiceChips options={[['free', 'Бесплатно'], ['paid', 'Есть цена'], ['unknown', 'Цена уточняется']]} selected={[values.price_mode]} onToggle={(value) => form.setValue('price_mode', value, { shouldValidate: true, shouldDirty: true })} className={styles.priceChips} /></fieldset>
      {values.price_mode === 'paid' && <div className={styles.moneyRange}><div className={styles.moneyField}><MoneyInput id="event-price-from" label="Цена от, ₽" min={0.01} max={1_000_000} value={values.price_from ?? ''} invalid={Boolean(field('price_from'))} onChange={(value) => form.setValue('price_from', value, { shouldValidate: true, shouldDirty: true })} />{field('price_from') && <small role="alert">{field('price_from')}</small>}</div><div className={styles.moneyField}><MoneyInput id="event-price-to" label="Цена до, ₽" describedBy="event-price-to-help" min={0.01} max={1_000_000} value={values.price_to ?? ''} invalid={Boolean(field('price_to'))} onChange={(value) => form.setValue('price_to', value, { shouldValidate: true, shouldDirty: true })} /><p id="event-price-to-help" className={styles.helper}>Необязательно</p>{field('price_to') && <small role="alert">{field('price_to')}</small>}</div></div>}
      <label>Ссылка на событие или билеты{values.price_mode !== 'free' && <em>обязательно</em>}<input type="url" inputMode="url" placeholder="https://…" maxLength={2048} {...form.register('ticket_url')} />{field('ticket_url') && <small role="alert">{field('ticket_url')}</small>}</label>
      <div className={styles.posterField}>
        <div className={styles.posterHeading}><span className={styles.fieldLabel}>Афиша</span><span>Необязательно</span></div>
        <input ref={posterInput} className={styles.fileInput} type="file" accept="image/jpeg,image/png,image/webp" tabIndex={-1} aria-hidden="true" onChange={(event) => { const file = event.currentTarget.files?.[0]; if (file) void uploadPoster(file) }} />
        <div className={`${poster.file ? styles.posterCard : styles.dropzone} ${posterDragging ? styles.dropzoneActive : ''}`} role="group" aria-label="Зона загрузки афиши" onDragEnter={(event) => { event.preventDefault(); setPosterDragging(true) }} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setPosterDragging(false) }} onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); setPosterDragging(false); const file = event.dataTransfer.files[0]; if (file) void uploadPoster(file) }}>
          {poster.file ? <>
            {poster.previewUrl ? <img className={styles.posterThumb} src={poster.previewUrl} alt="Предпросмотр выбранной афиши" /> : null}
            <div className={styles.posterCardBody}>
              <div className={styles.posterFileInfo}><div><strong>{poster.file.name}</strong><span>{new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 }).format(poster.file.size / 1024 / 1024)} МиБ</span></div><button type="button" className={styles.posterRemove} aria-label={poster.imageId ? 'Удалить' : 'Удалить файл'} onClick={clearPoster}>×</button></div>
              {poster.error ? <>
                <div className={styles.uploadError} role="alert">{poster.error}</div>
                <div className={styles.posterActions}><Button type="button" onClick={() => poster.file && void uploadPoster(poster.file)}>Повторить</Button><Button type="button" tone="secondary" onClick={() => posterInput.current?.click()}>Выбрать другой файл</Button></div>
              </> : poster.imageId ? <>
                <span className={styles.posterReady}><span aria-hidden="true">✓</span><span>Афиша загружена</span></span>
                <Button type="button" tone="secondary" onClick={() => posterInput.current?.click()}>Заменить афишу</Button>
              </> : <>
                <div className={styles.progressLabel}><span>Загрузка афиши…</span><span>{poster.progress}%</span></div>
                <div className={styles.progressTrack} role="progressbar" aria-label="Загрузка афиши" aria-valuenow={poster.progress} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${poster.progress}%` }} /></div>
                <Button type="button" tone="secondary" onClick={cancelPosterUpload}>Отменить загрузку</Button>
              </>}
            </div>
          </> : <>
            <button type="button" className={styles.uploadIcon} aria-label="Выбрать файл афиши" onClick={() => posterInput.current?.click()}>
              <svg viewBox="0 0 40 40" aria-hidden="true"><path d="M20 27V10m0 0-7 7m7-7 7 7M9 25v7h22v-7" /></svg>
            </button>
            <strong>{posterDragging ? 'Отпустите файл здесь' : 'Добавьте афишу'}</strong>
            <span>{posterDragging ? 'Загрузится одно изображение' : 'Нажмите на значок загрузки или перетащите изображение сюда'}</span>
            <span>JPEG, PNG или WebP · до 10 МиБ</span>
          </>}
        </div>
        <p className={styles.helper}>Афиша появится в каталоге после проверки события.</p>
      </div>
      <CustomSelect label="Возрастной рейтинг · необязательно" value={values.age_rating ?? ''} placeholder="Не указан" options={[{ value: '', label: 'Не указан' }, ...['0+', '6+', '12+', '16+', '18+'].map((value) => ({ value, label: value }))]} onChange={(value) => form.setValue('age_rating', value, { shouldDirty: true })} />
      {mutation.isError && <div className={styles.alert} role="alert">{submissionErrorText(mutation.error)} {mutation.error instanceof ApiError && mutation.error.kind === 'auth' && <a href="/">Открыть приложение MAX</a>}</div>}
      <Button type="submit" disabled={mutation.isPending || Boolean(poster.file && !poster.imageId)} state={mutation.isPending ? 'loading' : 'idle'} loadingLabel="Отправляем…">Проверить перед отправкой</Button>
      <Link className={styles.secondaryLink} to="/my/event-submissions">Мои заявки</Link>
    </form> : <div className={styles.preview}>
      <div className={styles.imagePlaceholder} aria-label="Предпросмотр афиши">{poster.previewUrl ? <img src={poster.previewUrl} alt={`Афиша: ${values.title || 'мероприятия'}`} /> : <span>Фото появится после модерации</span>}</div>
      <p className={styles.eyebrow}>{currentCategory}</p><h2>{values.title}</h2><p>{values.description}</p>
      <dl><div><dt>Когда</dt><dd>{localDraftLabel(startsAt)}{endsAt ? ` — ${localDraftLabel(endsAt)}` : ''} · {city?.timezone}</dd></div><div><dt>Где</dt><dd>{values.venue_name}, {values.venue_address}, {city?.name}</dd></div><div><dt>Цена</dt><dd>{values.price_mode === 'free' ? 'Бесплатно' : values.price_mode === 'unknown' ? 'Цена уточняется' : `${values.price_from} ₽${values.price_to ? ` — ${values.price_to} ₽` : ''}`}</dd></div><div><dt>Ссылка</dt><dd>{values.ticket_url || 'Не указана'}</dd></div></dl>
      {mutation.isError && <div className={styles.alert} role="alert"><p>{submissionErrorText(mutation.error)} Все введённые данные сохранены.</p>{Object.entries(form.formState.errors).map(([name, error]) => error?.message ? <span key={name}>{fieldLabels[name as keyof Values] ?? name}: {error.message}</span> : null)}{mutation.error instanceof ApiError && mutation.error.kind === 'auth' && <a href="/">Открыть приложение MAX</a>}</div>}
      <Button disabled={mutation.isPending || Boolean(poster.file && !poster.imageId)} state={mutation.isPending ? 'loading' : 'idle'} loadingLabel="Отправляем…" onClick={() => form.handleSubmit((data) => mutation.mutate(data))()}>Отправить на модерацию</Button><Button tone="secondary" disabled={mutation.isPending} onClick={() => setReview(false)}>Вернуться к форме</Button>
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
