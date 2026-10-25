import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useNavigate, useParams } from 'react-router-dom'
import { z } from 'zod'
import { apiClient } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { CategorySlug, TimeSlot } from '../../shared/api/types'
import { useBootstrap } from '../../features/auth/useBootstrap'
import { Button, Chip, ChipGroup, PageContent, PageShell } from '../../shared/ui/index'
import { CityPicker } from '../../shared/ui/CityPicker'
import styles from '../pages.module.css'
import onboardingStyles from './onboarding.module.css'
import { cityById, MOSCOW_CITY_ID } from '../../shared/lib/cities'
import { track } from '../../shared/analytics/client'
import { launchContext } from '../../shared/platform/max/launchContext'

const options: Array<{ slug: CategorySlug; label: string; icon: string }> = [
  { slug: 'concerts', label: 'Концерты', icon: '♫' },
  { slug: 'cinema', label: 'Кино', icon: '◉' },
  { slug: 'theatre', label: 'Театр', icon: '◫' },
  { slug: 'standup', label: 'Стендап', icon: '◡' },
  { slug: 'exhibitions', label: 'Выставки', icon: '◇' },
  { slug: 'sports', label: 'Спорт', icon: '↗' },
  { slug: 'food', label: 'Еда', icon: '○' },
  { slug: 'parties', label: 'Вечеринки', icon: '✦' },
  { slug: 'festivals', label: 'Фестивали', icon: '✺' },
]

const schema = z.object({
  budget: z.coerce.number().min(0).max(1_000_000),
  usual: z.array(z.enum(['weekday', 'weekend'])),
  times: z.array(z.enum(['morning', 'day', 'evening', 'night'])),
})
type ProfileForm = z.infer<typeof schema>

export function OnboardingPage() {
  const { step } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const bootstrap = useBootstrap()
  const stored = bootstrap.data?.preferences
  const [selectedCityId, setSelectedCityId] = useState<string | null>(null)
  const [cityPickerOpen, setCityPickerOpen] = useState(false)
  const cityId = selectedCityId ?? stored?.cityId ?? bootstrap.data?.user.cityId ?? MOSCOW_CITY_ID
  const [interests, setInterests] = useState<CategorySlug[]>(() => stored?.interestSlugs ?? ['concerts', 'exhibitions', 'food'])
  const form = useForm<ProfileForm>({ resolver: zodResolver(schema), defaultValues: { budget: (stored?.budgetMaxMinor ?? 350_000) / 100, usual: stored?.usualDayTypes ?? ['weekend'], times: stored?.usualTimeSlots ?? ['evening'] } })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const usual = useWatch({ control: form.control, name: 'usual' })
  const times = useWatch({ control: form.control, name: 'times' })
  useEffect(() => { track('onboarding_started') }, [step])
  const save = useMutation({
    onMutate: () => launchContext.getSnapshot().revision,
    mutationFn: (values: ProfileForm) => apiClient.replacePreferences({
      city_id: cityId,
      interest_slugs: interests,
      budget_max_minor: values.budget * 100,
      usual_day_types: values.usual,
      usual_time_slots: values.times,
    }),
    onSuccess: async (_data, _values, launchRevision) => {
      track('onboarding_completed')
      await queryClient.refetchQueries({ queryKey: ['bootstrap'] })
      if (launchContext.getSnapshot().revision !== launchRevision) return
      navigate('/', { replace: true })
    },
    onError: (error) => {
      if (!(error instanceof ApiError)) return
      if (error.fieldErrors.budget_max_minor) form.setError('budget', { message: error.fieldErrors.budget_max_minor }, { shouldFocus: true })
      if (error.fieldErrors.usual_day_types) form.setError('usual', { message: error.fieldErrors.usual_day_types })
      if (error.fieldErrors.usual_time_slots) form.setError('times', { message: error.fieldErrors.usual_time_slots })
    },
  })

  const toggleInterest = (slug: CategorySlug) => setInterests((current) => current.includes(slug) ? current.filter((item) => item !== slug) : [...current, slug])
  const isInterests = step !== 'profile'

  return (
    <PageShell>
      <PageContent className={styles.narrow}>
        <div className={styles.heroHeader}><span className={styles.brand}>вместе<span className={styles.brandAccent}>•</span></span><span className={styles.eyebrow}>{isInterests ? '1 / 2' : '2 / 2'}</span></div>
        {isInterests ? (
          <>
            <p className={styles.eyebrow}>ЗНАКОМСТВО</p>
            <h1 className={styles.title}>Что вам интересно?</h1>
            <p className={styles.subtitle}>Выберите несколько тем — они станут первым сигналом для персональной афиши.</p>
            <section className={styles.section}>
              <ChipGroup label="Интересы">
                {options.map((item) => <Chip key={item.slug} selected={interests.includes(item.slug)} onClick={() => toggleInterest(item.slug)}>{item.icon} {item.label}</Chip>)}
              </ChipGroup>
            </section>
            <div className={styles.footer}><Button disabled={interests.length === 0} onClick={() => navigate('/onboarding/profile')}>Продолжить</Button></div>
          </>
        ) : (
          <form onSubmit={form.handleSubmit((values) => save.mutate(values))}>
            <p className={styles.eyebrow}>ВАШ ПРОФИЛЬ</p>
            <h1 className={styles.title}>Настроим подборку</h1>
            <p className={`${styles.subtitle} ${onboardingStyles.profileIntro}`}>Пара деталей поможет точнее ранжировать события и быстрее находить общее.</p>
            <div className={styles.formStack}>
              <div className={styles.fieldLabel}>
                <label htmlFor="onboarding-city">Ваш город</label>
                <button id="onboarding-city" className={onboardingStyles.cityTrigger} type="button" aria-haspopup="dialog" aria-expanded={cityPickerOpen} aria-describedby="city-help" disabled={save.isPending} onClick={() => setCityPickerOpen(true)}>
                  <span>{cityById(cityId)?.name ?? 'Выбранный город'}</span>
                  <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m7 10 5 5 5-5" /></svg>
                </button>
                <span id="city-help" className={onboardingStyles.cityHelp}>Выберите город для вашей афиши</span>
              </div>
              <label className={styles.fieldLabel}>Комфортный бюджет · до {budget.toLocaleString('ru')} ₽<input className={styles.range} type="range" min="0" max="10000" step="500" aria-invalid={Boolean(form.formState.errors.budget)} aria-describedby="budget-error" {...form.register('budget')} /></label>
              {form.formState.errors.budget ? <p id="budget-error" className={styles.fieldError}>Укажите корректный бюджет.</p> : null}
              <fieldset className={styles.fieldLabel} aria-invalid={Boolean(form.formState.errors.usual)}><legend>Когда обычно удобно</legend><ChipGroup label="Дни недели"><Chip selected={usual.includes('weekday')} onClick={() => form.setValue('usual', toggleValue(usual, 'weekday'), { shouldValidate: true })}>Будни</Chip><Chip selected={usual.includes('weekend')} onClick={() => form.setValue('usual', toggleValue(usual, 'weekend'), { shouldValidate: true })}>Выходные</Chip></ChipGroup>{form.formState.errors.usual ? <p className={styles.fieldError}>Выберите хотя бы один вариант.</p> : null}</fieldset>
              <fieldset className={styles.fieldLabel} aria-invalid={Boolean(form.formState.errors.times)}><legend>Время суток</legend><ChipGroup label="Время суток">{([['morning', 'Утро'], ['day', 'День'], ['evening', 'Вечер'], ['night', 'Ночь']] as Array<[TimeSlot, string]>).map(([value, label]) => <Chip key={value} selected={times.includes(value)} onClick={() => form.setValue('times', toggleValue(times, value), { shouldValidate: true })}>{label}</Chip>)}</ChipGroup>{form.formState.errors.times ? <p className={styles.fieldError} role="alert">{form.formState.errors.times.message}</p> : null}</fieldset>
            </div>
            {save.isError ? <p className={styles.error}>Не удалось сохранить настройки. Попробуйте ещё раз.</p> : null}
            <div className={styles.footer}><Button type="submit" disabled={save.isPending}>{save.isPending ? 'Сохраняем…' : 'Сформировать подборку'}</Button></div>
          </form>
        )}
        {cityPickerOpen ? <CityPicker cityId={cityId} onSelect={async (id) => { setSelectedCityId(id) }} onClose={() => setCityPickerOpen(false)} /> : null}
      </PageContent>
    </PageShell>
  )
}

function toggleValue<T extends string>(items: T[], value: T) {
  return items.includes(value) ? items.filter((item) => item !== value) : [...items, value]
}
