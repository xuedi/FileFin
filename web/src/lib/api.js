// Thin fetch wrapper shared by every view: throws the Response on a non-2xx so callers can
// read the server's error text, and decodes JSON only when the response carries it.
export async function api(path, opts) {
  const res = await fetch(path, opts)
  if (!res.ok) throw res
  const ct = res.headers.get('content-type') || ''
  return ct.includes('application/json') ? res.json() : null
}

// errText pulls a trimmed server error message out of a thrown Response, falling back to ''.
export async function errText(e) {
  return e instanceof Response ? (await e.text()).trim() : ''
}
