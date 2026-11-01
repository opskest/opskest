import { ApiError } from './api'

/** Credential rejections are local form errors, not global session-expiry events. */
export async function passwordAuth<T>(path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    const cookie = document.cookie.split(';').map(c => c.trim()).find(c => c.startsWith('opskest.csrf='))
    if (cookie) headers['X-CSRF-Token'] = decodeURIComponent(cookie.slice('opskest.csrf='.length))
  }
  let response: Response
  try {
    response = await fetch(path, { method: body === undefined ? 'GET' : 'POST', credentials: 'include', headers, body: body === undefined ? undefined : JSON.stringify(body) })
  } catch { throw new ApiError(0, 'Could not connect. Check your connection and try again.') }
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new ApiError(response.status, typeof data?.error === 'string' ? data.error : response.status === 429 ? 'Too many attempts. Please wait before trying again.' : 'The request could not finish. Please try again.')
  return data as T
}
