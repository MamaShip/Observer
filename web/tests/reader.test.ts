import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'

it('keeps the selected version when an older signed link arrives late', async () => {
  let resolveOld!: (response: Response) => void
  const json = (data: unknown) => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
  const signed = (id: string) => json({ url: 'https://snapshot.example/' + id, expires_at: new Date(Date.now() + 3600000).toISOString() })
  const requests: string[] = []
  vi.stubGlobal('fetch', async (req: Request) => {
    const path = new URL(req.url).pathname
    requests.push(path)
    if (path.endsWith('/auth/session')) return json({ user: { id: 'u1', role: 'user' } })
    if (path.endsWith('/watches/w1')) return json({
      watch: { article: { title: '测试文章' } },
      snapshots: ['s2', 's1'].map(id => ({ id, captured_at: new Date().toISOString() })),
    })
    if (path.endsWith('/s2/view-url')) return new Promise<Response>(resolve => { resolveOld = resolve })
    if (path.endsWith('/s1/view-url')) return signed('s1')
    return json({})
  })
  const { router } = await import('@/router')
  const Reader = (await import('@/views/ReaderView.vue')).default
  await router.push('/watches/w1/backup/s2')
  const wrapper = mount(Reader, { global: { plugins: [router] } })
  try {
    await flushPromises()
    await wrapper.find('select').setValue('s1')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/watches/w1/backup/s1')
    resolveOld(signed('s2'))
    await flushPromises()
    expect(wrapper.get('iframe').attributes('src')).toBe('https://snapshot.example/s1')
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-same-origin allow-popups allow-popups-to-escape-sandbox')

    await router.push('/watches/w1/backup/missing')
    await flushPromises()
    expect(wrapper.text()).toContain('没有找到这个备份版本')
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(requests.some(path => path.includes('/missing/view-url'))).toBe(false)
  } finally {
    wrapper.unmount()
    vi.unstubAllGlobals()
  }
})
