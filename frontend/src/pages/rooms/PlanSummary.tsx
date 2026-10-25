import { timeZoneLabel } from '../../shared/lib/timeZoneLabel'
import type { PublicPlan } from '../../shared/api/types'
import { categoryLabel, formatBudgetMinor, formatPlanDate, roomCity, TIME_SLOT_OPTIONS } from './public-plan'
import styles from './public-plan.module.css'

export function PlanSummary({ plan, cityId }: { plan?: PublicPlan | null; cityId?: string | null }) {
  const city = roomCity(cityId)
  return (
    <section className={styles.summary} aria-label="Публичное предложение комнаты">
      <div className={styles.summaryCity}>
        <span>Город</span>
        <strong>{city?.name ?? 'Город уточняется'}</strong>
        <small>{city ? timeZoneLabel(city.timezone) : 'Часовой пояс пока неизвестен'}</small>
      </div>
      {plan ? <dl className={styles.summaryDetails}>
        <div><dt>Даты</dt><dd>{plan.dates.map(formatPlanDate).join(', ')}</dd></div>
        <div><dt>Время</dt><dd>{plan.time_slots.length ? plan.time_slots.map((slot) => TIME_SLOT_OPTIONS.find((item) => item.value === slot)?.label ?? slot).join(', ') : 'Любое'}</dd></div>
        <div><dt>Категории</dt><dd>{plan.category_slugs.map(categoryLabel).join(', ')}</dd></div>
        <div><dt>Бюджет на человека</dt><dd>До {formatBudgetMinor(plan.budget_max_minor)}</dd></div>
      </dl> : <p className={styles.noPlan}>Детали встречи можно уточнить у пригласившего.</p>}
      {plan ? <p className={styles.immutableNote}>Публичное предложение задаётся при создании комнаты и не меняется. После вступления каждый участник отдельно подтверждает свои условия.</p> : null}
    </section>
  )
}
