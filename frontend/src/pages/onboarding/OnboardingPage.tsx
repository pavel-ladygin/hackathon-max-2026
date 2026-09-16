import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useNavigate, useParams } from 'react-router-dom'
import { z } from 'zod'
import { apiClient } from '../../shared/api/client'
import type { CategorySlug } from '../../shared/api/types'
import { Button, Chip, ChipGroup, PageContent, PageShell } from '../../shared/ui/index'
import styles from '../pages.module.css'

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
  budget: z.coerce.number().min(500).max(10_000),
  usual: z.array(z.enum(['weekday', 'weekend'])).min(1),
})
type ProfileForm = z.infer<typeof schema>

export function OnboardingPage() {
  const { step } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [interests, setInterests] = useState<CategorySlug[]>(['concerts', 'exhibitions', 'food'])
  const form = useForm<ProfileForm>({ resolver: zodResolver(schema), defaultValues: { budget: 3_500, usual: ['weekend'] } })
  const budget = useWatch({ control: form.control, name: 'budget' })
  const usual = useWatch({ control: form.control, name: 'usual' })
  const save = useMutation({
    mutationFn: (values: ProfileForm) => apiClient.replacePreferences({
      city_id: 'aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa',
      interest_slugs: interests,
      budget_max_minor: values.budget * 100,
      usual_day_types: values.usual,
      usual_time_slots: ['evening'],
    }),
    onSuccess: async () => {
      await queryClient.refetchQueries({ queryKey: ['bootstrap'] })
      navigate('/', { replace: true })
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
            <p className={styles.subtitle}>Пара деталей поможет точнее ранжировать события и быстрее находить общее.</p>
            <div className={styles.formStack}>
              <label className={styles.fieldLabel}>Ваш город<input className={styles.input} value="Москва" readOnly /></label>
              <label className={styles.fieldLabel}>Комфортный бюджет · до {budget.toLocaleString('ru')} ₽<input className={styles.range} type="range" min="500" max="10000" step="500" {...form.register('budget')} /></label>
              <fieldset className={styles.fieldLabel}><legend>Когда обычно удобно</legend><ChipGroup><Chip selected={usual.includes('weekday')} onClick={() => form.setValue('usual', toggleValue(usual, 'weekday'))}>Будни</Chip><Chip selected={usual.includes('weekend')} onClick={() => form.setValue('usual', toggleValue(usual, 'weekend'))}>Выходные</Chip></ChipGroup></fieldset>
            </div>
            {save.isError ? <p className={styles.error}>Не удалось сохранить настройки. Попробуйте ещё раз.</p> : null}
            <div className={styles.footer}><Button type="submit" disabled={save.isPending}>{save.isPending ? 'Сохраняем…' : 'Сформировать подборку'}</Button></div>
          </form>
        )}
      </PageContent>
    </PageShell>
  )
}

function toggleValue<T extends string>(items: T[], value: T) {
  return items.includes(value) ? items.filter((item) => item !== value) : [...items, value]
}
