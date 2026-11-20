import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'

// A fake API. openapi-fetch takes globalThis.fetch when the client is
// created, so the stub goes in before the app's modules are imported.
type Reply = { status: number; body?: unknown; headers?: Record<string, string> }
let routes: Record<string, (body: Record<string, unknown>) => Reply> = {}
const requests: { path: string; body: Record<string, unknown> }[] = []

vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  const body = req.method === 'GET' ? {} : ((await req.json().catch(() => ({}))) as Record<string, unknown>)
  requests.push({ path, body })
  const handler = routes[`${req.method} ${path}`]
  const r = handler ? handler(body) : { status: 404, body: { detail: 'not found' } }
  return new Response(r.body === undefined ? null : JSON.stringify(r.body), {
    status: r.status,
    headers: { 'Content-Type': 'application/json', ...r.headers },
  })
})

let router: Router
let LoginView: (typeof import('@/views/LoginView.vue'))['default']

beforeAll(async () => {
  router = (await import('@/router')).router
  LoginView = (await import('@/views/LoginView.vue')).default
})

const user = { id: 'u1', email: 'a@example.com', role: 'user', notify_on_expire: false, created_at: '2026-11-20T00:00:00Z' }
const site = { service_name: '折光观察者', registration_mode: 'invite', watch_days: 60, codes_per_day: 2, code_ttl_minutes: 10, max_open_watches: 50 }

function baseRoutes() {
  routes = {
    'GET /api/v1/auth/session': () => ({ status: 200, body: {} }),
    'GET /api/v1/site': () => ({ status: 200, body: site }),
  }
}

async function mountAt(path: string) {
  await router.push(path)
  await router.isReady()
  const w = mount(LoginView, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return w
}

afterEach(() => {
  sessionStorage.clear()
  requests.length = 0
  document.body.innerHTML = ''
})

describe('LoginView', () => {
  it('requests a code, survives a reload, and logs in', async () => {
    baseRoutes()
    routes['POST /api/v1/auth/code'] = () => ({ status: 202, body: { message: '如果该邮箱可以登录，几分钟内会收到验证码，10 分钟内有效。', remaining: 1, resend_after: 60 } })
    routes['POST /api/v1/auth/verify'] = (b) =>
      b.code === '123456' ? { status: 200, body: { user } } : { status: 400, body: { detail: '验证码不正确或已失效。' } }

    let w = await mountAt('/login?next=/settings')
    expect(w.text()).toContain('邀请测试阶段') // invite mode is explained
    await w.find('input[name=email]').setValue(' a@example.com ')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(requests.find((r) => r.path === '/api/v1/auth/code')?.body).toEqual({ email: 'a@example.com' })
    expect(w.find('input[name=code]').exists()).toBe(true)
    expect(w.text()).toContain('重新获取（60 秒）')
    expect(w.text()).toContain('还可以获取 1 次')

    // The mail app reloads the page.
    w.unmount()
    w = await mountAt('/login?next=/settings')
    expect(w.find('input[name=code]').exists()).toBe(true)
    expect(w.text()).toContain('a@example.com')

    const input = w.find('input[name=code]')
    await input.setValue('000000')
    await flushPromises()
    expect(w.text()).toContain('验证码不正确或已失效。')

    await input.setValue('12 34-56') // pasted with separators
    await flushPromises()
    expect(requests.filter((r) => r.path === '/api/v1/auth/verify').at(-1)?.body).toEqual({ email: 'a@example.com', code: '123456' })
    expect(router.currentRoute.value.fullPath).toBe('/settings')
    expect(sessionStorage.getItem('login')).toBeNull()
    w.unmount()
  })

  it('shows the wait when a code was just sent', async () => {
    baseRoutes()
    routes['POST /api/v1/auth/code'] = () => ({
      status: 429,
      body: { detail: '验证码已发送，请 42 秒后再重新获取。' },
      headers: { 'Retry-After': '42' },
    })
    const w = await mountAt('/login')
    await w.find('input[name=email]').setValue('b@example.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('input[name=code]').exists()).toBe(true)
    expect(w.text()).toContain('42 秒')
    w.unmount()
  })

  it('stays on the email step when the day is used up', async () => {
    baseRoutes()
    routes['POST /api/v1/auth/code'] = () => ({ status: 429, body: { detail: '这个邮箱今天已获取过 2 次验证码，请明天再试。' } })
    const w = await mountAt('/login')
    await w.find('input[name=email]').setValue('c@example.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('input[name=code]').exists()).toBe(false)
    expect(w.text()).toContain('请明天再试')
    expect(w.text()).toContain('已经收到验证码？')
    w.unmount()
  })

  it('rejects an obviously wrong email without a request', async () => {
    baseRoutes()
    const w = await mountAt('/login')
    await w.find('input[name=email]').setValue('not-an-email')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('请输入正确的邮箱地址')
    expect(requests.some((r) => r.path === '/api/v1/auth/code')).toBe(false)
    w.unmount()
  })
})
