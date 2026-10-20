import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { EventSubmissionInput, EventSubmissionRow } from '../../shared/api/client'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import styles from './event-moderation.module.css'

const base = '/api/v1/internal/event-submissions'
type Domain = { id: string; hostname: string; purpose: 'image' | 'ticket'; created_at: string }
type Duplicate = { title: string; venue_name: string; starts_at: string; event_id: string }
type Detail = { submission: EventSubmissionRow; duplicates: Duplicate[]; resource_domains: Array<{ hostname: string; purpose: 'image' | 'ticket'; approved?: boolean }> }
type List = { items: EventSubmissionRow[]; total: number; counts: { pending: number; approved: number; rejected: number } }
const category = (slug: string) => CATEGORY_OPTIONS.find((item) => item.slug === slug)?.label ?? slug
const stamp = (value: string, timezone?: string) => { try { return new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short', ...(timezone ? { timeZone: timezone } : {}) }).format(new Date(value)) } catch { return value } }
const localMinute = (value?: string | null) => { if (!value) return null; const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value); return match ? `${match[3]}.${match[2]}.${match[1]}, ${match[4]}:${match[5]}` : value }
function apiError(response: Response, body: any) { const code = body?.error?.code; if (response.status === 401 || code === 'UNAUTHENTICATED' || code === 'TOKEN_EXPIRED') return 'Внутренний доступ не подтверждён. Войдите через защищённую панель администратора.'; if (response.status === 403) return 'У вас нет прав для этого действия.'; if (code === 'DECISION_CONFLICT' || response.status === 409) return 'Заявка уже обработана. Обновите очередь.'; if (code === 'DOMAIN_NOT_APPROVED') return 'Сначала разрешите домен для нужного назначения.'; if (code === 'VALIDATION_FAILED') return 'Проверьте данные заявки или разрешение выбранного домена.'; if (code === 'RATE_LIMITED') return 'Слишком много действий за короткое время. Подождите и повторите.'; return 'Не удалось выполнить запрос. Проверьте соединение и попробуйте снова.' }

export function EventModerationPage() {
  const [status, setStatus] = useState<'pending' | 'approved' | 'rejected'>('pending')
  const [offset, setOffset] = useState(0)
  const [list, setList] = useState<List | null>(null)
  const [detail, setDetail] = useState<Detail | null>(null)
  const [domains, setDomains] = useState<Domain[]>([])
  const [reason, setReason] = useState('')
  const [hostname, setHostname] = useState('')
  const [purpose, setPurpose] = useState<'image' | 'ticket'>('image')
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const listRequestSequence = useRef(0)
  const pageSize = 20
  const invalidateListRequest = () => { listRequestSequence.current += 1 }

  const request = useCallback(async (url: string, init: RequestInit = {}) => {
    const response = await fetch(url, { ...init, credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json', ...(init.body ? { 'Content-Type': 'application/json' } : {}), ...(init.method && init.method !== 'GET' ? { 'X-Admin-Request': '1' } : {}), ...init.headers } })
    const body: any = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(apiError(response, body))
    return body
  }, [])

  const loadList = useCallback(async (nextStatus = status, nextOffset = offset) => {
    const sequence = ++listRequestSequence.current
    try { const q = new URLSearchParams({ status: nextStatus, limit: String(pageSize), offset: String(nextOffset) }); const result = await request(`${base}?${q}`) as List; if (sequence === listRequestSequence.current) setList(result) }
    catch (cause) { if (sequence === listRequestSequence.current) setError(cause instanceof Error ? cause.message : 'Не удалось загрузить очередь.') }
    finally { if (sequence === listRequestSequence.current) setLoading(false) }
  }, [offset, request, status])

  // eslint-disable-next-line react-hooks/set-state-in-effect -- Load remote queue after this standalone admin screen mounts.
  useEffect(() => { void loadList(); return () => { listRequestSequence.current += 1 } }, [loadList])
  const loadDomains = useCallback(async () => { try { const result = await request(`${base}/domains`); setDomains(result.domains ?? []) } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось загрузить домены.') } }, [request])
  // eslint-disable-next-line react-hooks/set-state-in-effect -- Load remote allowlist after this standalone admin screen mounts.
  useEffect(() => { void loadDomains() }, [loadDomains])

  async function openDetail(id: string) {
    setBusy('detail'); setError(''); setNotice('')
    try { const result = await request(`${base}/${encodeURIComponent(id)}`) as Detail; setDetail(result); setReason(''); const payload = result.submission.payload; const linked = payload.image_url || payload.ticket_url; if (linked) { try { setHostname(new URL(linked).hostname); setPurpose(payload.image_url ? 'image' : 'ticket') } catch { setHostname('') } } }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось открыть заявку.') }
    finally { setBusy('') }
  }

  async function decide(decision: 'approve' | 'reject') {
    if (!detail) return
    if (decision === 'approve' && !window.confirm('Одобрить событие и добавить его в общую афишу?')) return
    if (decision === 'reject' && !reason.trim()) { setError('Укажите причину отклонения.'); return }
    setBusy(decision); setError(''); setNotice('')
    try {
      const response = await request(`${base}/${encodeURIComponent(detail.submission.id)}/${decision}`, { method: 'POST', body: JSON.stringify(decision === 'approve' ? {} : { reason: reason.trim() }) })
      setNotice(decision === 'approve' ? `Событие одобрено${response.event_id ? ` и опубликовано (ID ${response.event_id})` : ''}.` : 'Заявка отклонена.')
      setDetail(null); setLoading(true); await loadList();
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось сохранить решение.') }
    finally { setBusy('') }
  }

  async function allowDomain() {
    if (!hostname.trim()) return
    setBusy('domain'); setError(''); setNotice('')
    try { await request(`${base}/domains`, { method: 'POST', body: JSON.stringify({ hostname: hostname.trim(), purpose }) }); await loadDomains(); setNotice(`Домен разрешён для назначения «${purpose === 'image' ? 'афиша' : 'ссылка'}».`) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось разрешить домен.') }
    finally { setBusy('') }
  }

  async function revokeDomain(id: string) {
    setBusy(`revoke:${id}`); setError(''); setNotice('')
    try { await request(`${base}/domains/${encodeURIComponent(id)}`, { method: 'DELETE' }); await loadDomains(); setNotice('Разрешение домена отозвано.') }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось отозвать разрешение.') }
    finally { setBusy('') }
  }

  const resourceHosts = useMemo(() => {
    if (!detail) return []
    const result: Array<{ hostname: string; purpose: 'image' | 'ticket' }> = []
    const payload: EventSubmissionInput = detail.submission.payload
    for (const [url, resourcePurpose] of [[payload.image_url, 'image'], [payload.ticket_url, 'ticket']] as const) if (url) { try { result.push({ hostname: new URL(url).hostname, purpose: resourcePurpose }) } catch { /* backend rejects malformed URLs */ } }
    return result
  }, [detail])
  const domainSignature = useMemo(() => domains.map((item) => `${item.id}:${item.hostname.toLowerCase()}:${item.purpose}`).sort().join('|'), [domains])

  return <main className={styles.page}>
    <header className={styles.header}><div className={styles.brand}><div className={styles.brandMark}>M</div><div><p>WORKNET <span>/ INTERNAL</span></p><h1>Модерация событий</h1></div></div><div className={styles.headerLinks}><span>Внутренний доступ</span><a href="/internal/event-sources">Источники API →</a><a href="/internal/analytics">Аналитика →</a></div></header>
    <div className={styles.content}><div className={styles.intro}><div><p className={styles.kicker}>ПОЛЬЗОВАТЕЛЬСКИЕ ЗАЯВКИ</p><h2>Очередь модерации</h2><p>Проверьте сведения, ресурсы и похожие события перед решением.</p></div><button className={styles.secondary} onClick={() => { setLoading(true); setError(''); void loadList() }}>Обновить</button></div>
      {error && <div className={styles.alert} role="alert">{error}</div>}{notice && <div className={styles.notice} role="status">{notice}</div>}
      <div className={styles.counts} aria-label="Количество заявок по статусу">{(['pending', 'approved', 'rejected'] as const).map((item) => <div key={item}><span>{statusLabel(item)}</span><strong>{list?.counts[item] ?? '—'}</strong></div>)}</div>
      <div className={styles.tabs} aria-label="Фильтр заявок">{(['pending', 'approved', 'rejected'] as const).map((item) => <button key={item} aria-pressed={status === item} onClick={() => { if (item === status && offset === 0) return; invalidateListRequest(); setStatus(item); setOffset(0); setDetail(null); setLoading(true); setError('') }}>{statusLabel(item)}</button>)}</div>
      <div className={styles.columns}><section className={styles.queue} aria-label="Список заявок">{loading || !list ? <p className={styles.empty}>Загружаем очередь…</p> : !list.items.length ? <p className={styles.empty}>Заявок в этом разделе нет.</p> : list.items.map((item) => <button className={`${styles.row} ${detail?.submission.id === item.id ? styles.selected : ''}`} key={item.id} onClick={() => void openDetail(item.id)}><span className={styles.rowMeta}>{item.author_name || 'Пользователь'} · {stamp(item.created_at)}</span><strong>{item.payload.title}</strong><span>{item.payload.venue_name} · {stamp(item.starts_at, item.timezone)}</span><span className={styles.rowStatus}>{statusLabel(item.status)}</span></button>)}
        {list && list.total > pageSize && <div className={styles.pager}><button disabled={offset === 0} onClick={() => { invalidateListRequest(); setOffset((value) => Math.max(0, value - pageSize)); setLoading(true) }}>Назад</button><span>{Math.floor(offset / pageSize) + 1} / {Math.ceil(list.total / pageSize)}</span><button disabled={offset + pageSize >= list.total} onClick={() => { invalidateListRequest(); setOffset((value) => value + pageSize); setLoading(true) }}>Дальше</button></div>}
      </section>
      <section className={styles.detail} aria-label="Детали заявки">{busy === 'detail' ? <p className={styles.empty}>Загружаем заявку…</p> : !detail ? <p className={styles.empty}>Выберите заявку, чтобы открыть предпросмотр.</p> : <>
        <div className={styles.detailHead}><div><p className={styles.kicker}>{statusLabel(detail.submission.status)} · {detail.submission.author_name || 'Пользователь'}</p><h2>{detail.submission.payload.title}</h2></div>{detail.submission.payload.image_url ? <a href={`${base}/${encodeURIComponent(detail.submission.id)}/image`} target="_blank" rel="noreferrer" className={styles.imageLink}>Открыть афишу</a> : null}</div>
        {detail.submission.payload.image_url ? <img key={`${detail.submission.id}:${domainSignature}`} className={styles.image} src={`${base}/${encodeURIComponent(detail.submission.id)}/image`} alt={`Афиша: ${detail.submission.payload.title}`} onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = '/events/photo-unavailable.png' }} /> : <div className={styles.imagePlaceholder}>Афиша не указана</div>}
        <p className={styles.category}>{category(detail.submission.payload.category_slug)}</p><p className={styles.description}>{detail.submission.payload.description}</p>
        <dl className={styles.meta}><div><dt>Автор</dt><dd>{detail.submission.author_name || detail.submission.author_id}</dd></div><div><dt>Город и место</dt><dd>{detail.submission.payload.venue_name}, {detail.submission.payload.venue_address}</dd></div><div><dt>Начало</dt><dd>{stamp(detail.submission.starts_at, detail.submission.timezone)} · {detail.submission.timezone}</dd></div>{detail.submission.payload.ends_at_local && <div><dt>Окончание</dt><dd>{localMinute(detail.submission.payload.ends_at_local)} · {detail.submission.timezone}</dd></div>}<div><dt>Цена</dt><dd>{price(detail.submission.payload)}</dd></div><div><dt>Возраст</dt><dd>{detail.submission.payload.age_rating || 'Не указан'}</dd></div><div><dt>Ссылка</dt><dd>{detail.submission.payload.ticket_url || 'Не указана'}</dd></div>{detail.submission.decided_at && <div><dt>Решение</dt><dd>{stamp(detail.submission.decided_at, detail.submission.timezone)}</dd></div>}{detail.submission.decided_by && <div><dt>Модератор</dt><dd>{detail.submission.decided_by}</dd></div>}{detail.submission.rejection_reason && <div><dt>Причина</dt><dd>{detail.submission.rejection_reason}</dd></div>}</dl>
        {detail.duplicates?.length > 0 && <aside className={styles.warning}><strong>Возможные дубликаты</strong>{detail.duplicates.map((item) => <span key={item.event_id}>{item.title} · {item.venue_name} · {stamp(item.starts_at)}</span>)}</aside>}
        {detail.submission.status === 'pending' && <>
          <section className={styles.domainPanel}><h3>Разрешённые домены</h3><p>Разрешение действует для всех пользовательских мероприятий. Домен не разрешается автоматически при одобрении заявки.</p>{resourceHosts.map((resource) => { const allowed = domains.some((item) => item.hostname.toLowerCase() === resource.hostname.toLowerCase() && item.purpose === resource.purpose); return <div className={styles.resource} key={`${resource.purpose}:${resource.hostname}`}><span>{resource.hostname} · {resource.purpose === 'image' ? 'афиша' : 'ссылка'}</span><b className={allowed ? styles.allowed : styles.denied}>{allowed ? 'Разрешён' : 'Требует разрешения'}</b><button onClick={() => { setHostname(resource.hostname); setPurpose(resource.purpose) }}>Выбрать</button></div> })}<div className={styles.domainForm}><input aria-label="Hostname" autoComplete="off" placeholder="cdn.example.ru" value={hostname} onChange={(event) => setHostname(event.target.value)} /><select aria-label="Назначение разрешения" value={purpose} onChange={(event) => setPurpose(event.target.value as 'image' | 'ticket')}><option value="image">Афиша</option><option value="ticket">Ссылка на билеты</option></select><button disabled={Boolean(busy) || !hostname.trim()} onClick={() => void allowDomain()}>Разрешить домен</button></div>{domains.length > 0 && <ul>{domains.map((item) => <li key={item.id}>{item.hostname} · {item.purpose === 'image' ? 'афиша' : 'ссылка'} <button disabled={Boolean(busy)} onClick={() => void revokeDomain(item.id)}>Отозвать</button></li>)}</ul>}</section>
          <div className={styles.actions}><button className={styles.approve} disabled={Boolean(busy)} onClick={() => void decide('approve')}>{busy === 'approve' ? 'Публикуем…' : 'Одобрить и опубликовать'}</button><label>Причина отклонения<textarea rows={3} maxLength={2000} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Напишите, что нужно исправить" /></label><button className={styles.reject} disabled={Boolean(busy) || !reason.trim()} onClick={() => void decide('reject')}>{busy === 'reject' ? 'Сохраняем…' : 'Отклонить заявку'}</button></div>
        </>}
      </>}</section></div>
    </div>
  </main>
}

function statusLabel(status: EventSubmissionRow['status']) { return status === 'pending' ? 'На проверке' : status === 'approved' ? 'Одобрено' : 'Отклонено' }
function price(payload: EventSubmissionInput) { if (payload.price_mode === 'free') return 'Бесплатно'; if (payload.price_mode === 'unknown') return 'Цена уточняется'; const amount = (payload.price_from_minor ?? 0) / 100; const max = payload.price_to_minor ? ` — ${payload.price_to_minor / 100} ₽` : ''; return `${amount} ₽${max}` }
