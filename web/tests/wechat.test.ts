import { flushPromises, mount } from '@vue/test-utils'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'

// A fake API, stubbed before the app's modules create the client.
type Reply = { status: number; body?: unknown }
let routes: Record<string, (body: Record<string, unknown>) => Reply> = {}
let calls: string[] = []

vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  const body = req.method === 'GET' ? {} : ((await req.json().catch(() => ({}))) as Record<string, unknown>)
  calls.push(`${req.method} ${path}`)
  const handler = routes[`${req.method} ${path}`]
  const r = handler ? handler(body) : { status: 404, body: { detail: 'not found' } }
  return new Response(r.body === undefined ? null : JSON.stringify(r.body), {
    status: r.status,
    headers: { 'Content-Type': 'application/json' },
  })
})

let router: Router
let session: (typeof import('@/session'))['session']
let lib: typeof import('@/lib/wechat')

beforeAll(async () => {
  router = (await import('@/router')).router
  session = (await import('@/session')).session
  lib = await import('@/lib/wechat')
})

const token = 'A'.repeat(42) + 'b'
const user = { id: 'u1', email: 'alice@example.com', role: 'user', created_at: '2026-11-20T00:00:00Z' }
const site = { service_name: '折光观察者', wechat: { name: '折光观察者' } }

beforeEach(async () => {
  sessionStorage.clear()
  calls = []
  routes = {
    'GET /api/v1/auth/session': () => ({ status: 200, body: {} }),
    'GET /api/v1/site': () => ({ status: 200, body: site }),
  }
  session.user = null
  session.site = site as never
  await router.push('/')
})

async function open(path: string, view: 'WechatBindView' | 'WechatGoView' | 'SettingsView' | 'LoginView') {
  await router.push(path)
  await router.isReady()
  const component = (await import(`@/views/${view}.vue`)).default
  const w = mount(component, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

describe('reply link tokens', () => {
  it('are taken from the fragment and kept for after a login', () => {
    expect(lib.isTokenHash('#t=' + token)).toBe(true)
    expect(lib.isTokenHash('#history')).toBe(false)
    expect(lib.takeToken('bind', '#t=' + token)).toBe(token)
    expect(lib.takeToken('bind', '')).toBe(token)
    expect(lib.takeToken('go', '')).toBeNull()
    expect(lib.takeToken('go', '#t=short')).toBeNull()
    lib.dropToken('bind')
    expect(lib.takeToken('bind', '')).toBeNull()
  })
})

describe('the login link (/wx/go)', () => {
  it('signs in and opens the page', async () => {
    let sent: Record<string, unknown> = {}
    routes['POST /api/v1/wechat/login'] = (b) => {
      sent = b
      return { status: 200, body: { user } }
    }
    await open(`/wx/go?to=/watches/w1#t=${token}`, 'WechatGoView')
    expect(sent).toEqual({ token })
    expect(session.user?.email).toBe('alice@example.com')
    expect(router.currentRoute.value.fullPath).toBe('/watches/w1')
    expect(lib.takeToken('go', '')).toBeNull()
  })

  it('opens the page anyway when already logged in', async () => {
    routes['POST /api/v1/wechat/login'] = () => ({ status: 410, body: { detail: 'gone' } })
    session.user = user as never
    await open(`/wx/go?to=/watches/w1#t=${token}`, 'WechatGoView')
    expect(router.currentRoute.value.fullPath).toBe('/watches/w1')
  })

  it('asks to log in otherwise, without the token in the address', async () => {
    routes['POST /api/v1/wechat/login'] = () => ({ status: 410, body: { detail: 'gone' } })
    await open(`/wx/go?to=/watches/w1#t=${token}`, 'WechatGoView')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.next).toBe('/watches/w1')
    expect(router.currentRoute.value.fullPath).not.toContain(token)
  })

  it('stays on the site', async () => {
    session.user = user as never
    await open('/wx/go?to=//evil.example/x', 'WechatGoView')
    expect(router.currentRoute.value.fullPath).toBe('/watches')
    expect(calls).not.toContain('POST /api/v1/wechat/login')
  })
})

describe('the bind link (/wx/bind)', () => {
  it('sends visitors to log in first, keeping the token out of the address', async () => {
    await open(`/wx/bind#t=${token}`, 'WechatBindView')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.next).toBe('/wx/bind')
    expect(router.currentRoute.value.fullPath).not.toContain(token)
    expect(lib.takeToken('bind', '')).toBe(token)
  })

  it('binds after confirming, and links to the article sent before', async () => {
    let sent: Record<string, unknown> = {}
    routes['POST /api/v1/wechat/bind'] = (b) => {
      sent = b
      return { status: 200, body: { watch_id: 'w9' } }
    }
    session.user = user as never
    lib.takeToken('bind', '#t=' + token) // came back from the login page
    const w = await open('/wx/bind', 'WechatBindView')
    expect(w.find('.bind-email').text()).toBe('alice@example.com')
    expect(calls).not.toContain('POST /api/v1/wechat/bind')
    await w.find('button.zg-btn').trigger('click')
    await flushPromises()
    expect(sent).toEqual({ token })
    expect(w.find('h1').text()).toBe('已绑定')
    expect(w.find('a[href="/watches/w9"]').exists()).toBe(true)
    expect(lib.takeToken('bind', '')).toBeNull()
  })

  it('says why the article was not watched', async () => {
    routes['POST /api/v1/wechat/bind'] = () => ({ status: 200, body: { submit_error: '同时观察的文章已达上限（50 篇）' } })
    session.user = user as never
    const w = await open(`/wx/bind#t=${token}`, 'WechatBindView')
    expect(router.currentRoute.value.fullPath).toBe('/wx/bind')
    await w.find('button.zg-btn').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('没能开始观察：同时观察的文章已达上限')
  })

  it('explains a used or missing link', async () => {
    routes['POST /api/v1/wechat/bind'] = () => ({ status: 410, body: { detail: 'gone' } })
    session.user = user as never
    let w = await open(`/wx/bind#t=${token}`, 'WechatBindView')
    await w.find('button.zg-btn').trigger('click')
    await flushPromises()
    expect(w.find('h1').text()).toBe('链接已失效')
    expect(w.text()).toContain('只能使用一次')
    w = await open('/wx/bind', 'WechatBindView')
    expect(w.text()).toContain('这个链接不完整')
  })
})

describe('the site and the Official Account', () => {
  const other = { ...site, registration_mode: 'invite', wechat: { name: '时间从来不回答' } }

  it('are named together on the bind page', async () => {
    session.site = other as never
    session.user = user as never
    const w = await open(`/wx/bind#t=${token}`, 'WechatBindView')
    expect(w.find('.wx-origin').text()).toBe('「折光观察者」是公众号「时间从来不回答」配套的文章备份网站')
    session.site = site as never
    const same = await open(`/wx/bind#t=${token}`, 'WechatBindView')
    expect(same.find('.wx-origin').exists()).toBe(false)
  })

  it('and on the login page, when sent there from a bind link', async () => {
    session.site = other as never
    let w = await open('/login?next=/wx/bind', 'LoginView')
    expect(w.text()).toContain('「折光观察者」是公众号「时间从来不回答」配套的文章备份网站。用邮箱登录后，就能完成微信绑定。')
    expect(w.text()).not.toContain('自动创建账号') // invitation only
    w = await open('/login', 'LoginView')
    expect(w.text()).not.toContain('配套的文章备份网站')
  })
})

describe('the settings card', () => {
  const me = { ...user, emails_this_month: 0, monthly_emails: 20 }

  it('tells how to bind', async () => {
    routes['GET /api/v1/me'] = () => ({ status: 200, body: me })
    session.user = user as never
    const w = await open('/settings', 'SettingsView')
    expect(w.text()).toContain('微信公众号')
    expect(w.text()).toContain('发送「绑定」')
    expect(w.find('img[src="/wechat-qr.jpg"]').attributes('alt')).toBe('公众号「折光观察者」的二维码')
  })

  it('unbinds after confirming', async () => {
    let bound = true
    routes['GET /api/v1/me'] = () => ({ status: 200, body: bound ? { ...me, wechat: { bound_at: '2026-11-21T02:00:00Z' } } : me })
    routes['DELETE /api/v1/me/wechat'] = () => {
      bound = false
      return { status: 204 }
    }
    session.user = user as never
    const w = await open('/settings', 'SettingsView')
    expect(w.text()).toContain('2026-11-21 绑定')
    const button = () => w.findAll('button').find((b) => b.text() === '解除绑定' || b.text() === '确认解除')!
    await button().trigger('click')
    expect(bound).toBe(true)
    await button().trigger('click')
    await flushPromises()
    expect(bound).toBe(false)
    expect(w.text()).toContain('未绑定')
  })

  it('is hidden without the Official Account', async () => {
    routes['GET /api/v1/me'] = () => ({ status: 200, body: me })
    session.user = user as never
    session.site = { service_name: '折光观察者' } as never
    const w = await open('/settings', 'SettingsView')
    expect(w.text()).not.toContain('微信公众号')
    expect(w.find('img[src="/wechat-qr.jpg"]').exists()).toBe(false)
  })
})
