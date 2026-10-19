import { useEffect, useRef, useState, type FormEvent } from 'react'
import type { CategorySlug, PublicPlan } from '../../shared/api/types'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import { Button } from '../../shared/ui'
import { roomErrorMessage } from '../../features/rooms/errors'
import { budgetToMinor, cityToday, formatPlanDate, isCanonicalDate, publicPlanSchema, publicPlanPreset, resolveCreateCity, ROOM_CITIES, roomCity, TIME_SLOT_OPTIONS } from './public-plan'
import styles from './public-plan.module.css'

type PublicPlanFields = {
  dates: string[]
  time_slots: PublicPlan['time_slots']
  category_slugs: CategorySlug[]
  budget: string
  anyTime: boolean
}

type Draft = { version: 1; cityId: string; citySelectedExplicitly: boolean; name: string; publicPlanEnabled: boolean; plan: PublicPlanFields }
type CreateValues = { name: string; city_id: string; public_plan?: PublicPlan }
type FieldErrors = Partial<Record<'name' | 'city' | 'dates' | 'time' | 'categories' | 'budget', string>>

export type CreateRoomFormProps = {
  profileCityId?: string | null
  draftKey: string
  pending: boolean
  success: boolean
  error?: unknown
  onSubmit: (values: CreateValues) => void
}

const defaultPlanFields = (): PublicPlanFields => ({ dates: [], time_slots: [], category_slugs: [], budget: '', anyTime: false })
const DEFAULT_ROOM_NAME = 'Куда идём в субботу?'

function readDraft(key: string): Draft | null {
  try {
    const raw = window.sessionStorage.getItem(key)
    if (!raw) return null
    const draft = JSON.parse(raw) as Partial<Draft>
    if (draft.version !== 1 || typeof draft.cityId !== 'string' || typeof draft.name !== 'string' || typeof draft.publicPlanEnabled !== 'boolean' || !draft.plan) return null
    if (!Array.isArray(draft.plan.dates) || !Array.isArray(draft.plan.time_slots) || !Array.isArray(draft.plan.category_slugs) || typeof draft.plan.budget !== 'string' || typeof draft.plan.anyTime !== 'boolean') return null
    return { ...draft as Draft, citySelectedExplicitly: draft.citySelectedExplicitly === true }
  } catch {
    return null
  }
}

export function CreateRoomForm({ profileCityId, draftKey, pending, success, error, onSubmit }: CreateRoomFormProps) {
  const [restored] = useState(() => readDraft(draftKey))
  const [explicitCityId, setExplicitCityId] = useState<string | null>(() => restored?.citySelectedExplicitly ? restored.cityId : null)
  const cityId = explicitCityId ?? resolveCreateCity(null, profileCityId)
  const [name, setName] = useState(() => restored?.name ?? DEFAULT_ROOM_NAME)
  const [publicPlanEnabled, setPublicPlanEnabled] = useState(() => restored?.publicPlanEnabled ?? false)
  const [plan, setPlan] = useState<PublicPlanFields>(() => restored?.plan ?? defaultPlanFields())
  const [dateInput, setDateInput] = useState('')
  const [errors, setErrors] = useState<FieldErrors>({})
  const [presetNow, setPresetNow] = useState(() => new Date())
  const [presetFeedback, setPresetFeedback] = useState('')
  const submitLocked = useRef(false)
  const formRef = useRef<HTMLFormElement>(null)
  const city = roomCity(cityId)
  const today = city ? cityToday(city.timezone, presetNow) : null
  const knownCity = Boolean(city)
  const cityReady = Boolean(city?.ready)

  useEffect(() => {
    if (!pending) submitLocked.current = false
  }, [pending])

  useEffect(() => {
    const refresh = () => setPresetNow(new Date())
    const timer = window.setInterval(refresh, 30_000)
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('focus', refresh)
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [])

  useEffect(() => {
    const draft: Draft = { version: 1, cityId, citySelectedExplicitly: explicitCityId !== null, name, publicPlanEnabled, plan }
    try { window.sessionStorage.setItem(draftKey, JSON.stringify(draft)) } catch { /* storage can be unavailable in embedded browsers */ }
  }, [cityId, draftKey, explicitCityId, name, plan, publicPlanEnabled])

  const changePlan = (patch: Partial<PublicPlanFields>) => {
    setPlan((current) => ({ ...current, ...patch }))
    setErrors((current) => ({ ...current, dates: undefined, time: undefined, categories: undefined, budget: undefined }))
  }

  const applyPreset = (kind: Parameters<typeof publicPlanPreset>[0]) => {
    if (pending || success || !city?.ready) return
    const now = new Date()
    setPresetNow(now)
    const patch = publicPlanPreset(kind, city.timezone, now)
    if (!patch) {
      setPresetFeedback('Этот вариант уже недоступен по времени города. Выберите дату и время самостоятельно.')
      return
    }
    const currentBudget = budgetToMinor(plan.budget)
    changePlan({
      ...(patch.dates ? { dates: patch.dates } : {}),
      ...(patch.time_slots ? { time_slots: patch.time_slots, anyTime: false } : {}),
      ...(patch.budget_max_minor !== undefined ? { budget: String(Math.min(currentBudget ?? patch.budget_max_minor, patch.budget_max_minor) / 100) } : {}),
    })
    if (patch.dates) setDateInput('')
    setPresetFeedback(kind === 'tonight' ? 'Выбраны сегодняшняя дата и вечер по времени города.' : kind === 'weekend' ? 'Выбраны ближайшие оставшиеся выходные. Время и бюджет сохранены.' : currentBudget !== null && currentBudget < 100_000 ? 'Ваш более низкий бюджет сохранён.' : 'Лимит установлен до 1 000 ₽.')
  }

  const tonightAvailable = Boolean(city?.ready && publicPlanPreset('tonight', city.timezone, presetNow))

  const addDate = () => {
    const currentToday = city ? cityToday(city.timezone) : null
    if (!isCanonicalDate(dateInput) || !currentToday || dateInput < currentToday || plan.dates.includes(dateInput) || plan.dates.length >= 14) {
      setErrors((current) => ({ ...current, dates: plan.dates.length >= 14 ? 'Можно выбрать не больше 14 дат.' : 'Выберите корректную будущую дату без повторов.' }))
      return
    }
    changePlan({ dates: [...plan.dates, dateInput].sort() })
    setDateInput('')
  }

  const validate = (): { errors: FieldErrors; public_plan?: PublicPlan } => {
    const next: FieldErrors = {}
    const submitToday = city ? cityToday(city.timezone) : null
    const normalizedName = name.trim()
    if (!normalizedName) next.name = 'Введите название комнаты.'
    else if (normalizedName.length > 80) next.name = 'Не больше 80 символов.'
    if (!cityReady) next.city = knownCity ? 'Выберите город с доступным каталогом.' : 'Выберите доступный город.'
    if (!publicPlanEnabled) return { errors: next }

    if (!city || !submitToday) next.city = 'Сначала выберите город с известным часовым поясом.'
    if (!plan.dates.length) next.dates = 'Добавьте хотя бы одну дату.'
    else if (plan.dates.length > 14 || new Set(plan.dates).size !== plan.dates.length || plan.dates.some((date) => !isCanonicalDate(date) || date < (submitToday ?? '9999-12-31'))) next.dates = 'Выберите от 1 до 14 будущих дат без повторов.'
    if (!plan.anyTime && !plan.time_slots.length) next.time = 'Выберите время или отметьте «Любое время».'
    if (!plan.category_slugs.length) next.categories = 'Выберите хотя бы одну категорию.'
    const budget = budgetToMinor(plan.budget)
    if (budget === null) next.budget = 'Введите сумму от 0 до 1 000 000 ₽ с точностью до копейки.'

    const result = publicPlanSchema.safeParse({
      dates: plan.dates,
      time_slots: plan.anyTime ? [] : plan.time_slots,
      category_slugs: plan.category_slugs,
      budget_max_minor: budget,
    })
    if (!result.success && !Object.keys(next).length) next.budget = 'Проверьте заполнение публичного предложения.'
    return { errors: next, public_plan: result.success ? result.data : undefined }
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pending || success || submitLocked.current) return
    const result = validate()
    setErrors(result.errors)
    if (Object.keys(result.errors).length || (publicPlanEnabled && !result.public_plan)) {
      window.requestAnimationFrame(() => formRef.current?.querySelector<HTMLElement>('[data-error-summary]')?.focus())
      return
    }
    submitLocked.current = true
    onSubmit({ name: name.trim(), city_id: cityId, ...(publicPlanEnabled && result.public_plan ? { public_plan: result.public_plan } : {}) })
  }

  const invalidCityProfile = !knownCity && cityId === profileCityId
  const fieldDescription = (id: string, message?: string) => message ? `${id}-error` : undefined

  return (
    <form ref={formRef} className={styles.form} onSubmit={submit} noValidate aria-busy={pending}>
      {Object.values(errors).some(Boolean) ? <div className={styles.errorSummary} role="alert" tabIndex={-1} data-error-summary aria-labelledby="create-room-error-title">
        <strong id="create-room-error-title">Проверьте форму перед созданием комнаты</strong>
        <ul>{(Object.entries(errors) as [keyof FieldErrors, string][]).filter(([, message]) => message).map(([field, message]) => {
          const fieldId = ({ name: 'create-room-name', city: 'create-room-city', dates: 'create-plan-date-input', time: 'create-plan-time', categories: 'create-plan-categories', budget: 'create-plan-budget' } as const)[field]
          return <li key={field}><a href={`#${fieldId}`}>{message}</a></li>
        })}</ul>
      </div> : null}
      <label className={styles.field} htmlFor="create-room-name">
        <span>Название комнаты</span>
        <input id="create-room-name" className={styles.input} value={name} maxLength={80} autoComplete="off" aria-invalid={Boolean(errors.name)} aria-describedby={fieldDescription('create-room-name', errors.name)} onChange={(event) => { setName(event.target.value); setErrors((current) => ({ ...current, name: undefined })) }} />
        {errors.name ? <span id="create-room-name-error" className={styles.error} role="alert">{errors.name}</span> : null}
      </label>

      <label className={styles.field} htmlFor="create-room-city">
        <span>Город комнаты</span>
        <select id="create-room-city" className={styles.input} value={cityId} aria-invalid={Boolean(errors.city)} aria-describedby={fieldDescription('create-room-city', errors.city) ?? (!knownCity ? 'create-room-city-help' : undefined)} onChange={(event) => { setExplicitCityId(event.target.value); setErrors((current) => ({ ...current, city: undefined, dates: undefined })) }}>
          {!knownCity ? <option value={cityId} disabled>{invalidCityProfile ? 'Город профиля не поддерживается — выберите вручную' : 'Сохранённый город недоступен — выберите вручную'}</option> : null}
          <option value="">Выберите город</option>
          {ROOM_CITIES.map((item) => <option value={item.id} key={item.id}>{item.ready ? item.name : `${item.name} — город готовится`}</option>)}
        </select>
        {!knownCity ? <span id="create-room-city-help" className={styles.hint}>Город из профиля пока недоступен. Выберите город для этой комнаты.</span> : null}
        {city && !city.ready ? <span className={styles.hint}>Каталог для Казани готовится. Создать комнату в этом городе пока нельзя.</span> : null}
        {city ? <span className={styles.hint}>Часовой пояс: {city.timezone}</span> : null}
        {errors.city ? <span id="create-room-city-error" className={styles.error} role="alert">{errors.city}</span> : null}
      </label>

      <section className={styles.planSection} aria-labelledby="public-plan-heading">
        <label className={styles.enablePlan}>
          <input type="checkbox" checked={publicPlanEnabled} onChange={(event) => { setPublicPlanEnabled(event.target.checked); setErrors({}) }} />
          <span><strong id="public-plan-heading">Добавить публичное предложение</strong><small>Его увидит приглашённый до вступления. После подключения каждый отдельно укажет свои условия.</small></span>
        </label>

        {publicPlanEnabled ? <div className={styles.planFields}>
          <fieldset className={styles.fieldset} aria-describedby="public-plan-presets-help" disabled={pending || success || !cityReady}>
            <legend>Быстрый выбор</legend>
            <p id="public-plan-presets-help" className={styles.hint}>Это ваше предложение. Проверьте выбранные поля перед созданием комнаты; друг отдельно подтвердит свои условия.</p>
            <div className={styles.presetButtons}>
              <Button type="button" tone="secondary" disabled={!tonightAvailable} onClick={() => applyPreset('tonight')}>Сегодня вечером</Button>
              <Button type="button" tone="secondary" onClick={() => applyPreset('weekend')}>Выходные</Button>
              <Button type="button" tone="secondary" onClick={() => applyPreset('budget')}>Недорого</Button>
            </div>
            <p className={styles.hint}>{!cityReady ? 'Выберите город с доступным каталогом.' : 'Вечер — с 17:00 до 22:00 по времени города. «Недорого» — до 1 000 ₽.'}</p>
            {cityReady && !tonightAvailable ? <p className={styles.hint}>Сегодняшний вечер уже закончился. Выберите другую дату.</p> : null}
            <p className={styles.hint} role="status" aria-live="polite">{presetFeedback}</p>
          </fieldset>
          <fieldset className={styles.fieldset} aria-describedby={errors.dates ? 'create-plan-dates-error' : 'create-plan-dates-help'}>
            <legend>Подходящие даты</legend>
            <p id="create-plan-dates-help" className={styles.hint}>{city ? `Выбирайте даты начиная с ${today}, ${city.timezone}.` : 'Сначала выберите город с часовым поясом.'}</p>
            <div className={styles.dateAdd}>
              <input id="create-plan-date-input" className={styles.input} type="date" min={today ?? undefined} max="9999-12-31" value={dateInput} aria-label="Новая дата публичного предложения" aria-invalid={Boolean(errors.dates)} aria-describedby={errors.dates ? 'create-plan-dates-error' : 'create-plan-dates-help'} onChange={(event) => setDateInput(event.target.value)} />
              <Button type="button" tone="secondary" disabled={!dateInput || plan.dates.length >= 14} onClick={addDate}>Добавить дату</Button>
            </div>
            {plan.dates.length ? <ul className={styles.dateList}>{plan.dates.map((date) => <li key={date}><span>{formatPlanDate(date)}</span><button type="button" className={styles.removeDate} aria-label={`Удалить дату ${formatPlanDate(date)}`} onClick={() => changePlan({ dates: plan.dates.filter((item) => item !== date) })}>Удалить</button></li>)}</ul> : <p className={styles.hint}>Даты не выбраны.</p>}
            {errors.dates ? <span id="create-plan-dates-error" className={styles.error} role="alert">{errors.dates}</span> : null}
          </fieldset>

          <fieldset id="create-plan-time" className={styles.fieldset} tabIndex={-1} aria-invalid={Boolean(errors.time)} aria-describedby={errors.time ? 'create-plan-time-error' : undefined}>
            <legend>Время</legend>
            <div className={styles.choices}>
              {TIME_SLOT_OPTIONS.map(({ value, label }) => <label className={styles.choice} key={value}><input type="checkbox" checked={plan.time_slots.includes(value)} disabled={plan.anyTime} onChange={(event) => changePlan({ time_slots: event.target.checked ? [...plan.time_slots, value] : plan.time_slots.filter((slot) => slot !== value) })} /><span>{label}</span></label>)}
              <label className={styles.choice}><input type="checkbox" checked={plan.anyTime} onChange={(event) => changePlan({ anyTime: event.target.checked, time_slots: event.target.checked ? [] : plan.time_slots })} /><span>Любое время</span></label>
            </div>
            {errors.time ? <span id="create-plan-time-error" className={styles.error} role="alert">{errors.time}</span> : null}
          </fieldset>

          <fieldset id="create-plan-categories" className={styles.fieldset} tabIndex={-1} aria-invalid={Boolean(errors.categories)} aria-describedby={errors.categories ? 'create-plan-categories-error' : undefined}>
            <legend>Категории</legend>
            <div className={styles.choices}>
              {CATEGORY_OPTIONS.map(({ slug, label }) => <label className={styles.choice} key={slug}><input type="checkbox" checked={plan.category_slugs.includes(slug)} onChange={(event) => changePlan({ category_slugs: event.target.checked ? [...plan.category_slugs, slug] : plan.category_slugs.filter((item) => item !== slug) })} /><span>{label}</span></label>)}
            </div>
            {errors.categories ? <span id="create-plan-categories-error" className={styles.error} role="alert">{errors.categories}</span> : null}
          </fieldset>

          <label className={styles.field} htmlFor="create-plan-budget">
            <span>Бюджет на человека · максимум</span>
            <div className={styles.budgetInput}><input id="create-plan-budget" className={styles.input} type="number" min="0" max="1000000" step="0.01" inputMode="decimal" value={plan.budget} aria-invalid={Boolean(errors.budget)} aria-describedby={errors.budget ? 'create-plan-budget-error' : 'create-plan-budget-help'} onChange={(event) => changePlan({ budget: event.target.value })} /><span>₽</span></div>
            <span id="create-plan-budget-help" className={styles.hint}>От 0 до 1 000 000 ₽. Можно указать копейки.</span>
            {errors.budget ? <span id="create-plan-budget-error" className={styles.error} role="alert">{errors.budget}</span> : null}
          </label>
          <p className={styles.immutableNote}>Предложение станет неизменяемым после создания комнаты.</p>
        </div> : null}
      </section>

      {error ? <p className={styles.error} role="alert">{roomErrorMessage(error, 'Не удалось создать комнату.')}</p> : null}
      <Button type="submit" disabled={pending || success || !cityReady} state={pending ? 'loading' : success ? 'success' : 'idle'} loadingLabel="Создаём…" successLabel="Комната создана">Создать комнату</Button>
    </form>
  )
}
