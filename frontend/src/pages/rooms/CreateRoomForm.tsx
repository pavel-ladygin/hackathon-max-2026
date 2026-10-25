import { timeZoneLabel } from '../../shared/lib/timeZoneLabel'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import type { CategorySlug, PublicPlan } from '../../shared/api/types'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import { Button } from '../../shared/ui'
import { CustomSelect } from '../../shared/ui/CustomSelect'
import { ChoiceChips } from '../../shared/ui/ChoiceChips'
import { MoneyInput } from '../../shared/ui/MoneyInput'
import { RoomDateChoices } from '../../shared/ui/RoomDateChoices'
import { roomErrorMessage } from '../../features/rooms/errors'
import { budgetToMinor, cityToday, isCanonicalDate, publicPlanSchema, resolveCreateCity, ROOM_CITIES, roomCity, TIME_SLOT_OPTIONS } from './public-plan'
import styles from './public-plan.module.css'

type PublicPlanFields = {
  dates: string[]
  time_slots: PublicPlan['time_slots']
  category_slugs: CategorySlug[]
  budget: string
  anyTime: boolean
}

type Draft = { version: 1; cityId: string; citySelectedExplicitly: boolean; profileCityId?: string | null; name: string; publicPlanEnabled: boolean; plan: PublicPlanFields }
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
function readDraft(key: string, currentProfileCityId: string | null): Draft | null {
  try {
    const raw = window.sessionStorage.getItem(key)
    if (!raw) return null
    const draft = JSON.parse(raw) as Partial<Draft>
    if (draft.version !== 1 || typeof draft.cityId !== 'string' || typeof draft.name !== 'string' || typeof draft.publicPlanEnabled !== 'boolean' || !draft.plan) return null
    if (!Array.isArray(draft.plan.dates) || !Array.isArray(draft.plan.time_slots) || !Array.isArray(draft.plan.category_slugs) || typeof draft.plan.budget !== 'string' || typeof draft.plan.anyTime !== 'boolean') return null
    const saved = draft as Draft
    const savedProfileCityId = typeof saved.profileCityId === 'string' ? saved.profileCityId : saved.cityId
    const profileMatches = currentProfileCityId === null
      ? saved.profileCityId == null
      : savedProfileCityId === currentProfileCityId
    return { ...saved, citySelectedExplicitly: saved.citySelectedExplicitly === true && profileMatches }
  } catch {
    return null
  }
}

export function CreateRoomForm({ profileCityId, draftKey, pending, success, error, onSubmit }: CreateRoomFormProps) {
  const [restored] = useState(() => readDraft(draftKey, profileCityId ?? null))
  const [explicitCityId, setExplicitCityId] = useState<string | null>(() => restored?.citySelectedExplicitly ? restored.cityId : null)
  const cityId = explicitCityId ?? resolveCreateCity(null, profileCityId)
  const [name, setName] = useState(() => restored?.name ?? '')
  const [publicPlanEnabled, setPublicPlanEnabled] = useState(() => restored?.publicPlanEnabled ?? false)
  const [plan, setPlan] = useState<PublicPlanFields>(() => restored?.plan ?? defaultPlanFields())
  const [errors, setErrors] = useState<FieldErrors>({})
  const [cityNow, setCityNow] = useState(() => new Date())
  const submitLocked = useRef(false)
  const formRef = useRef<HTMLFormElement>(null)
  const city = roomCity(cityId)
  const today = city ? cityToday(city.timezone, cityNow) : null
  const knownCity = Boolean(city)
  const cityReady = Boolean(city?.ready)

  useEffect(() => {
    if (!pending) submitLocked.current = false
  }, [pending])

  useEffect(() => {
    const refresh = () => setCityNow(new Date())
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
    const draft: Draft = { version: 1, cityId, citySelectedExplicitly: explicitCityId !== null, profileCityId: profileCityId ?? null, name, publicPlanEnabled, plan }
    try { window.sessionStorage.setItem(draftKey, JSON.stringify(draft)) } catch { /* storage can be unavailable in embedded browsers */ }
  }, [cityId, draftKey, explicitCityId, name, plan, profileCityId, publicPlanEnabled])

  const changePlan = (patch: Partial<PublicPlanFields>) => {
    setPlan((current) => ({ ...current, ...patch }))
    setErrors((current) => ({ ...current, dates: undefined, time: undefined, categories: undefined, budget: undefined }))
  }

  const validate = (): { errors: FieldErrors; public_plan?: PublicPlan } => {
    const next: FieldErrors = {}
    const submitToday = city ? cityToday(city.timezone) : null
    const normalizedName = name.trim()
    if (!normalizedName) next.name = 'Введите название комнаты'
    else if (normalizedName.length > 80) next.name = 'Не больше 80 символов'
    if (!cityReady) next.city = knownCity ? 'Выберите город с доступным каталогом' : 'Выберите доступный город'
    if (!publicPlanEnabled) return { errors: next }

    if (!city || !submitToday) next.city = 'Сначала выберите город с известным часовым поясом'
    if (!plan.dates.length) next.dates = 'Добавьте хотя бы одну дату'
    else if (plan.dates.length > 14 || new Set(plan.dates).size !== plan.dates.length || plan.dates.some((date) => !isCanonicalDate(date) || date < (submitToday ?? '9999-12-31'))) next.dates = 'Выберите от 1 до 14 будущих дат без повторов'
    if (!plan.anyTime && !plan.time_slots.length) next.time = 'Выберите время или отметьте «Любое время»'
    if (!plan.category_slugs.length) next.categories = 'Выберите хотя бы одну категорию'
    const budget = budgetToMinor(plan.budget)
    if (budget === null) next.budget = 'Введите сумму от 0 до 1 000 000 ₽ с точностью до копейки'

    const result = publicPlanSchema.safeParse({
      dates: plan.dates,
      time_slots: plan.anyTime ? [] : plan.time_slots,
      category_slugs: plan.category_slugs,
      budget_max_minor: budget,
    })
    if (!result.success && !Object.keys(next).length) next.budget = 'Проверьте заполнение публичного предложения'
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
        <input id="create-room-name" className={styles.input} value={name} placeholder="Введите название комнаты" maxLength={80} autoComplete="off" aria-invalid={Boolean(errors.name)} aria-describedby={fieldDescription('create-room-name', errors.name)} onChange={(event) => { setName(event.target.value); setErrors((current) => ({ ...current, name: undefined })) }} />
        {errors.name ? <span id="create-room-name-error" className={styles.error} role="alert">{errors.name}</span> : null}
      </label>

      <div className={styles.field}>
        <CustomSelect id="create-room-city" label="Город комнаты" value={cityId} placeholder={invalidCityProfile ? 'Город профиля не поддерживается — выберите вручную' : 'Выберите город'} error={errors.city} options={[...(!knownCity ? [{ value: cityId, label: invalidCityProfile ? 'Город профиля не поддерживается — выберите вручную' : 'Сохранённый город недоступен — выберите вручную', disabled: true }] : []), { value: '', label: 'Выберите город' }, ...ROOM_CITIES.map((item) => ({ value: item.id, label: item.ready ? item.name : `${item.name} — город готовится`, disabled: !item.ready }))]} onChange={(value) => { setExplicitCityId(value); setErrors((current) => ({ ...current, city: undefined, dates: undefined })) }} />
        {!knownCity ? <span id="create-room-city-help" className={styles.hint}>Город из профиля пока недоступен. Выберите город для этой комнаты</span> : null}
        {city && !city.ready ? <span className={styles.hint}>Каталог для Казани готовится. Создать комнату в этом городе пока нельзя</span> : null}
        {city ? <span className={styles.hint}>{timeZoneLabel(city.timezone)}</span> : null}
        {errors.city ? <span id="create-room-city-error" className={styles.error} role="alert">{errors.city}</span> : null}
      </div>

      <section className={styles.planSection} aria-labelledby="public-plan-heading">
        <label className={styles.enablePlan}>
          <input type="checkbox" checked={publicPlanEnabled} onChange={(event) => { setPublicPlanEnabled(event.target.checked); setErrors({}) }} />
          <span><strong id="public-plan-heading">Добавить публичное предложение</strong><small>Его увидит приглашённый до вступления. После подключения каждый отдельно укажет свои условия</small></span>
        </label>

        {publicPlanEnabled ? <div className={styles.planFields}>
          <fieldset className={styles.fieldset} aria-describedby={errors.dates ? 'create-plan-date-input-error' : 'create-plan-date-input-help'}>
            <legend>Подходящие даты</legend>
            <RoomDateChoices id="create-plan-date-input" selected={plan.dates} onChange={(dates) => changePlan({ dates })} min={today ?? undefined} error={errors.dates} disabled={pending || success || !cityReady} />
            {!plan.dates.length ? <p className={styles.hint}>Даты не выбраны</p> : null}
          </fieldset>

          <fieldset id="create-plan-time" className={styles.fieldset} tabIndex={-1} aria-invalid={Boolean(errors.time)} aria-describedby={errors.time ? 'create-plan-time-error' : undefined}>
            <legend>Время</legend>
            <ChoiceChips options={[...TIME_SLOT_OPTIONS.map(({ value, label }) => [value, label] as [string, string]), ['any', 'Любое время']]} selected={[...plan.time_slots, ...(plan.anyTime ? ['any'] : [])]} onToggle={(value) => value === 'any' ? changePlan({ anyTime: !plan.anyTime, time_slots: plan.anyTime ? plan.time_slots : [] }) : changePlan({ anyTime: false, time_slots: plan.time_slots.includes(value as PublicPlan['time_slots'][number]) ? plan.time_slots.filter((slot) => slot !== value) : [...plan.time_slots, value as PublicPlan['time_slots'][number]] })} className={styles.choices} />
            {errors.time ? <span id="create-plan-time-error" className={styles.error} role="alert">{errors.time}</span> : null}
          </fieldset>

          <fieldset id="create-plan-categories" className={styles.fieldset} tabIndex={-1} aria-invalid={Boolean(errors.categories)} aria-describedby={errors.categories ? 'create-plan-categories-error' : undefined}>
            <legend>Категории</legend>
            <ChoiceChips options={CATEGORY_OPTIONS.map(({ slug, label }) => [slug, label] as [CategorySlug, string])} selected={plan.category_slugs} onToggle={(value) => changePlan({ category_slugs: plan.category_slugs.includes(value) ? plan.category_slugs.filter((item) => item !== value) : [...plan.category_slugs, value] })} className={styles.choices} />
            {errors.categories ? <span id="create-plan-categories-error" className={styles.error} role="alert">{errors.categories}</span> : null}
          </fieldset>

          <label className={styles.field} htmlFor="create-plan-budget">
            <span>Бюджет на человека · максимум</span>
            <MoneyInput id="create-plan-budget" suffix="₽" value={plan.budget} invalid={Boolean(errors.budget)} describedBy={errors.budget ? 'create-plan-budget-error' : 'create-plan-budget-help'} onChange={(budget) => changePlan({ budget })} />
            <span id="create-plan-budget-help" className={styles.hint}>От 0 до 1 000 000 ₽. Можно указать копейки</span>
            {errors.budget ? <span id="create-plan-budget-error" className={styles.error} role="alert">{errors.budget}</span> : null}
          </label>
          <p className={styles.immutableNote}>Предложение станет неизменяемым после создания комнаты</p>
        </div> : null}
      </section>

      {error ? <p className={styles.error} role="alert">{roomErrorMessage(error, 'Не удалось создать комнату').replace(/\.+$/, '')}</p> : null}
      <Button type="submit" disabled={pending || success || !cityReady} state={success ? 'success' : pending ? 'loading' : 'idle'} loadingLabel="Создаём…" successLabel="Комната создана">Создать комнату</Button>
    </form>
  )
}
