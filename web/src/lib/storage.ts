// sessionStorage can be missing or throw (private mode, blocked storage);
// everything that uses it must work without it.

export function load<T>(key: string): T | null {
  try {
    const s = sessionStorage.getItem(key)
    return s ? (JSON.parse(s) as T) : null
  } catch {
    return null
  }
}

export function save(key: string, value: unknown): void {
  try {
    sessionStorage.setItem(key, JSON.stringify(value))
  } catch {
    // ignore
  }
}

export function remove(key: string): void {
  try {
    sessionStorage.removeItem(key)
  } catch {
    // ignore
  }
}
