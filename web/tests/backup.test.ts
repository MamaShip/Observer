import { flushPromises, mount } from '@vue/test-utils'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'

// A fake API, stubbed before the app's modules create the client.
let detail: Record<string, unknown> = {}
vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  const bodies: Record<string, unknown> = {
    '/api/v1/auth/session': { user: { id: 'u1', email: 'a@example.com', role: 'user', created_at: '2026-11-20T00:00:00Z' } },
    '/api/v1/site': { service_name: '折光观察者', registration_mode: 'invite', watch_days: 60, codes_per_day: 2, code_ttl_minutes: 10, max_open_watches: 50, monthly_emails: 20, version: 'test' },
    '/api/v1/watches/w1': detail,
  }
  const body = bodies[path]
  return new Response(JSON.stringify(body ?? { detail: 'not found' }), {
    status: body ? 200 : 404,
    headers: { 'Content-Type': 'application/json' },
  })
})

let router: Router
let WatchDetailView: (typeof import('@/views/WatchDetailView.vue'))['default']
beforeAll(async () => {
  router = (await import('@/router')).router
  WatchDetailView = (await import('@/views/WatchDetailView.vue')).default
})

function watchDetail(backup: Record<string, unknown>) {
  return {
    watch: {
      id: 'w1', status: 'active', submitted_url: 'https://mp.weixin.qq.com/s/p', created_at: '2026-11-20T00:00:00Z',
      expires_at: '2027-01-19T00:00:00Z', can_renew: true,
      article: { id: 'a1', status: 'alive', title: '图片消息', url: 'https://mp.weixin.qq.com/s/p' },
    },
    snapshots: [],
    checks: [],
    ...backup,
  }
}

async function mountWith(backup: Record<string, unknown>) {
  detail = watchDetail(backup)
  await router.push('/watches/w1')
  await router.isReady()
  const w = mount(WatchDetailView, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

describe('backup state on the watch page', () => {
  it('shows progress while the backup is being made', async () => {
    const w = await mountWith({ backup_status: 'pending' })
    expect(w.text()).toContain('正在备份')
    w.unmount()
  })

  it('explains a post that cannot be backed up, instead of backing up for ever', async () => {
    const w = await mountWith({ backup_status: 'failed', backup_error: 'no_content' })
    expect(w.text()).not.toContain('正在备份')
    expect(w.text()).toContain('图片或视频形式的消息')
    expect(w.text()).toContain('仍会照常检查')
    w.unmount()
  })

  it('reports other failures', async () => {
    const w = await mountWith({ backup_status: 'failed', backup_error: 'error' })
    expect(w.text()).not.toContain('正在备份')
    expect(w.text()).toContain('多次出错')
    w.unmount()
  })
})
