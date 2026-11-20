import { reactive } from 'vue'
import { api, call, type Site, type User } from './api/client'

/** What the app knows about the visitor and the service. */
export const session = reactive({
  user: null as User | null,
  site: null as Site | null,
  loaded: false,
})

let loading: Promise<void> | null = null

/** ensureSession loads the current user and the site settings once. */
export function ensureSession(): Promise<void> {
  loading ??= (async () => {
    // /auth/session answers visitors with 200 (no user), unlike /me's 401,
    // so loading a page as a visitor logs no error in the console.
    const [me, site] = await Promise.allSettled([call(api.GET('/api/v1/auth/session')), call(api.GET('/api/v1/site'))])
    session.user = me.status === 'fulfilled' ? (me.value.user ?? null) : null
    session.site = site.status === 'fulfilled' ? site.value : null
    session.loaded = true
  })()
  return loading
}

export function setUser(u: User | null): void {
  session.user = u
}

/** serviceName is the configured name, or the default before /site loads. */
export function serviceName(): string {
  return session.site?.service_name || '折光观察者'
}
