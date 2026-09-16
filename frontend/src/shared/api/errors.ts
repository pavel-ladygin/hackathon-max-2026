export class ApiError extends Error {
  readonly code: string
  readonly requestId: string
  readonly status: number
  readonly details?: Record<string, unknown>
  constructor(status: number, body: { error?: { code?: string; message?: string; request_id?: string; details?: Record<string, unknown> } }) {
    super(body.error?.message ?? `Request failed (${status})`); this.name = 'ApiError'; this.status = status
    this.code = body.error?.code ?? 'INTERNAL'; this.requestId = body.error?.request_id ?? ''; this.details = body.error?.details
  }
}

