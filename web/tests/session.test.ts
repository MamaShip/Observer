import { afterEach, describe, expect, it, vi } from 'vitest'

// ensureSession must not call /me (a 401 for visitors) but /auth/session.
describe('ensureSession', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('asks /auth/session and treats a visitor as logged out without errors', async () => {
    const paths: string[] = []
    vi.stubGlobal('fetch', async (input: RequestInfo) => {
      const req = input instanceof Request ? input : new Request(input)
      const path = new URL(req.url).pathname
      paths.push(path)
      const body = path === '/api/v1/site' ? { service_name: 'X', registration_mode: 'open' } : {}
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    const { ensureSession, session } = await import('@/session')
    await ensureSession()
    expect(paths.sort()).toEqual(['/api/v1/auth/session', '/api/v1/site'])
    expect(session.user).toBeNull()
    expect(session.site?.service_name).toBe('X')
  })
})
