const csrfStorageKey = 'PSW_CSRF_TOKEN'

export function setCSRFToken(token: string) {
  if (token) sessionStorage.setItem(csrfStorageKey, token)
  else sessionStorage.removeItem(csrfStorageKey)
}

export function clearCSRFToken() {
  sessionStorage.removeItem(csrfStorageKey)
}

export function csrfHeaders(): Record<string, string> {
  const token = sessionStorage.getItem(csrfStorageKey) || ''
  return token ? { 'X-CSRF-Token': token } : {}
}

function needsCSRF(method?: string): boolean {
  const normalized = (method || 'GET').toUpperCase()
  return normalized !== 'GET' && normalized !== 'HEAD' && normalized !== 'OPTIONS'
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const isFormData = Object.prototype.toString.call(init.body) === '[object FormData]'
  const headers = { ...(init.headers as Record<string, string> || {}) }
  if (needsCSRF(init.method)) {
    Object.assign(headers, csrfHeaders())
  }
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers: isFormData
      ? headers
      : { 'Content-Type': 'application/json', ...headers }
  })
  const text = await response.text()
  let body: any = {}
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = { error: text.trim() }
    }
  }
  if (response.status === 401 && path !== '/api/login') {
    clearCSRFToken()
    location.replace('/login')
    throw new Error('登录状态已失效')
  }
  if (response.status === 403) {
    clearCSRFToken()
    location.replace('/login')
    throw new Error('CSRF token 已过期，请重新登录')
  }
  if (!response.ok) throw new Error(body.error || `请求失败 (${response.status})`)
  return body as T
}
