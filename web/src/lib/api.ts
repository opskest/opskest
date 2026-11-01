// Thin JSON fetch wrapper for /api. Cookies (`opskest.sid`) carry the
// session; the CSRF double-submit cookie (`opskest.csrf`) is echoed back as
// the `X-CSRF-Token` header on every mutating request.

import type { FieldErrors } from './types'

/** Dispatched on `window` whenever a request comes back 401. */
export const UNAUTHENTICATED_EVENT = 'opskest:unauthenticated'
/** Dispatched on `window` when a request is refused because the workspace requires two-factor and this account has none. */
export const TWO_FACTOR_REQUIRED_EVENT = 'opskest:two-factor-required'

export class ApiError extends Error {
  status: number
  fields?: FieldErrors
  /** Seconds from a Retry-After header (429 and similar), when the server sent one. */
  retryAfter?: number

  constructor(status: number, message: string, fields?: FieldErrors, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.fields = fields
    this.retryAfter = retryAfter
  }
}

function readCookie(name: string): string | null {
  const prefix = `${name}=`
  for (const part of document.cookie.split(';')) {
    const c = part.trim()
    if (c.startsWith(prefix)) return decodeURIComponent(c.slice(prefix.length))
  }
  return null
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

async function request<T>(method: string, path: string, body?: unknown, raw?: { body: BodyInit; contentType?: string }): Promise<T> {
  const headers: Record<string, string> = {}
  const init: RequestInit = { method, credentials: 'include', headers }

  if (raw) {
    if (raw.contentType) headers['Content-Type'] = raw.contentType
    init.body = raw.body
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  if (method !== 'GET') {
    const csrf = readCookie('opskest.csrf')
    if (csrf) headers['X-CSRF-Token'] = csrf
  }

  let res: Response
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError(0, 'Network error — check your connection.')
  }

  if (res.status === 401) window.dispatchEvent(new Event(UNAUTHENTICATED_EVENT))

  const text = await res.text()
  const data = text ? safeParse(text) : undefined

  if (!res.ok) {
    const obj = data && typeof data === 'object' ? data as Record<string, unknown> : null
    if (res.status === 403 && obj && obj.code === 'two_factor_required') window.dispatchEvent(new Event(TWO_FACTOR_REQUIRED_EVENT))
    const message = obj && typeof obj.error === 'string'
      ? obj.error
      : res.status === 429 ? 'Too many requests — wait a few seconds and try again.' : `${res.status} ${res.statusText}`.trim()
    const fields = obj && obj.fields && typeof obj.fields === 'object' ? obj.fields as FieldErrors : undefined
    const ra = Number(res.headers.get('Retry-After'))
    throw new ApiError(res.status, message, fields, Number.isFinite(ra) && ra > 0 ? Math.ceil(ra) : undefined)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {}),
  del: <T>(path: string) => request<T>('DELETE', path),
  /** Sends a non-JSON body (CSV text, FormData upload) with CSRF + error handling. */
  send: <T>(method: string, path: string, body: BodyInit, contentType?: string) =>
    request<T>(method, path, undefined, { body, contentType }),
}

/** A list response is `{key:[…]}`, a bare array, or `{key:null}` (empty). */
export function unwrapList<T>(data: unknown, key: string): T[] {
  if (Array.isArray(data)) return data as T[]
  if (data && typeof data === 'object') {
    const v = (data as Record<string, unknown>)[key]
    if (Array.isArray(v)) return v as T[]
  }
  return []
}

/**
 * A paginated list: `nextBefore` is a number (fetch more), null (the server
 * said that's all) or undefined (the endpoint doesn't say; infer from a full
 * page).
 */
export interface PageOf<T> {
  rows: T[]
  nextBefore: number | null | undefined
}

export function unwrapPage<T>(data: unknown, key: string): PageOf<T> {
  const rows = unwrapList<T>(data, key)
  if (data && typeof data === 'object' && 'nextBefore' in (data as Record<string, unknown>)) {
    const nb = (data as Record<string, unknown>).nextBefore
    return { rows, nextBefore: typeof nb === 'number' ? nb : null }
  }
  return { rows, nextBefore: undefined }
}

/** Whether there is another page, given what came back. */
export function hasMorePages(page: PageOf<unknown>, limit: number): boolean {
  if (page.nextBefore === null) return false
  if (page.nextBefore !== undefined) return true
  return page.rows.length >= limit
}

export function errorText(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback
}
