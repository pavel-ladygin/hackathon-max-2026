import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useNavigate } from 'react-router-dom'
import { z } from 'zod'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { apiClient } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { CategorySlug, DayType, TimeSlot } from '../../shared/api/types'
import { Button, Chip, ChipGroup, ErrorState, FieldError, PageContent, PageShell, ScreenSkeleton, TopBar } from '../../shared/ui'
import styles from '../pages.module.css'
import notificationStyles from './preferences.module.css'

const MOSCOW_CITY_ID = 'a0f625ee-2154-5a45-8afe-37adf955ec24'
const categories: Array<[CategorySlug, string]> = [['concerts', 'Концерты'], ['cinema', 'Кино'], ['theatre', 'Театр'], ['standup', 'Стендап'], ['exhibitions', 'Выставки'], ['sports', 'Спорт'], ['food', 'Еда'], ['parties', 'Вечеринки'], ['festivals', 'Фестивали'], ['walks', 'Прогулки'], ['other', 'Другое']]
const schema = z.object({
  budget: z.coerce.number().min(0).max(1_000_000),
  days: z.array(z.enum(['weekday', 'weekend'])),
  times: z.array(z.enum(['morning', 'day', 'evening', 'night'])),
})
type FormValues = z.infer<typeof schema>

export function PreferencesPage() {
  const navigate = useNavigate()
  const bootstrap = useBootstrap()
  const client = useQueryClient()
  const initial = bootstrap.data?.preferences
  const [interests, setInterests] = useState<CategorySlug[]>(() => initial?.interestSlugs ?? ['concerts'])
  const [dailyNotificationsOverride, setDailyNotificationsOverride] = useState<boolean | null>(null)
  const dailyNotificationsEnabled = dailyNotificationsOverride ?? bootstrap.data?.dailyNotificationsEnabled ?? false
  const form = useForm<FormValues>({ resolver: zodResolver(schema), defaultValues: { budget: (initial?.budgetMaxMinor ?? 350_000) / 100, days: initial?.usualDayTypes ?? ['weekend'], times: initial?.usualTimeSlots ?? ['evening'] } })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const days = useWatch({ control: form.control, name: 'days' })
  const times = useWatch({ control: form.control, name: 'times' })
  const saveNotifications = useMutation({
    mutationFn: (enabled: boolean) => apiClient.updateNotificationPreferences({ daily_notifications_enabled: enabled }),
    onMutate: (enabled) => setDailyNotificationsOverride(enabled),
    onSuccess: (result) => {
      setDailyNotificationsOverride(result.daily_notifications_enabled)
      client.setQueriesData({ queryKey: ['bootstrap'] }, (data: typeof bootstrap.data) => data ? { ...data, dailyNotificationsEnabled: result.daily_notifications_enabled } : data)
    },
    onError: () => setDailyNotificationsOverride(null),
  })
  const save = useMutation({
    mutationFn: (values: FormValues) => apiClient.replacePreferences({ city_id: initial?.cityId ?? bootstrap.data?.user.cityId ?? MOSCOW_CITY_ID, interest_slugs: interests, budget_max_minor: values.budget * 100, usual_day_types: values.days, usual_time_slots: values.times }),
    onSuccess: async () => { await client.invalidateQueries({ queryKey: ['bootstrap'] }); await client.invalidateQueries({ queryKey: ['home-feed'] }); navigate('/') },
    onError: (error) => {
      if (!(error instanceof ApiError)) return
      if (error.fieldErrors.budget_max_minor) form.setError('budget', { message: error.fieldErrors.budget_max_minor }, { shouldFocus: true })
      if (error.fieldErrors.usual_day_types) form.setError('days', { message: error.fieldErrors.usual_day_types })
      if (error.fieldErrors.usual_time_slots) form.setError('times', { message: error.fieldErrors.usual_time_slots })
    },
  })
  if (bootstrap.isPending) return <ScreenSkeleton variant="form" label="Загружаем предпочтения…" />
  if (bootstrap.isError || !bootstrap.data) return <ErrorState title="Настройки не загрузились" action={<Button onClick={() => void bootstrap.refetch()}>Повторить</Button>} />
  const toggle = <T,>(list: T[], value: T) => list.includes(value) ? list.filter((item) => item !== value) : [...list, value]
  return <PageShell><TopBar title="Предпочтения" onBack={() => navigate(-1)} /><PageContent className={styles.narrow}>
    <h1 className={styles.title}>Настройте афишу</h1><p className={styles.subtitle}>Эти параметры влияют на персональные рекомендации. Условия отдельной встречи задаются приватно в комнате.</p>
    <section className={notificationStyles.card} aria-busy={saveNotifications.isPending}>
      <label className={notificationStyles.option} htmlFor="preferences-daily-notifications">
        <span className={notificationStyles.icon} aria-hidden="true"><svg viewBox="0 0 24 24" fill="none"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg></span>
        <span className={notificationStyles.copy}><strong id="preferences-notifications-title">Ежедневные идеи от бота</strong><span>Короткое приглашение найти планы с друзьями · в 12:00 по Москве</span></span>
        <span className={notificationStyles.hint} id="preferences-notifications-hint">Можно выключить в любой момент</span>
        <input id="preferences-daily-notifications" className={notificationStyles.switch} type="checkbox" role="switch" aria-labelledby="preferences-notifications-title" aria-describedby="preferences-notifications-hint" checked={dailyNotificationsEnabled} disabled={saveNotifications.isPending} onChange={(event) => { saveNotifications.reset(); saveNotifications.mutate(event.currentTarget.checked) }} />
      </label>
      {saveNotifications.isError ? <p className={notificationStyles.error} role="alert">Не удалось изменить уведомления. Попробуйте ещё раз.</p> : null}
    </section>
    <form className={styles.formStack} aria-busy={save.isPending} onSubmit={form.handleSubmit((values) => interests.length > 0 && save.mutate(values))}>
      <fieldset className={styles.fieldLabel} aria-invalid={interests.length === 0}><legend>Интересы</legend><ChipGroup>{categories.map(([value, label]) => <Chip key={value} selected={interests.includes(value)} onClick={() => setInterests((current) => toggle(current, value))}>{label}</Chip>)}</ChipGroup><FieldError id="preferences-interests-error">{interests.length === 0 ? 'Выберите хотя бы один интерес.' : null}</FieldError></fieldset>
      <label className={styles.fieldLabel} htmlFor="preferences-budget">Бюджет · до {budget.toLocaleString('ru-RU')} ₽<input id="preferences-budget" className={styles.range} type="range" min="0" max="10000" step="500" aria-invalid={Boolean(form.formState.errors.budget)} {...form.register('budget')} /><FieldError id="preferences-budget-error">{form.formState.errors.budget?.message}</FieldError></label>
      <fieldset className={styles.fieldLabel} aria-invalid={Boolean(form.formState.errors.days)}><legend>Обычно удобно</legend><ChipGroup>{([['weekday', 'Будни'], ['weekend', 'Выходные']] as Array<[DayType, string]>).map(([value, label]) => <Chip key={value} selected={days.includes(value)} onClick={() => form.setValue('days', toggle(days, value), { shouldValidate: true })}>{label}</Chip>)}</ChipGroup><FieldError id="preferences-days-error">{form.formState.errors.days?.message}</FieldError></fieldset>
      <fieldset className={styles.fieldLabel} aria-invalid={Boolean(form.formState.errors.times)}><legend>Время</legend><ChipGroup>{([['morning', 'Утро'], ['day', 'День'], ['evening', 'Вечер'], ['night', 'Ночь']] as Array<[TimeSlot, string]>).map(([value, label]) => <Chip key={value} selected={times.includes(value)} onClick={() => form.setValue('times', toggle(times, value), { shouldValidate: true })}>{label}</Chip>)}</ChipGroup><FieldError id="preferences-times-error">{form.formState.errors.times?.message}</FieldError></fieldset>
      {save.isError ? <p className={styles.error} role="alert">Не удалось сохранить настройки. Данные формы не потеряны.</p> : null}
      <div className={styles.footer}><Button type="submit" disabled={save.isPending || interests.length === 0}>{save.isPending ? 'Сохраняем…' : 'Сохранить'}</Button></div>
    </form>
  </PageContent></PageShell>
}
