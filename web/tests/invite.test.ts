import { flushPromises, mount } from '@vue/test-utils'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Router } from 'vue-router'

// A fake API, stubbed before the app's modules create the client.
type Reply = { status: number; body?: unknown }
let routes: Record<string, (body: Record<string, unknown>) => Reply> = {}

vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  const body = req.method === 'GET' ? {} : ((await req.json().catch(() => ({}))) as Record<string, unknown>)
  const handler = routes[`${req.method} ${path}`]
  const r = handler ? handler(body) : { status: 404, body: { detail: 'not found' } }
  return new Response(r.body === undefined ? null : JSON.stringify(r.body), {
    status: r.status,
    headers: { 'Content-Type': 'application/json' },
  })
})

let router: Router
let SettingsView: (typeof import('@/views/SettingsView.vue'))['default']
let session: (typeof import('@/session'))['session']

beforeAll(async () => {
  router = (await import('@/router')).router
  SettingsView = (await import('@/views/SettingsView.vue')).default
  session = (await import('@/session')).session
})

const user = { id: 'u1', email: 'a@example.com', role: 'user', created_at: '2026-11-20T00:00:00Z' }

function invitations(over: Record<string, unknown> = {}) {
  return { items: [], can_invite: true, used_this_month: 4, monthly_limit: 5, ...over }
}

async function mountSettings() {
  session.user = user as never
  await router.push('/settings')
  await router.isReady()
  const w = mount(SettingsView, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

beforeEach(() => {
  routes = {
    'GET /api/v1/me': () => ({ status: 200, body: { ...user, emails_this_month: 0, monthly_emails: 20 } }),
    'GET /api/v1/invites': () => ({ status: 200, body: invitations() }),
  }
})

describe('inviting from the settings page', () => {
  it('shows the remaining quota and confirms the email invitation', async () => {
    let sent: Record<string, unknown> = {}
    routes['POST /api/v1/invites'] = (b) => {
      sent = b
      return {
        status: 201,
        body: { invitation: { id: 'i1', email: 'bob@example.com', created_at: '2026-11-20T01:00:00Z', status: 'pending' }, used_this_month: 5, monthly_limit: 5 },
      }
    }
    const w = await mountSettings()
    expect(w.text()).toContain('本月还可以邀请 1 人')
    await w.find('.invite-form input').setValue(' Bob@Example.com ')
    await w.find('.invite-form').trigger('submit')
    await flushPromises()
    expect(sent).toEqual({ email: 'Bob@Example.com' })
    expect(w.find('[role=status]').text()).toContain('已邀请 bob@example.com')
    expect(w.find('[role=status]').text()).toContain('从邮件中接受邀请')
    expect(w.text()).toContain('本月还可以邀请 0 人')
    expect(w.find('.invite-form button').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('剩余 0 / 5 个名额')
    expect(w.find('.invite-email').text()).toContain('bob@example.com')
  })

  it('withdraws a pending invitation, but not an accepted one', async () => {
    let list = [
      { id: 'i1', email: 'bob@example.com', created_at: '2026-11-20T01:00:00Z', status: 'pending' },
      { id: 'i2', email: 'carol@example.com', created_at: '2026-11-19T01:00:00Z', status: 'accepted' },
    ]
    routes['GET /api/v1/invites'] = () => ({ status: 200, body: invitations({ items: list }) })
    let revoked = ''
    routes['DELETE /api/v1/invites/i1'] = () => {
      revoked = 'i1'
      list = list.slice(1)
      return { status: 204 }
    }
    const w = await mountSettings()
    const rows = w.findAll('.zg-setrow').filter(r => r.find('.invite-email').exists())
    expect(rows.map(r => r.text())).toEqual([expect.stringContaining('待接受'), expect.stringContaining('已注册')])
    expect(rows[1]!.find('button').exists()).toBe(false)
    await rows[0]!.find('button').trigger('click')
    await flushPromises()
    expect(revoked).toBe('i1')
    expect(w.text()).not.toContain('bob@example.com')
  })

  it("shows the server's refusal", async () => {
    routes['POST /api/v1/invites'] = () => ({ status: 429, body: { detail: '本月的 5 个邀请名额已用完，下月 1 日（北京时间）恢复。' } })
    const w = await mountSettings()
    await w.find('.invite-form input').setValue('c@example.com')
    await w.find('.invite-form').trigger('submit')
    await flushPromises()
    expect(w.find('[role=alert]').text()).toContain('名额已用完')
  })

  it('is hidden when nobody needs inviting', async () => {
    routes['GET /api/v1/invites'] = () => ({ status: 200, body: invitations({ can_invite: false, monthly_limit: 0 }) })
    const w = await mountSettings()
    expect(w.text()).not.toContain('邀请朋友')
  })
})

describe('opening the invitation email', () => {
  const token = 't'.repeat(43)
  async function openLink() {
    await router.push('/invite#t=' + token)
    const InviteView = (await import('@/views/InviteView.vue')).default
    const w = mount(InviteView, { global: { plugins: [router] } })
    await flushPromises()
    return w
  }

  it('signs a visitor in after a click and opens their watches', async () => {
    session.user = null
    const accept = vi.fn(() => ({ status: 200, body: { user } }))
    routes['POST /api/v1/auth/accept-invite'] = accept
    const w = await openLink()
    // Opening the link alone (as a mail scanner might) spends nothing.
    expect(accept).not.toHaveBeenCalled()
    expect(router.currentRoute.value.fullPath).toBe('/invite')
    expect(w.find('a').exists()).toBe(false)
    await w.get('button').trigger('click')
    await flushPromises()
    expect(accept).toHaveBeenCalledExactlyOnceWith({ token })
    expect(router.currentRoute.value.fullPath).toBe('/watches')
    expect(session.user).toMatchObject({ email: user.email })
    w.unmount()
  })

  it('protects the current session and offers email login for a spent link', async () => {
    session.user = user as never
    const accept = vi.fn(() => ({ status: 410, body: { detail: '邀请链接已失效。' } }))
    routes['POST /api/v1/auth/accept-invite'] = accept
    const w = await openLink()
    expect(accept).not.toHaveBeenCalled()
    expect(router.currentRoute.value.fullPath).toBe('/invite')
    await w.get('button').trigger('click')
    await flushPromises()
    expect(w.get('[role=alert]').text()).toContain('已失效')
    expect(w.get('a').attributes('href')).toBe('/login')
    w.unmount()
  })
})
