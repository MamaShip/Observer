import * as storage from './storage'

// Links in the Official Account's replies carry a one-time token after #t=:
// browsers never send the fragment to a server, so it stays out of logs and
// Referer headers. A page takes it out of the address bar at once and keeps
// it in sessionStorage, so a login in between (or a reload) does not lose it.

export type LinkKind = 'bind' | 'go'

const tokenRe = /^#t=([A-Za-z0-9_-]{43})$/

/** isTokenHash tells a reply link's fragment from an in-page anchor. */
export function isTokenHash(hash: string): boolean {
  return hash.startsWith('#t=')
}

/**
 * takeToken returns the token of a reply link: from the fragment (which it
 * stores) or, after a detour such as logging in, from storage.
 */
export function takeToken(kind: LinkKind, hash: string): string | null {
  const m = tokenRe.exec(hash)
  if (m) {
    storage.save(key(kind), m[1])
    return m[1]!
  }
  return storage.load<string>(key(kind))
}

/** dropToken forgets a token once used (or found useless). */
export function dropToken(kind: LinkKind): void {
  storage.remove(key(kind))
}

function key(kind: LinkKind): string {
  return `wechat-${kind}`
}
