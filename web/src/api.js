// Thin fetch wrapper. The SPA and API are same-origin (the binary serves both),
// so the session cookie is sent automatically.
export class Unauthorized extends Error {}

export async function api(path, opts = {}) {
  const res = await fetch(path, { credentials: 'same-origin', ...opts })
  if (res.status === 401) throw new Unauthorized()
  if (!res.ok) throw new Error(await res.text())
  if (res.status === 204) return null
  const ct = res.headers.get('content-type') || ''
  return ct.includes('application/json') ? res.json() : res.text()
}
