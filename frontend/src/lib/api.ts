// Thin fetch wrapper: JSON in/out, CSRF double-submit header on mutating
// requests, and auth-aware redirects on 401/428.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)landash_csrf=([^;]+)/)
  return m ? decodeURIComponent(m[1]) : ''
}

type Listener = (status: number) => void
const authListeners = new Set<Listener>()
/** Subscribe to 401/428 responses (used by the auth provider). */
export function onAuthError(fn: Listener) {
  authListeners.add(fn)
  return () => authListeners.delete(fn)
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken()
  const res = await fetch(path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401 || res.status === 428) {
    if (!path.startsWith('/api/auth/')) authListeners.forEach((fn) => fn(res.status))
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: any
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    data = undefined
  }
  if (!res.ok) throw new ApiError(res.status, data?.error ?? (text.slice(0, 200) || res.statusText || `HTTP ${res.status}`))
  if (text && data === undefined) throw new ApiError(res.status, 'Unexpected non-JSON response from server')
  return data as T
}

export const get = <T,>(p: string) => api<T>('GET', p)
export const post = <T,>(p: string, b?: unknown) => api<T>('POST', p, b ?? {})
export const put = <T,>(p: string, b: unknown) => api<T>('PUT', p, b)
export const del = <T,>(p: string) => api<T>('DELETE', p)
