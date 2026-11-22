import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'

// A fake API, stubbed before the app's modules create the client.
type Reply = { status: number; body?: unknown }
let routes: Record<string, (body: Record<string, unknown>) => Reply> = {}
const requests: { method: string; path: string; body: Record<string, unknown> }[] = []

vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  const body = req.method === 'GET' || req.method === 'DELETE' ? {} : ((await req.json().catch(() => ({}))) as Record<string, unknown>)
  requests.push({ method: req.method, path, body })
  const handler = routes[`${req.method} ${path}`]
  const r = handler ? handler(body) : { status: 404, body: { detail: 'not found' } }
  return new Response(r.body === undefined ? null : JSON.stringify(r.body), {
    status: r.status,
    headers: { 'Content-Type': 'application/json' },
  })
})

let router: Router
let LoginView: (typeof import('@/views/LoginView.vue'))['default']
let AdminInviteLinksView: (typeof import('@/views/admin/AdminInviteLinksView.vue'))['default']
let session: (typeof import('@/session'))['session']

beforeAll(async () => {
  router = (await import('@/router')).router
  LoginView = (await import('@/views/LoginView.vue')).default
  AdminInviteLinksView = (await import('@/views/admin/AdminInviteLinksView.vue')).default
  session = (await import('@/session')).session
})

const token = 'A'.repeat(43)
const user = { id: 'u1', email: 'new@example.com', role: 'user', created_at: '2026-11-20T00:00:00Z' }
const site = { service_name: '折光观察者', registration_mode: 'invite', watch_days: 60, codes_per_day: 2, code_ttl_minutes: 10, max_open_watches: 50 }
const gone = { status: 410, body: { detail: '这个邀请链接已失效。已有账号可以直接用邮箱登录。' } }

beforeEach(() => {
  routes = {
    'GET /api/v1/auth/session': () => ({ status: 200, body: {} }),
    'GET /api/v1/site': () => ({ status: 200, body: site }),
    'POST /api/v1/auth/invite-link': (b) => (b.token === token ? { status: 200, body: { expires_at: '2026-11-29T12:30:00+08:00' } } : gone),
  }
})

afterEach(() => {
  sessionStorage.clear()
  requests.length = 0
  document.body.innerHTML = ''
  if (session) session.user = null
})

async function mountAt(path: string) {
  await router.push(path)
  await router.isReady()
  const w = mount(LoginView, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return w
}

const bodyOf = (path: string) => requests.filter((r) => r.path === path).at(-1)?.body

describe('/join', () => {
  it('signs up through an open link, keeping its token across a reload', async () => {
    routes['POST /api/v1/auth/code'] = () => ({ status: 202, body: { message: '如果该邮箱可以登录，几分钟内会收到验证码，10 分钟内有效。', remaining: 1, resend_after: 60 } })
    routes['POST /api/v1/auth/verify'] = () => ({ status: 200, body: { user } })

    let w = await mountAt(`/join#t=${token}`)
    expect(bodyOf('/api/v1/auth/invite-link')).toEqual({ token })
    expect(router.currentRoute.value.hash).toBe('') // the token leaves the address bar
    expect(w.find('h1').text()).toBe('接受邀请')
    expect(w.text()).toContain('邀请有效期至 2026-11-29')
    expect(w.text()).not.toContain('名额')
    await w.find('input[name=email]').setValue('new@example.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(bodyOf('/api/v1/auth/code')).toEqual({ email: 'new@example.com', invite_token: token })

    // The mail app reloads the page, without the fragment.
    w.unmount()
    w = await mountAt('/join')
    expect(bodyOf('/api/v1/auth/invite-link')).toEqual({ token })
    expect(w.find('input[name=code]').exists()).toBe(true)
    await w.find('input[name=code]').setValue('123456')
    await flushPromises()
    expect(bodyOf('/api/v1/auth/verify')).toEqual({ email: 'new@example.com', code: '123456', invite_token: token })
    expect(router.currentRoute.value.path).toBe('/watches')
    expect(sessionStorage.getItem('join-link')).toBeNull()
  })

  it('says when the link no longer works', async () => {
    const w = await mountAt('/join#t=nope')
    expect(w.text()).toContain('邀请已失效')
    expect(w.find('input[name=email]').exists()).toBe(false)
    expect(w.find('a[href="/login"]').exists()).toBe(true)
  })

  it('says so when the link closes before the code is sent', async () => {
    routes['POST /api/v1/auth/code'] = () => gone
    const w = await mountAt(`/join#t=${token}`)
    await w.find('input[name=email]').setValue('late@example.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('邀请已失效')
    expect(w.find('input[name=email]').exists()).toBe(false)
  })

  it('keeps the link when today\'s budget for its codes is used up', async () => {
    const detail = '今天通过邀请链接发送的验证码已达上限，请明天再试。已有账号可以在登录页正常登录。'
    routes['POST /api/v1/auth/code'] = () => ({ status: 503, body: { detail } })
    const w = await mountAt(`/join#t=${token}`)
    await w.find('input[name=email]').setValue('busy@example.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain(detail)
    expect(w.text()).not.toContain('邀请已失效')
    expect(w.find('input[name=email]').exists()).toBe(true)
    expect(sessionStorage.getItem('join-link')).not.toBeNull()
  })

  it('treats a missing token as an invalid link', async () => {
    const w = await mountAt('/join')
    expect(w.text()).toContain('邀请已失效')
    expect(requests.some((r) => r.path === '/api/v1/auth/invite-link')).toBe(false)
  })

  it('asks a logged-in visitor to log out first', async () => {
    await router.push('/')
    await router.isReady()
    session.user = { ...user, email: 'me@example.com' } as never
    const w = await mountAt(`/join#t=${token}`)
    expect(w.text()).toContain('当前已登录 me@example.com')
    expect(w.find('input[name=email]').exists()).toBe(false)
  })
})

const link = {
  id: 'l1', note: '读者群', max_uses: 20, used: 3, accounts: 2, expires_at: '2026-11-29T12:00:00Z',
  created_at: '2026-11-22T12:00:00Z', created_by: 'ops@example.com', status: 'active', url: `https://ob.example/join#t=${token}`,
}

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, 'clipboard', { value, configurable: true })
}

describe('AdminInviteLinksView', () => {
  async function mountAdmin() {
    session.site = site as never
    const w = mount(AdminInviteLinksView, { global: { plugins: [router] }, attachTo: document.body })
    await flushPromises()
    return w
  }

  it('lists, creates and revokes links', async () => {
    let items = [link]
    routes['GET /api/v1/admin/invite-links'] = () => ({ status: 200, body: { items } })
    routes['POST /api/v1/admin/invite-links'] = (b) => ({ status: 201, body: { ...link, id: 'l2', note: b.note ?? '', used: 0, accounts: 0, max_uses: b.max_uses } })
    routes['DELETE /api/v1/admin/invite-links/l1'] = () => {
      items = [{ ...link, status: 'revoked' }]
      return { status: 204 }
    }
    const writeText = vi.fn(async () => {})
    setClipboard({ writeText })

    const w = await mountAdmin()
    expect(w.text()).toContain('读者群')
    expect(w.text()).toContain('3 / 20')
    expect(w.find('a[href="/admin/users?invite_link=l1"]').text()).toBe('2 个账户')

    await w.find('button[aria-expanded]').trigger('click')
    await w.find('form input[type=number]').setValue(5)
    await w.find('form select').setValue('3')
    await w.find('form input[maxlength]').setValue(' 公众号 ')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(bodyOf('/api/v1/admin/invite-links')).toEqual({ max_uses: 5, valid_hours: 72, note: '公众号' })
    expect(writeText).toHaveBeenCalledWith(link.url)
    expect(w.text()).toContain('链接已创建并复制')
    expect(w.findAll('tbody tr')).toHaveLength(2)

    // Revoking takes a second click.
    const revoke = () => w.findAll('tbody tr').find((r) => r.text().includes('读者群'))!.findAll('button').find((b) => b.text().includes('撤销'))!
    await revoke().trigger('click')
    expect(requests.some((r) => r.method === 'DELETE')).toBe(false)
    expect(revoke().text()).toBe('确认撤销')
    await revoke().trigger('click')
    await flushPromises()
    expect(requests.some((r) => r.method === 'DELETE' && r.path === '/api/v1/admin/invite-links/l1')).toBe(true)
    expect(w.text()).toContain('已撤销')
  })

  it('shows the link to copy by hand without a clipboard', async () => {
    routes['GET /api/v1/admin/invite-links'] = () => ({ status: 200, body: { items: [link] } })
    const w = await mountAdmin()
    setClipboard(undefined)
    await w.findAll('button').find((b) => b.text() === '复制链接')!.trigger('click')
    await flushPromises()
    expect((w.find('input[readonly]').element as HTMLInputElement).value).toBe(link.url)
  })
})
