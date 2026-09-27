import { type ReactNode, useEffect, useMemo, useState } from 'react'
import styles from './analytics.module.css'

type DailyMetric = {
  date: string
  active_users: number
  rooms_created: number
  rooms_invite_shared: number
  rooms_invite_opened: number
  rooms_joined: number
  activated_rooms: number
  rooms_matched: number
  rooms_ticket_clicked: number
  rooms_match_shown: number
  room_creation_rate: number | null
  invite_share_rate: number | null
  invite_open_rate: number | null
  invite_join_conversion: number | null
  room_activation_rate: number | null
  match_rate: number | null
  match_ticket_ctr: number | null
  median_time_to_match_seconds: number | null
  median_time_to_join_seconds: number | null
  p75_time_to_join_seconds: number | null
  p90_time_to_join_seconds: number | null
  median_swipes_to_match: number | null
  second_room_rate_7d: number | null
  second_room_rate_30d: number | null
  recommendation_top10_like_rate: number | null
}
type Recommendation = { rank: number; impressions: number; likes: number; opens: number; saves: number; matches: number; like_rate: number | null; open_rate: number | null; save_rate: number | null; match_rate: number | null; average_score_liked: number | null; average_score_disliked: number | null }
type Retention = { cohort_date: string; cohort_users: number; d1_retention: number | null; d7_retention: number | null; d14_retention: number | null; d30_retention: number | null }
type Provider = { provider: string; date: string; sync_runs: number; successful_runs: number; failed_runs: number; errors: number; fetched: number; inserted: number; p95_duration_seconds: number | null }
type Performance = { date: string; operation: string; requests_and_errors: number; successful_operations: number; errors: number; error_rate: number | null; p50_duration_ms: number | null; p95_duration_ms: number | null; p99_duration_ms: number | null }
type GenericRow = Record<string, string | number | null>
type DashboardData = {
  period_days: number
  generated_at: string
  daily: DailyMetric[]
  summary?: Record<string, number | null>
  recommendations: Recommendation[]
  retention: Retention[]
  guardrails: Record<string, number | null>
  providers: Provider[]
  api_performance: Performance[]
  catalog_quality: GenericRow[]
  vote_agreement: GenericRow[]
  vote_agreement_summary: { events_with_same_vote: number; events_voted_by_both: number; agreement_rate: number | null }
  pool_diversity: Record<string, number | null>
  repeat_exposure: { date: string; impressions: number; repeat_impressions: number; repeat_event_exposure_rate: number | null }[]
}

const periodOptions = [7, 30, 90] as const
type Period = (typeof periodOptions)[number]
const number = (value: number | null | undefined, digits = 0) => value == null || !Number.isFinite(value) ? '—' : new Intl.NumberFormat('ru-RU', { maximumFractionDigits: digits }).format(value)
const percent = (value: number | null | undefined) => value == null || !Number.isFinite(value) ? 'Нет данных' : `${number(value * 100, 1)}%`
const dateLabel = (value: string) => new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short' }).format(new Date(`${value}T00:00:00Z`))
const duration = (seconds: number | null | undefined) => seconds == null ? '—' : seconds < 60 ? `${number(seconds, 0)} сек` : `${number(seconds / 60, 1)} мин`

function averageRate(rows: DailyMetric[], key: keyof DailyMetric, numerator: keyof DailyMetric, denominator: keyof DailyMetric) {
  const total = rows.reduce((sum, row) => sum + (typeof row[numerator] === 'number' ? row[numerator] as number : 0), 0)
  const base = rows.reduce((sum, row) => sum + (typeof row[denominator] === 'number' ? row[denominator] as number : 0), 0)
  if (!base) return rows.length ? rows.reduce((sum, row) => sum + (typeof row[key] === 'number' ? row[key] as number : 0), 0) / rows.filter((row) => typeof row[key] === 'number').length || null : null
  return total / base
}

function MetricCard({ label, value, detail, tone = 'default' }: { label: string; value: string; detail?: string; tone?: 'default' | 'accent' }) {
  return <article className={`${styles.metricCard} ${tone === 'accent' ? styles.metricAccent : ''}`}><span>{label}</span><strong>{value}</strong>{detail && <small>{detail}</small>}</article>
}

function Panel({ title, hint, children, className = '' }: { title: string; hint?: string; children: ReactNode; className?: string }) {
  return <section className={`${styles.panel} ${className}`}><div className={styles.panelHeading}><div><h2>{title}</h2>{hint && <p>{hint}</p>}</div></div>{children}</section>
}

function TrendChart({ rows }: { rows: DailyMetric[] }) {
  const max = Math.max(1, ...rows.map((row) => row.active_users))
  const points = rows.map((row, i) => `${rows.length < 2 ? 50 : 12 + i * (76 / (rows.length - 1))},${88 - row.active_users / max * 68}`).join(' ')
  return <div className={styles.chartWrap}><svg className={styles.chart} viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label="Активные пользователи и созданные комнаты по дням">
    {[20, 40, 60, 80].map((y) => <line key={y} x1="8" x2="92" y1={y} y2={y} className={styles.gridLine} />)}
    <polyline points={points} className={styles.linePrimary} />
    <polyline points={rows.map((row, i) => `${rows.length < 2 ? 50 : 12 + i * (76 / (rows.length - 1))},${88 - row.rooms_created / Math.max(1, ...rows.map((item) => item.rooms_created)) * 68}`).join(' ')} className={styles.lineSecondary} />
  </svg><div className={styles.chartLegend}><span><i /> Активные пользователи</span><span><i className={styles.legendSecondary} /> Создано комнат</span></div><div className={styles.axisLabels}><span>{rows[0] ? dateLabel(rows[0].date) : ''}</span><span>{rows.at(-1) ? dateLabel(rows.at(-1)!.date) : ''}</span></div></div>
}

function Empty({ children = 'Пока нет данных за выбранный период' }: { children?: string }) { return <div className={styles.empty}>{children}</div> }

function AnalyticsDashboard() {
  const [days, setDays] = useState<Period>(30)
  const [reload, setReload] = useState(0)
  const [data, setData] = useState<DashboardData | null>(null)
  const [error, setError] = useState('')
  const [loadingFor, setLoadingFor] = useState<Period | null>(30)
  const loading = loadingFor === days
  useEffect(() => {
    const controller = new AbortController()
    fetch(`/api/v1/internal/analytics/dashboard?days=${days}`, { headers: { Accept: 'application/json' }, cache: 'no-store', credentials: 'same-origin', signal: controller.signal })
      .then(async (response) => { if (!response.ok) throw new Error(response.status === 401 ? 'Сессия авторизации истекла. Обновите страницу и войдите снова.' : `Не удалось загрузить данные (${response.status}).`); return response.json() as Promise<DashboardData> })
      .then(setData)
      .catch((cause: unknown) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Ошибка загрузки данных.') })
      .finally(() => { if (!controller.signal.aborted) setLoadingFor(null) })
    return () => controller.abort()
  }, [days, reload])

  const daily = useMemo(() => data?.daily ?? [], [data])
  const summary = data?.summary
  const totals = { active: summary?.active_users ?? 0, created: summary?.rooms_created ?? 0, invites: summary?.rooms_invite_shared ?? 0, joined: summary?.rooms_joined ?? 0, activated: summary?.activated_rooms ?? 0, matched: summary?.rooms_matched ?? 0, ticketRooms: summary?.rooms_ticket_clicked ?? 0, ticketClicks: summary?.ticket_clicks ?? summary?.rooms_ticket_clicked ?? 0 }
  const activationRate = summary?.room_activation_rate ?? null
  const matchRate = summary?.match_rate ?? null
  const createdMatchConversion = summary?.created_match_conversion ?? null
  const eventOpenCtr = summary?.match_event_open_ctr ?? null
  const ticketCtr = data?.summary?.match_ticket_ctr ?? null
  const weightedAgreement = data?.vote_agreement_summary?.agreement_rate ?? null
  const weightedRepeat = ratio(sumField((data?.repeat_exposure ?? []) as GenericRow[], 'repeat_impressions'), sumField((data?.repeat_exposure ?? []) as GenericRow[], 'impressions'))
  const noData = !loading && !error && (daily.length === 0 || (totals.active + totals.created + totals.joined + totals.activated + totals.matched + totals.ticketRooms === 0))

  return <main className={styles.page}>
    <header className={styles.header}><div className={styles.brand}><div className={styles.brandMark}>M</div><div><p>WORKNET <span>/ INTERNAL</span></p><h1>Продуктовая аналитика</h1></div></div><div className={styles.headerTools}><span className={styles.live}><i /> Данные PostgreSQL</span><div className={styles.period} aria-label="Период отчёта">{periodOptions.map((period) => <button key={period} aria-pressed={days === period} onClick={() => { setDays(period); setLoadingFor(period); setError('') }}>{period} дней</button>)}</div></div></header>
    <div className={styles.content}>
      <div className={styles.intro}><div><p className={styles.kicker}>ОБЗОР ПРОДУКТА</p><h2>Что происходит в MAX Together</h2><p>Сводка за последние {days} дней · часовой пояс UTC</p></div><div className={styles.updated}>{data?.generated_at ? <>Обновлено<br /><b>{new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(data.generated_at))}</b></> : 'Обновляется автоматически'}</div></div>
      {error && <div className={styles.error} role="alert"><strong>Не получилось загрузить аналитику</strong><span>{error}</span><button onClick={() => { setError(''); setLoadingFor(days); setReload((value) => value + 1) }}>Повторить</button></div>}
      {loading && <div className={styles.loading}>Загружаем сводку…</div>}
      {!loading && !error && <>
        {noData && <div className={styles.emptyBanner}><strong>Данных пока нет</strong><span>События появятся здесь после первых пользовательских сессий.</span></div>}
        <div className={styles.metricsGrid}>
          <MetricCard label="Активность" value={number(totals.active)} detail="уникальные активные пользователи за период" tone="accent" />
          <MetricCard label="Создано комнат" value={number(totals.created)} detail="за выбранный период" />
          <MetricCard label="Room Activation Rate" value={percent(activationRate)} detail={`${number(totals.activated)} из ${number(totals.created)} комнат · оба участника голосовали`} />
          <MetricCard label="Activated Room Match Rate" value={percent(matchRate)} detail={`${number(totals.matched)} комнат с мэтчем / активированные`} />
          <MetricCard label="Created → Match Conversion" value={percent(createdMatchConversion)} detail="комнаты с мэтчем / созданные комнаты" />
          <MetricCard label="Match → Event Open CTR" value={percent(eventOpenCtr)} detail="комнаты с открытием события / комнаты с мэтчем" />
          <MetricCard label="Ticket clicks" value={number(totals.ticketClicks)} detail={`${number(totals.ticketRooms)} комнат · Match → Ticket CTR ${percent(ticketCtr)}`} />
          <MetricCard label="No-match rate" value={percent(summary?.no_match_rate)} detail="активированные комнаты, завершившиеся без мэтча" />
          <MetricCard label="Pool exhausted rate" value={percent(summary?.pool_exhausted_rate)} detail="активированные комнаты с исчерпанным пулом" />
        </div>
        <div className={styles.mainGrid}>
          <Panel title="Динамика" hint="Активные пользователи и новые комнаты по дням" className={styles.trendPanel}>{daily.length && !noData ? <TrendChart rows={daily} /> : <Empty />}</Panel>
          <Panel title="Воронка комнат" hint="Уникальные комнаты когорты по дате создания · каждый этап включает предыдущие"><div className={styles.funnel}>{[
            ['Комната создана', totals.created], ['Приглашение отправлено или использовано', totals.invites], ['Второй участник вошёл', totals.joined], ['Комната активирована', totals.activated], ['Найден мэтч', totals.matched], ['Переход к билетам', totals.ticketRooms],
          ].map(([label, value], index) => <div className={styles.funnelStep} key={label}><span className={styles.funnelIndex}>0{index + 1}</span><span>{label}</span><b>{number(value as number)}</b></div>)}</div></Panel>
        </div>
        <div className={styles.twoCol}>
          <Panel title="Совместный выбор" hint="Медианы среди комнат, созданных за выбранный период"><div className={styles.statPair}><div><span>От активации до первого мэтча</span><b>{duration(data?.summary?.median_time_to_match_seconds)}</b></div><div><span>Уникальные события с голосом до мэтча</span><b>{number(data?.summary?.median_swipes_to_match, 1)}</b></div><div><span>Время до входа</span><b>{duration(data?.summary?.median_time_to_join_seconds)}</b></div><div><span>Сходимость голосов</span><b>{percent(weightedAgreement)}</b></div></div></Panel>
          <Panel title="Повторное использование" hint="По зрелым когортам первых комнат выбранного периода"><div className={styles.statPair}><div><span>Вторая комната · 7 дней</span><b>{percent(data?.summary?.second_room_rate_7d)}</b></div><div><span>Вторая комната · 30 дней</span><b>{percent(data?.summary?.second_room_rate_30d)}</b></div><div><span>Создано на пользователя</span><b>{percent(data?.summary?.room_creation_rate ?? averageRate(daily, 'room_creation_rate', 'rooms_created', 'active_users'))}</b></div><div><span>Повторные показы</span><b>{percent(weightedRepeat)}</b></div></div></Panel>
        </div>
        <div className={styles.twoCol}>
          <Panel title="Рекомендации" hint="Like rate по позиции · события за последние 90 дней"><div className={styles.tableWrap}><table><thead><tr><th>Позиция</th><th>Показы</th><th>Likes</th><th>Like rate</th><th>Мэтчи</th></tr></thead><tbody>{(data?.recommendations ?? []).slice(0, 10).map((item) => <tr key={item.rank}><td><span className={styles.rank}>#{item.rank}</span></td><td>{number(item.impressions)}</td><td>{number(item.likes)}</td><td>{percent(item.like_rate)}</td><td>{number(item.matches)}</td></tr>)}</tbody></table>{!data?.recommendations.length && <Empty />}</div></Panel>
          <Panel title="Удержание" hint="Возврат к активности по дате первого события"><div className={styles.tableWrap}><table><thead><tr><th>Когорта</th><th>Пользователи</th><th>D1</th><th>D7</th><th>D14</th><th>D30</th></tr></thead><tbody>{(data?.retention ?? []).slice(-8).map((item) => <tr key={item.cohort_date}><td>{dateLabel(item.cohort_date)}</td><td>{number(item.cohort_users)}</td><td>{percent(item.d1_retention)}</td><td>{percent(item.d7_retention)}</td><td>{percent(item.d14_retention)}</td><td>{percent(item.d30_retention)}</td></tr>)}</tbody></table>{!data?.retention.length && <Empty />}</div></Panel>
        </div>
        <div className={styles.twoCol}>
          <Panel title="Качество каталога" hint="Текущий опубликованный каталог · полнота атрибутов"><div className={styles.tableWrap}><table><thead><tr><th>Категория</th><th>События</th><th>Фото</th><th>Цена</th><th>Билеты</th><th>Описание</th></tr></thead><tbody>{aggregateCatalog((data?.catalog_quality ?? []) as GenericRow[]).map((item) => <tr key={item.category}><td>{item.category}</td><td>{number(item.event_count)}</td><td>{percent(item.image_rate)}</td><td>{percent(item.price_rate)}</td><td>{percent(item.ticket_link_rate)}</td><td>{percent(item.description_rate)}</td></tr>)}</tbody></table>{!data?.catalog_quality.length && <Empty />}</div></Panel>
          <Panel title="Разнообразие и повторы" hint="Качество подборок и повторная экспозиция"><div className={styles.statPair}><div><span>Пулов для анализа</span><b>{number(data?.pool_diversity?.pools)}</b></div><div><span>Категорий на пул</span><b>{number(data?.pool_diversity?.average_categories_top10, 1)}</b></div><div><span>Поставщиков на пул</span><b>{number(data?.pool_diversity?.average_providers_top20, 1)}</b></div><div><span>Повторные показы</span><b>{percent(weightedRepeat)}</b></div></div><p className={styles.subnote}>Агрегаты по сохранённым пулам и показам за период.</p></Panel>
        </div>
        <div className={styles.twoCol}>
          <Panel title="Провайдеры" hint="Синхронизация данных по дням"><div className={styles.tableWrap}><table><thead><tr><th>Провайдер</th><th>Запуски</th><th>Успех</th><th>Ошибки</th><th>P95</th></tr></thead><tbody>{(data?.providers ?? []).slice(-10).map((item, index) => <tr key={`${item.provider}-${item.date}-${index}`}><td>{item.provider}</td><td>{number(item.sync_runs)}</td><td>{percent(item.sync_runs ? item.successful_runs / item.sync_runs : null)}</td><td>{number(item.errors)}</td><td>{duration(item.p95_duration_seconds)}</td></tr>)}</tbody></table>{!data?.providers.length && <Empty />}</div></Panel>
          <Panel title="Надёжность API" hint="Ошибки и задержки операций"><div className={styles.tableWrap}><table><thead><tr><th>Операция</th><th>Запросы</th><th>Ошибки</th><th>P50</th><th>P95</th></tr></thead><tbody>{(data?.api_performance ?? []).slice(-10).map((item, index) => <tr key={`${item.date}-${item.operation}-${index}`}><td>{item.operation}</td><td>{number(item.requests_and_errors)}</td><td>{percent(item.error_rate)}</td><td>{number(item.p50_duration_ms)} мс</td><td>{number(item.p95_duration_ms)} мс</td></tr>)}</tbody></table>{!data?.api_performance.length && <Empty />}</div></Panel>
        </div>
        <Panel title="Защитные метрики" hint="Сводные ошибки и проблемные сценарии за всё время"><div className={styles.guardrails}>{Object.entries(data?.guardrails ?? {}).map(([key, value]) => <div key={key}><span>{humanize(key)}</span><b>{key.endsWith('_rate') ? percent(value) : number(value)}</b></div>)}{!Object.keys(data?.guardrails ?? {}).length && <Empty />}</div></Panel>
      </>}
      <footer className={styles.footer}>Внутренняя аналитика · только агрегированные данные · персональные идентификаторы не отображаются</footer>
    </div>
  </main>
}

function sumField(rows: GenericRow[], key: string) { return rows.reduce((sum, row) => sum + (typeof row[key] === 'number' ? row[key] as number : 0), 0) }
function aggregateCatalog(rows: GenericRow[]) {
  const totals = new Map<string, Record<string, number>>()
  rows.forEach((row) => {
    const category = String(row.category ?? 'Без категории')
    const total = totals.get(category) ?? { event_count: 0, events_with_image: 0, events_with_price: 0, events_with_ticket_link: 0, events_with_description: 0 }
    for (const key of Object.keys(total)) total[key] += typeof row[key] === 'number' ? row[key] as number : 0
    totals.set(category, total)
  })
  return [...totals].map(([category, total]) => ({ category, event_count: total.event_count, image_rate: ratio(total.events_with_image, total.event_count), price_rate: ratio(total.events_with_price, total.event_count), ticket_link_rate: ratio(total.events_with_ticket_link, total.event_count), description_rate: ratio(total.events_with_description, total.event_count) })).sort((a, b) => b.event_count - a.event_count).slice(0, 8)
}
function ratio(numerator: number, denominator: number) { return denominator ? numerator / denominator : null }
function humanize(value: string) { return value.replaceAll('_', ' ').replace(/\b\w/g, (char) => char.toUpperCase()) }

export { AnalyticsDashboard }
