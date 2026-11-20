import { flushPromises, mount } from '@vue/test-utils'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'

// A fake API, stubbed before the app's modules create the client.
type Reply = { status: number; body?: unknown }
let routes: Record<string, () => Reply> = {}
let posted: string[] = []

vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  if (req.method === 'POST') posted.push(path)
  const handler = routes[`${req.method} ${path}`]
  const r = handler ? handler() : { status: 404, body: { detail: 'not found' } }
  return new Response(r.body === undefined ? null : JSON.stringify(r.body), {
    status: r.status,
    headers: { 'Content-Type': 'application/json' },
  })
})

let router: Router
let WatchDetailView: (typeof import('@/views/WatchDetailView.vue'))['default']

beforeAll(async () => {
  router = (await import('@/router')).router
  WatchDetailView = (await import('@/views/WatchDetailView.vue')).default
})

const user = { id: 'u1', email: 'a@example.com', role: 'user', created_at: '2026-11-20T00:00:00Z' }
const site = { service_name: '折光观察者', registration_mode: 'invite', watch_days: 60, codes_per_day: 2, code_ttl_minutes: 10, max_open_watches: 50, monthly_emails: 20 }
const always = {
  'GET /api/v1/auth/session': () => ({ status: 200, body: { user } }),
  'GET /api/v1/site': () => ({ status: 200, body: site }),
}

const day = 24 * 60 * 60 * 1000
function detail(watch: Record<string, unknown>) {
  return {
    watch: {
      id: 'w1',
      status: 'active',
      submitted_url: 'https://mp.weixin.qq.com/s/abc',
      created_at: new Date(Date.now() - 55 * day).toISOString(),
      expires_at: new Date(Date.now() + 5 * day - 60_000).toISOString(),
      can_renew: true,
      article: { id: 'a1', status: 'alive', title: '标题', url: 'https://mp.weixin.qq.com/s/abc' },
      ...watch,
    },
    snapshots: [],
    checks: [],
  }
}

async function mountDetail() {
  await router.push('/watches/w1')
  await router.isReady()
  const w = mount(WatchDetailView, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

beforeEach(() => {
  posted = []
})

describe('renewing a watch', () => {
  it('extends an active watch near its end', async () => {
    routes = {
      ...always,
      'GET /api/v1/watches/w1': () => ({ status: 200, body: detail({}) }),
      'POST /api/v1/watches/w1/renew': () => ({
        status: 200,
        body: detail({ expires_at: new Date(Date.now() + 60 * day).toISOString() }),
      }),
    }
    const w = await mountDetail()
    expect(w.text()).toContain('还剩 5 天')
    const button = w.findAll('button').find((b) => b.text() === '延长观察')!
    await button.trigger('click')
    await flushPromises()
    expect(posted).toEqual(['/api/v1/watches/w1/renew'])
    expect(w.find('[role=status]').text()).toContain('已延长观察期')
    expect(w.text()).toContain('还剩 60 天')
    expect(w.findAll('button').some((b) => b.text() === '延长观察')).toBe(false) // nothing more to add today
  })

  it('watches an expired article again, and shows a refusal', async () => {
    routes = {
      ...always,
      'GET /api/v1/watches/w1': () => ({
        status: 200,
        body: detail({ status: 'ended', end_reason: 'expired', ended_at: new Date().toISOString() }),
      }),
      'POST /api/v1/watches/w1/renew': () => ({ status: 429, body: { detail: '同时观察的文章已达上限（50 篇），请先停止一些观察。' } }),
    }
    const w = await mountDetail()
    const button = w.findAll('button').find((b) => b.text() === '重新观察')!
    expect(button).toBeDefined()
    await button.trigger('click')
    await flushPromises()
    expect(w.find('[role=alert]').text()).toContain('已达上限')
  })

  it('offers nothing when the watch cannot be renewed', async () => {
    routes = {
      ...always,
      'GET /api/v1/watches/w1': () => ({
        status: 200,
        body: detail({ status: 'ended', end_reason: 'deleted', can_renew: false }),
      }),
    }
    const w = await mountDetail()
    expect(w.findAll('button').some((b) => ['重新观察', '延长观察'].includes(b.text()))).toBe(false)
  })
})
