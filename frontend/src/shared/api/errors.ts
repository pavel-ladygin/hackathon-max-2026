export type ErrorKind = 'validation' | 'auth' | 'forbidden' | 'notFound' | 'conflict' | 'rateLimit' | 'network' | 'unavailable' | 'unknown'
type ErrorPolicy = { kind: ErrorKind; message: string; retryable?: boolean; terminal?: boolean }

const policies: Record<string, ErrorPolicy> = {
  UNAUTHENTICATED: { kind: 'auth', message: 'Сессия не найдена. Откройте приложение заново.', terminal: true },
  TOKEN_EXPIRED: { kind: 'auth', message: 'Сессия истекла. Откройте приложение заново.', terminal: true },
  FORBIDDEN: { kind: 'forbidden', message: 'У вас нет доступа к этому действию.', terminal: true },
  VALIDATION_FAILED: { kind: 'validation', message: 'Проверьте заполненные поля.' },
  NOT_FOUND: { kind: 'notFound', message: 'Запрошенные данные не найдены.', terminal: true },
  CONFLICT: { kind: 'conflict', message: 'Данные изменились. Обновите экран и повторите действие.', retryable: true },
  IDEMPOTENCY_CONFLICT: { kind: 'conflict', message: 'Операция уже выполнялась с другими данными.' },
  ACTIVE_ROOM_EXISTS: { kind: 'conflict', message: 'У вас уже есть активная комната.' },
  RATE_LIMITED: { kind: 'rateLimit', message: 'Слишком много запросов. Подождите немного.', retryable: true },
  ROOM_FULL: { kind: 'conflict', message: 'Комната уже заполнена.', terminal: true },
  INVITE_EXPIRED: { kind: 'unavailable', message: 'Срок приглашения истёк.', terminal: true },
  ROOM_EXPIRED: { kind: 'unavailable', message: 'Срок действия комнаты истёк.', terminal: true },
  ROOM_NOT_READY: { kind: 'conflict', message: 'Комната ещё не готова.', retryable: true },
  POOL_NOT_READY: { kind: 'conflict', message: 'Подборка ещё формируется.', retryable: true },
  POOL_EXHAUSTED: { kind: 'conflict', message: 'Карточки этого раунда закончились.' },
  STALE_POOL_VERSION: { kind: 'conflict', message: 'Подборка обновилась. Загружаем новую версию.', retryable: true },
  INTENT_LOCKED: { kind: 'conflict', message: 'Пожелания этого раунда уже нельзя изменить.', terminal: true },
  VOTE_ALREADY_CAST: { kind: 'conflict', message: 'Этот голос уже сохранён.', retryable: true },
  ALREADY_MATCHED: { kind: 'conflict', message: 'Совпадение уже найдено.', terminal: true },
  ROUND_LIMIT_REACHED: { kind: 'unavailable', message: 'Доступные раунды закончились.', terminal: true },
  EVENT_UNAVAILABLE: { kind: 'unavailable', message: 'Событие больше недоступно.', terminal: true },
  TICKETS_UNAVAILABLE: { kind: 'unavailable', message: 'Билеты сейчас недоступны.' },
  INTERNAL: { kind: 'unknown', message: 'Сервис временно недоступен.', retryable: true },
}

export class ApiError extends Error {
  readonly code: string
  readonly requestId: string
  readonly status: number
  readonly details?: Record<string, unknown>
  readonly kind: ErrorKind
  readonly retryable: boolean
  readonly terminal: boolean
  readonly fieldErrors: Record<string, string>
  readonly retryAfterSeconds: number | null
  constructor(status: number, body: { error?: { code?: string; message?: string; request_id?: string; details?: Record<string, unknown> } } = {}, retryAfter?: string | null) {
    const code = body.error?.code ?? 'INTERNAL'
    const policy = policies[code] ?? { kind: status === 0 ? 'network' : 'unknown', message: status === 0 ? 'Нет соединения с сервисом.' : 'Не удалось выполнить действие.', retryable: status === 0 || status >= 500 }
    super(policy.message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = body.error?.request_id ?? ''
    this.details = body.error?.details
    this.kind = policy.kind
    this.retryable = Boolean(policy.retryable)
    this.terminal = Boolean(policy.terminal)
    this.fieldErrors = extractFieldErrors(body.error?.details)
    const parsedRetry = Number(retryAfter ?? body.error?.details?.retry_after_seconds)
    this.retryAfterSeconds = Number.isFinite(parsedRetry) && parsedRetry >= 0 ? parsedRetry : null
  }
}

function extractFieldErrors(details?: Record<string, unknown>): Record<string, string> {
  if (!details) return {}
  const result: Record<string, string> = {}
  if (typeof details.field === 'string') result[details.field] = typeof details.reason === 'string' ? details.reason : 'Проверьте значение.'
  if (Array.isArray(details.fields)) for (const value of details.fields) {
    if (value && typeof value === 'object' && 'field' in value && typeof value.field === 'string') result[value.field] = 'reason' in value && typeof value.reason === 'string' ? value.reason : 'Проверьте значение.'
  }
  return result
}

export function normalizeApiError(error: unknown): ApiError {
  if (error instanceof ApiError) return error
  return new ApiError(0, { error: { code: 'INTERNAL' } })
}
