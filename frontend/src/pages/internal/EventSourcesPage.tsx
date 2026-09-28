import { useCallback, useEffect, useMemo, useState } from 'react'
import { CATEGORY_OPTIONS } from '../../shared/api/categories'
import styles from './event-sources.module.css'

const api = '/api/v1/internal/event-sources'
type AuthType = 'none' | 'bearer' | 'api_key_header' | 'api_key_query'
type PaginationMode = 'none' | 'page' | 'offset'
type FieldMapping = { path: string; transform?: string; default?: string }
type Pagination = { mode: PaginationMode; page_param: string; page_size_param: string; offset_param: string; limit_param: string; page_size: number; limit: number }
type Mapping = Record<string, FieldMapping>
type SourceForm = {
  name: string; enabled: boolean; source_key: string; endpoint_url: string; auth_type: AuthType; auth_name: string; auth_secret: string
  query_params: Record<string, string>; response_path: string; pagination: Pagination; mapping: Mapping
  defaults: { category: string; timezone: string; currency: string; status: string }; price_unit: 'major' | 'minor'
}
type Source = Partial<SourceForm> & {
  id: string; source_key: string; name: string; enabled: boolean; endpoint_url: string; auth_type: AuthType; auth_name?: string
  secret_configured?: boolean; mapping_locked?: boolean; last_sync_status?: string; sync_status?: string; last_sync_state?: string; last_sync_at?: string; event_count?: number; kind?: string
}
type ResourceDomain = { id?: string; hostname: string; purpose: 'image' | 'ticket'; approved: boolean; count?: number; created_at?: string }
type Preview = { connection_ok: boolean; http_status?: number; received: number; valid: number; invalid: number; complete?: boolean; warnings?: { code: string; count: number }[]; resource_domains?: ResourceDomain[]; errors: { code: string; count: number }[]; preview: { title: string; starts_at: string; venue: string; price_from_minor?: number }[] }

const emptyPagination: Pagination = { mode: 'none', page_param: 'page', page_size_param: 'page_size', offset_param: 'offset', limit_param: 'limit', page_size: 100, limit: 100 }
const mappingFields = [
  ['external_id', 'External ID'], ['title', 'Title'], ['description', 'Description'], ['subtitle', 'Subtitle'], ['starts_at', 'Start date'], ['ends_at', 'End date'],
  ['venue_name', 'Venue name'], ['venue_address', 'Address'], ['latitude', 'Latitude'], ['longitude', 'Longitude'], ['metro', 'Metro'], ['image', 'Image'],
  ['ticket_url', 'Ticket URL'], ['ticket_available', 'Ticket available'], ['price_from', 'Price from'], ['price_to', 'Price to'],
] as const
const emptyForm = (): SourceForm => ({ name: '', enabled: true, source_key: '', endpoint_url: '', auth_type: 'none', auth_name: '', auth_secret: '', query_params: {}, response_path: '', pagination: { ...emptyPagination }, mapping: Object.fromEntries(mappingFields.map(([key]) => [key, { path: '' }])) as Mapping, defaults: { category: 'other', timezone: 'Europe/Moscow', currency: 'RUB', status: 'published' }, price_unit: 'major' })
function sameQueryParams(first: Record<string, string>, second: Record<string, string>): boolean {
 return Object.keys(first).length === Object.keys(second).length && Object.entries(first).every(([key, value]) => second[key] === value)
}
function writableForm(value: SourceForm): SourceForm {
  return { name: value.name, enabled: value.enabled, source_key: value.source_key, endpoint_url: value.endpoint_url, auth_type: value.auth_type, auth_name: value.auth_name ?? '', auth_secret: value.auth_secret ?? '', query_params: value.query_params ?? {}, response_path: value.response_path ?? '', pagination: value.pagination, mapping: value.mapping, defaults: value.defaults, price_unit: value.price_unit }
}

function readObject(value: unknown): Record<string, unknown> { if (value && typeof value === 'object' && !Array.isArray(value)) return value as Record<string, unknown>; if (typeof value === 'string') { try { return readObject(JSON.parse(value)) } catch { return {} } }; return {} }
function sourceList(value: unknown): Source[] { if (Array.isArray(value)) return value as Source[]; const object = readObject(value); return (Array.isArray(object.sources) ? object.sources : Array.isArray(object.items) ? object.items : []) as Source[] }
function unwrapSource(value: unknown): Source { const object = readObject(value); const source = readObject(object.source); return (source.id ? source : object) as unknown as Source }
function slug(value: string) {
  const transliteration: Record<string, string> = { а: 'a', б: 'b', в: 'v', г: 'g', д: 'd', е: 'e', ё: 'e', ж: 'zh', з: 'z', и: 'i', й: 'y', к: 'k', л: 'l', м: 'm', н: 'n', о: 'o', п: 'p', р: 'r', с: 's', т: 't', у: 'u', ф: 'f', х: 'kh', ц: 'ts', ч: 'ch', ш: 'sh', щ: 'shch', ъ: '', ы: 'y', ь: '', э: 'e', ю: 'yu', я: 'ya' }
  return value.trim().toLowerCase().replace(/[а-яё]/g, (character) => transliteration[character] ?? character).normalize('NFKD').replace(/[\u0300-\u036f]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
}
function errorMessage(response: Response, payload: unknown) { const body = readObject(payload); const nestedError = readObject(body.error); return typeof body.message === 'string' ? body.message : typeof nestedError.message === 'string' ? nestedError.message : typeof body.error === 'string' ? body.error : `Запрос завершился с ошибкой (${response.status}).` }

function EventSourcesPage() {
  const [sources, setSources] = useState<Source[]>([])
  const [form, setForm] = useState<SourceForm>(emptyForm)
  const [editing, setEditing] = useState<Source | null>(null)
  const [isFormOpen, setIsFormOpen] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [preview, setPreview] = useState<Preview | null>(null)
  const [domains, setDomains] = useState<ResourceDomain[]>([])
  const [domainHostname, setDomainHostname] = useState('')
  const [domainPurpose, setDomainPurpose] = useState<'image' | 'ticket'>('image')
  const [queryText, setQueryText] = useState('{}')

  const loadSources = useCallback(async (signal?: AbortSignal) => {
    const response = await fetch(api, { headers: { Accept: 'application/json' }, cache: 'no-store', credentials: 'same-origin', signal })
    const payload: unknown = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(errorMessage(response, payload))
    const list = sourceList(payload)
    setSources(list)
    return list
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    fetch(api, { headers: { Accept: 'application/json' }, cache: 'no-store', credentials: 'same-origin', signal: controller.signal })
      .then(async (response) => { const payload: unknown = await response.json().catch(() => ({})); if (!response.ok) throw new Error(errorMessage(response, payload)); return sourceList(payload) })
      .then(setSources)
      .catch((cause: unknown) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Не удалось загрузить источники.') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [loadSources])

  const builtIns = useMemo(() => sources.filter((source) => source.kind === 'built_in' || source.source_key === 'kudago' || source.source_key === 'timepad'), [sources])
  const genericSources = useMemo(() => sources.filter((source) => !builtIns.includes(source)), [sources, builtIns])

  function openCreate() { setEditing(null); setForm(emptyForm()); setQueryText('{}'); setPreview(null); setError(''); setNotice(''); setIsFormOpen(true) }
  async function openEdit(source: Source) {
    setBusy('detail'); setError('')
    try {
      const detail = unwrapSource(await request(`${api}/${encodeURIComponent(source.id)}`, 'GET'))
      const values = { ...emptyForm(), ...detail, auth_secret: '', query_params: detail.query_params ?? {}, pagination: { ...emptyPagination, ...readObject(detail.pagination) } as Pagination, mapping: { ...emptyForm().mapping, ...readObject(detail.mapping) } as Mapping, defaults: { ...emptyForm().defaults, ...detail.defaults }, price_unit: detail.price_unit ?? 'major' } as SourceForm
      setEditing({ ...source, ...detail }); setForm(values); setQueryText(JSON.stringify(values.query_params, null, 2)); setPreview(null); setDomains([]); setDomainHostname(''); setNotice(''); setIsFormOpen(true)
      await loadDomains(source.id)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось загрузить источник.') }
    finally { setBusy('') }
  }
  function setField<K extends keyof SourceForm>(key: K, value: SourceForm[K]) { setForm((current) => ({ ...current, [key]: value })) }
  function setPagination(key: keyof Pagination, value: string | number) { setForm((current) => ({ ...current, pagination: { ...current.pagination, [key]: value } })) }
  function setMapping(key: string, value: Partial<FieldMapping>) { setForm((current) => ({ ...current, mapping: { ...current.mapping, [key]: { ...current.mapping[key], ...value } } })) }
  function setDefault(key: keyof SourceForm['defaults'], value: string) { setForm((current) => ({ ...current, defaults: { ...current.defaults, [key]: value } })) }

  function makePayload(): SourceForm {
    let queryParams: Record<string, string>
    try { queryParams = JSON.parse(queryText) as Record<string, string> } catch { throw new Error('Query parameters должны быть корректным JSON-объектом.') }
    if (!queryParams || typeof queryParams !== 'object' || Array.isArray(queryParams) || Object.values(queryParams).some((value) => typeof value !== 'string')) throw new Error('Query parameters должны быть объектом со строковыми значениями.')
    const payload = { ...writableForm(form), source_key: editing?.source_key ?? `generic:${slug(form.name)}`, query_params: queryParams }
    if (!payload.source_key || payload.source_key === 'generic:') throw new Error('Добавьте название источника, чтобы сформировать source key.')
    return payload
  }

  async function request(path: string, method: string, body?: unknown) {
    const response = await fetch(path, { method, credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json', ...(body ? { 'Content-Type': 'application/json' } : {}), 'X-Admin-Request': '1' }, ...(body ? { body: JSON.stringify(body) } : {}) })
    const payload: unknown = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(errorMessage(response, payload))
    return payload
  }

  async function loadDomains(sourceId: string) {
    const payload = readObject(await request(`${api}/${encodeURIComponent(sourceId)}/domains`, 'GET'))
    setDomains(Array.isArray(payload.domains) ? payload.domains as ResourceDomain[] : [])
  }

  async function approveDomain(sourceId: string, hostname: string, purpose: 'image' | 'ticket') {
    await request(`${api}/${encodeURIComponent(sourceId)}/domains`, 'POST', { hostname, purpose })
    await loadDomains(sourceId)
    setPreview((current) => current ? { ...current, resource_domains: current.resource_domains?.map((domain) => domain.hostname.toLowerCase() === hostname.toLowerCase() && domain.purpose === purpose ? { ...domain, approved: true } : domain) } : current)
  }

  async function revokeDomain(domainId: string, sourceId: string) {
    const revoked = domains.find((domain) => domain.id === domainId)
    setBusy('domain'); setError(''); setNotice('')
    try {
      await request(`${api}/${encodeURIComponent(sourceId)}/domains/${encodeURIComponent(domainId)}`, 'DELETE')
      await loadDomains(sourceId)
      if (revoked) setPreview((current) => current ? { ...current, resource_domains: current.resource_domains?.map((domain) => domain.hostname.toLowerCase() === revoked.hostname.toLowerCase() && domain.purpose === revoked.purpose ? { ...domain, approved: false } : domain) } : current)
      setNotice(`Разрешение ${revoked?.hostname ?? 'домена'} отозвано.`)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось отозвать разрешение.') }
    finally { setBusy('') }
  }

  async function testConfig() {
    setBusy('test'); setError(''); setNotice(''); setPreview(null)
    try {
      const config = makePayload()
      const connectionChanged = Boolean(editing && (config.endpoint_url !== editing.endpoint_url || config.auth_type !== editing.auth_type || config.auth_name !== (editing.auth_name ?? '') || !sameQueryParams(config.query_params, editing.query_params ?? {})))
      if (editing?.secret_configured && config.auth_type !== 'none' && connectionChanged && !config.auth_secret) throw new Error('Настройки подключения изменены. Введите секрет повторно, чтобы проверить источник.')
      const payload = await request(`${api}/test`, 'POST', { ...config, ...(editing ? { source_id: editing.id } : {}) })
      setPreview(readObject(payload) as unknown as Preview); setNotice('Проверка конфигурации завершена.')
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось проверить источник.') }
    finally { setBusy('') }
  }

  async function saveConfig(keepOpen = false, preserveBusy = false): Promise<string | null> {
    if (!preserveBusy) setBusy('save'); setError(''); setNotice('')
    try {
      const payload = makePayload()
      const connectionChanged = Boolean(editing && (payload.endpoint_url !== editing.endpoint_url || payload.auth_type !== editing.auth_type || payload.auth_name !== (editing.auth_name ?? '') || !sameQueryParams(payload.query_params, editing.query_params ?? {})))
      if (editing?.secret_configured && payload.auth_type !== 'none' && connectionChanged && !payload.auth_secret) throw new Error('Настройки подключения изменены. Введите секрет повторно, чтобы сохранить источник.')
      const saved = readObject(await request(editing ? `${api}/${encodeURIComponent(editing.id)}` : api, editing ? 'PATCH' : 'POST', payload))
      const savedSource = readObject(saved.source)
      const sourceId = editing?.id ?? (typeof savedSource.id === 'string' ? savedSource.id : typeof saved.id === 'string' ? saved.id : null)
      if (sourceId) setEditing((current) => current ? { ...current, id: sourceId } : { id: sourceId, source_key: payload.source_key, name: payload.name, enabled: payload.enabled, endpoint_url: payload.endpoint_url, auth_type: payload.auth_type })
      await loadSources(); if (!keepOpen) setIsFormOpen(false); setNotice(editing ? 'Источник обновлён.' : 'Источник сохранён.')
      return sourceId
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось сохранить источник.') }
    finally { if (!preserveBusy) setBusy('') }
    return null
  }

  async function approvePreviewDomain(domain: ResourceDomain) {
    setBusy('domain'); setError(''); setNotice('')
    try {
      const sourceId = editing?.id ?? await saveConfig(true, true)
      if (!sourceId) throw new Error('Не удалось сохранить источник перед разрешением домена.')
      await approveDomain(sourceId, domain.hostname, domain.purpose)
      setNotice(`Разрешён домен ${domain.hostname} для ${domain.purpose === 'image' ? 'изображений' : 'билетов'}.`)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось разрешить домен.') }
    finally { setBusy('') }
  }

  async function addManualDomain() {
    const hostname = domainHostname.trim()
    if (!/^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{1,62}$/i.test(hostname)) { setError('Введите hostname без схемы, пути и подстановочных знаков.'); return }
    if (!editing?.id) { setError('Сначала сохраните источник.'); return }
    setBusy('domain'); setError(''); setNotice('')
    try { await approveDomain(editing.id, hostname.toLowerCase(), domainPurpose); setDomainHostname(''); setNotice(`Разрешён домен ${hostname.toLowerCase()}.`) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось разрешить домен.') }
    finally { setBusy('') }
  }

  async function syncSource(source: Source) {
    setBusy(`sync:${source.id}`); setError(''); setNotice(`Синхронизация «${source.name}» запущена.`)
    try {
      await request(`${api}/${encodeURIComponent(source.id)}/sync`, 'POST', {})
      const previousRun = source.last_sync_at ?? ''
      let observedRunning = false
      for (let attempt = 0; attempt < 20; attempt++) {
        await new Promise((resolve) => window.setTimeout(resolve, 2500))
        const refreshedSources = await loadSources()
        const refreshed = refreshedSources.find((item) => item.id === source.id)
        const status = refreshed?.last_sync_state ?? refreshed?.sync_status ?? refreshed?.last_sync_status
        if (status === 'running' || status === 'in_progress') observedRunning = true
        const runChanged = Boolean(refreshed?.last_sync_at && refreshed.last_sync_at !== previousRun)
        if ((observedRunning || runChanged) && status && status !== 'running' && status !== 'in_progress') { setNotice(`Синхронизация завершена: ${status}.`); break }
        if (attempt === 19) setNotice('Синхронизация принята. Список обновлён; проверьте статус позже.')
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось запустить синхронизацию.') }
    finally { setBusy('') }
  }

  async function disableSource(source: Source) {
    setBusy(`disable:${source.id}`); setError(''); setNotice('')
    try {
      const detail = await request(`${api}/${encodeURIComponent(source.id)}`, 'GET')
      const values = unwrapSource(detail) as unknown as SourceForm
      await request(`${api}/${encodeURIComponent(source.id)}`, 'PATCH', { ...writableForm(values), enabled: false, auth_secret: '' })
      await loadSources(); setNotice(`Источник «${source.name}» выключен.`)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Не удалось выключить источник.') }
    finally { setBusy('') }
  }

  return <main className={styles.page}>
    <header className={styles.header}><div className={styles.brand}><div className={styles.brandMark}>M</div><div><p>WORKNET <span>/ INTERNAL</span></p><h1>Источники событий</h1></div></div><div className={styles.headerActions}><span className={styles.secure}>Внутренний доступ</span><a className={`${styles.secondaryButton} ${styles.navLink}`} href="/internal/analytics">Аналитика →</a><button className={styles.primaryButton} onClick={openCreate}>＋ Добавить источник</button></div></header>
    <div className={styles.content}>
      <div className={styles.intro}><div><p className={styles.kicker}>УПРАВЛЕНИЕ ИМПОРТОМ</p><h2>Источники событий</h2><p>Проверьте JSON mapping и запускайте импорт в общую афишу.</p></div><button className={styles.secondaryButton} onClick={() => { setLoading(true); loadSources().catch((cause) => setError(cause instanceof Error ? cause.message : 'Ошибка загрузки')).finally(() => setLoading(false)) }}>Обновить список</button></div>
      {error && <div className={styles.alert} role="alert"><strong>Не удалось выполнить действие</strong><span>{error}</span></div>}
      {notice && <div className={styles.notice} role="status">{notice}</div>}
      {loading ? <div className={styles.empty}>Загружаем источники…</div> : <>
        <SourceSection title="Встроенные источники" hint="Состояние встроенных провайдеров. Настройки управляются системой." sources={builtIns} />
        <SourceSection title="Generic sources" hint="Подключённые API с настраиваемым JSON mapping." sources={genericSources} onEdit={openEdit} onSync={syncSource} onDisable={disableSource} busy={busy} />
      </>}
    </div>
    {isFormOpen && <div className={styles.modalBackdrop} role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setIsFormOpen(false) }}><section className={styles.formPanel} role="dialog" aria-modal="true" aria-labelledby="form-title">
      <div className={styles.formHeader}><div><p className={styles.kicker}>{editing ? 'ИЗМЕНЕНИЕ КОНФИГУРАЦИИ' : 'НОВЫЙ ИСТОЧНИК'}</p><h2 id="form-title">{editing ? form.name || 'Редактировать источник' : 'Подключить источник'}</h2></div><button className={styles.closeButton} aria-label="Закрыть" onClick={() => setIsFormOpen(false)}>×</button></div>
      <div className={styles.formBody}>
        <fieldset><legend>Connection</legend><div className={styles.formGrid}><label>Название<input value={form.name} onChange={(event) => setField('name', event.target.value)} required /></label><label>Endpoint URL<input type="url" placeholder="https://api.example.com/events" value={form.endpoint_url} onChange={(event) => setField('endpoint_url', event.target.value)} required /></label>
          <label>Authentication<select value={form.auth_type} onChange={(event) => setField('auth_type', event.target.value as AuthType)}><option value="none">None</option><option value="bearer">Bearer</option><option value="api_key_header">API Key Header</option><option value="api_key_query">API Key Query</option></select></label>
          {form.auth_type !== 'none' && <><label>{form.auth_type === 'bearer' ? 'Имя заголовка' : form.auth_type === 'api_key_header' ? 'Имя заголовка' : 'Имя query-параметра'}<input value={form.auth_type === 'bearer' ? form.auth_name || 'Authorization' : form.auth_name} onChange={(event) => setField('auth_name', event.target.value)} placeholder={form.auth_type === 'api_key_query' ? 'api_key' : 'X-API-Key'} /></label><label>Секрет<input type="password" autoComplete="new-password" value={form.auth_secret} onChange={(event) => setField('auth_secret', event.target.value)} placeholder={editing?.secret_configured ? 'Секрет сохранён · пустое поле оставит его без изменений' : 'Введите секрет'} /></label></>}
          <label className={styles.fullWidth}>Query parameters · JSON object<textarea value={queryText} onChange={(event) => setQueryText(event.target.value)} rows={3} spellCheck={false} /></label>
          <label className={styles.fullWidth}>Response path<input value={form.response_path} onChange={(event) => setField('response_path', event.target.value)} placeholder="data.events" /><small>Оставьте пустым, если массив находится в корне JSON.</small></label>
        </div></fieldset>
        <fieldset><legend>Pagination</legend><div className={styles.formGrid}><label>Режим<select value={form.pagination.mode} onChange={(event) => setPagination('mode', event.target.value)}><option value="none">None</option><option value="page">Page / Page Size</option><option value="offset">Offset / Limit</option></select></label>
          {form.pagination.mode === 'page' && <><label>Page parameter<input value={form.pagination.page_param} onChange={(event) => setPagination('page_param', event.target.value)} /></label><label>Page size parameter<input value={form.pagination.page_size_param} onChange={(event) => setPagination('page_size_param', event.target.value)} /></label><label>Page size<input type="number" min="1" value={form.pagination.page_size} onChange={(event) => setPagination('page_size', Number(event.target.value))} /></label></>}
          {form.pagination.mode === 'offset' && <><label>Offset parameter<input value={form.pagination.offset_param} onChange={(event) => setPagination('offset_param', event.target.value)} /></label><label>Limit parameter<input value={form.pagination.limit_param} onChange={(event) => setPagination('limit_param', event.target.value)} /></label><label>Limit<input type="number" min="1" value={form.pagination.limit} onChange={(event) => setPagination('limit', Number(event.target.value))} /></label></>}
        </div></fieldset>
        <fieldset><legend>Mapping</legend><p className={styles.fieldHint}>Dot-path до поля в каждой записи. Домены изображений и билетов можно обнаружить при проверке и разрешить явно.</p><div className={styles.mappingList}>{mappingFields.map(([key, label]) => <div className={styles.mappingRow} key={key}><span>{label}{['external_id', 'title', 'starts_at', 'venue_name'].includes(key) && <b aria-label="обязательное">*</b>}</span><input aria-label={`${label} path`} disabled={key === 'external_id' && editing?.mapping_locked} value={form.mapping[key]?.path ?? ''} onChange={(event) => setMapping(key, { path: event.target.value })} placeholder={key.replaceAll('_', '.')} /><select aria-label={`${label} transform`} disabled={key === 'external_id' && editing?.mapping_locked} value={form.mapping[key]?.transform ?? ''} onChange={(event) => setMapping(key, { transform: event.target.value || undefined })}><option value="">Без преобразования</option><option value="string">String</option><option value="number">Number</option><option value="iso_datetime">ISO datetime</option><option value="unix_timestamp">Unix timestamp</option><option value="strip_html">Strip HTML</option></select><input aria-label={`${label} default`} disabled={key === 'external_id' && editing?.mapping_locked} value={form.mapping[key]?.default ?? ''} onChange={(event) => setMapping(key, { default: event.target.value || undefined })} placeholder="Default" /></div>)}</div>{editing?.mapping_locked && <p className={styles.fieldHint}>Mapping External ID заблокирован после первого успешного импорта.</p>}</fieldset>
        <fieldset><legend>Defaults</legend><div className={styles.formGrid}><label>Категория<select value={form.defaults.category} onChange={(event) => setDefault('category', event.target.value)}>{CATEGORY_OPTIONS.map((category) => <option key={category.slug} value={category.slug}>{category.label}</option>)}</select></label><label>Timezone<input value={form.defaults.timezone} onChange={(event) => setDefault('timezone', event.target.value)} /></label><label>Currency<input value={form.defaults.currency} onChange={(event) => setDefault('currency', event.target.value)} /></label><label>Status<select value={form.defaults.status} onChange={(event) => setDefault('status', event.target.value)}><option value="published">Published</option><option value="sold_out">Sold out</option><option value="cancelled">Cancelled</option></select></label><label>Price unit<select value={form.price_unit} onChange={(event) => setField('price_unit', event.target.value as SourceForm['price_unit'])}><option value="major">Major units</option><option value="minor">Minor units</option></select></label><label>Source key<input value={editing?.source_key ?? (form.name ? `generic:${slug(form.name)}` : '')} readOnly placeholder="generic:source-name" /></label></div></fieldset>
        {preview && <section className={styles.preview} aria-label="Результат проверки"><h3>Результат проверки</h3><div className={styles.previewStats}><b>{preview.valid} валидных</b><span>{preview.invalid} с ошибками</span><span>Получено: {preview.received}</span>{preview.complete === false && <span>Показаны первые страницы</span>}</div>{preview.errors?.length > 0 && <ul>{preview.errors.map((item) => <li key={item.code}>{item.code}: {item.count}</li>)}</ul>}{preview.resource_domains?.length ? <section aria-label="Обнаружены внешние ресурсы"><h4>Обнаружены внешние ресурсы</h4>{preview.resource_domains.map((domain) => <article key={`${domain.purpose}:${domain.hostname}`}><strong>{domain.hostname}</strong><span>{domain.purpose === 'image' ? 'Изображения' : 'Билеты'} · {domain.count ?? 0} событий</span>{domain.approved ? <span>✓ Разрешён</span> : <><span>Не разрешён</span><button className={styles.secondaryButton} disabled={Boolean(busy)} onClick={() => void approvePreviewDomain(domain)}>{editing ? 'Разрешить' : 'Сохранить источник и разрешить домен'}</button></>}</article>)}</section> : null}{Boolean(preview.warnings?.length) && <ul aria-label="Ограничения ресурсов">{preview.warnings?.map((item) => <li key={item.code}>{item.code}: {item.count}</li>)}</ul>}<div className={styles.previewEvents}>{preview.preview?.slice(0, 5).map((item, index) => <article key={`${item.title}-${index}`}><strong>{item.title}</strong><span>{item.starts_at ? new Date(item.starts_at).toLocaleString('ru-RU') : 'Дата не указана'}</span><small>{item.venue}{item.price_from_minor != null ? ` · ${item.price_from_minor} minor units` : ''}</small></article>)}</div></section>}
        {editing && <fieldset><legend>Разрешённые домены источника</legend><div className={styles.formGrid}><label>Hostname<input value={domainHostname} onChange={(event) => setDomainHostname(event.target.value)} placeholder="cdn.partner.ru" autoComplete="off" /></label><label>Назначение<select value={domainPurpose} onChange={(event) => setDomainPurpose(event.target.value as 'image' | 'ticket')}><option value="image">Изображения</option><option value="ticket">Билеты</option></select></label><button className={styles.secondaryButton} disabled={Boolean(busy) || !domainHostname.trim()} onClick={() => void addManualDomain()}>Разрешить домен</button></div>{domains.length ? <ul aria-label="Текущие разрешения">{domains.map((domain) => <li key={domain.id ?? `${domain.purpose}:${domain.hostname}`}>{domain.hostname} · {domain.purpose === 'image' ? 'Изображения' : 'Билеты'} <button className={styles.textButton} disabled={Boolean(busy) || !domain.id} onClick={() => domain.id && void revokeDomain(domain.id, editing.id)}>Отозвать</button></li>)}</ul> : <p className={styles.fieldHint}>Нет разрешённых доменов.</p>}</fieldset>}
      </div>
      <footer className={styles.formFooter}><button className={styles.secondaryButton} disabled={Boolean(busy)} onClick={() => void testConfig()}>{busy === 'test' ? 'Проверяем…' : 'Проверить'}</button><div><button className={styles.secondaryButton} onClick={() => setIsFormOpen(false)}>Отмена</button><button className={styles.primaryButton} disabled={Boolean(busy)} onClick={() => void saveConfig()}>{busy === 'save' ? 'Сохраняем…' : 'Сохранить'}</button></div></footer>
    </section></div>}
  </main>
}

function SourceSection({ title, hint, sources, onEdit, onSync, onDisable, busy }: { title: string; hint: string; sources: Source[]; onEdit?: (source: Source) => void; onSync?: (source: Source) => void; onDisable?: (source: Source) => void; busy?: string }) {
  return <section className={styles.section}><div className={styles.sectionHeading}><div><h2>{title}</h2><p>{hint}</p></div><span className={styles.count}>{sources.length}</span></div>{sources.length === 0 ? <div className={styles.empty}>{title === 'Generic sources' ? 'Пока нет подключённых источников.' : 'Нет встроенных источников в ответе API.'}</div> : <div className={styles.sourceList}>{sources.map((source) => {
    const pending = busy === `sync:${source.id}` || busy === `disable:${source.id}`
    const status = source.last_sync_state ?? source.sync_status ?? source.last_sync_status
    const isRunning = status === 'running' || status === 'in_progress'
    return <article className={styles.sourceCard} key={source.id || source.source_key}><div className={styles.sourceIdentity}><span className={`${styles.statusDot} ${source.enabled ? styles.enabled : styles.disabled}`} /><div><h3>{source.name}</h3><p>{source.source_key}</p></div></div><div className={styles.sourceMeta}><span className={`${styles.badge} ${source.kind === 'built_in' ? styles.builtInBadge : ''}`}>{source.kind === 'built_in' ? 'BUILT-IN' : 'GENERIC'}</span><span>{source.enabled ? 'Включён' : 'Выключен'}</span>{source.secret_configured && <span>Секрет настроен</span>}{source.event_count != null && <span>{source.event_count.toLocaleString('ru-RU')} событий</span>}<span>Синхронизация: {status ?? 'нет данных'}</span></div><div className={styles.cardActions}>{onEdit && <button className={styles.secondaryButton} onClick={() => onEdit(source)}>Редактировать</button>}{onSync && <button className={styles.secondaryButton} disabled={pending || isRunning || !source.enabled} onClick={() => onSync(source)}>{pending || isRunning ? 'Синхронизация…' : 'Синхронизировать'}</button>}{onDisable && source.enabled && <button className={styles.textButton} disabled={pending} onClick={() => onDisable(source)}>Выключить</button>}</div></article>
  })}</div>}</section>
}

export { EventSourcesPage }
