import { flushPromises, mount } from '@vue/test-utils'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

// A fake API, stubbed before the app's modules create the client.
const posted: Record<string, unknown>[] = []

vi.stubGlobal('fetch', async (input: RequestInfo, init?: RequestInit) => {
  const req = input instanceof Request ? input : new Request(input, init)
  const path = new URL(req.url).pathname
  if (req.method === 'POST' && path === '/api/v1/watches') {
    posted.push((await req.json()) as Record<string, unknown>)
    return new Response(JSON.stringify({ watch: { id: 'w1', status: 'resolving' } }), {
      status: 202,
      headers: { 'Content-Type': 'application/json' },
    })
  }
  return new Response(JSON.stringify({ detail: 'not found' }), { status: 404, headers: { 'Content-Type': 'application/json' } })
})

let SubmitBox: (typeof import('@/components/SubmitBox.vue'))['default']

beforeAll(async () => {
  SubmitBox = (await import('@/components/SubmitBox.vue')).default
})

beforeEach(() => {
  posted.length = 0
})

const link = 'https://mp.weixin.qq.com/s/tRAmdKYdEgJ7a_X0NNp4Ww'

describe('SubmitBox', () => {
  it('keeps only the link from a pasted share text and says it was recognised', async () => {
    const w = mount(SubmitBox)
    const box = w.get('textarea')
    expect(w.get('#link-hint').text()).toContain('复制链接')

    await box.setValue(`【标题】一篇文章\n${link}。`)
    await flushPromises()
    expect((box.element as HTMLTextAreaElement).value).toBe(link)
    expect(w.get('#link-hint').text()).toContain('已识别文章链接')
    expect(w.get('#link-hint').classes()).toContain('is-ok')
  })

  it('leaves typed text alone', async () => {
    const w = mount(SubmitBox)
    const box = w.get('textarea')
    const typed = `x ${link}.`
    for (let i = 1; i <= typed.length; i++) await box.setValue(typed.slice(0, i))
    expect((box.element as HTMLTextAreaElement).value).toBe(`x ${link}.`)
  })

  it('keeps a pasted text without a link and reports it on submit', async () => {
    const w = mount(SubmitBox)
    const box = w.get('textarea')
    await box.setValue('https://example.com/a')
    expect((box.element as HTMLTextAreaElement).value).toBe('https://example.com/a')
    expect(w.get('#link-hint').text()).toContain('复制链接')

    await w.get('form').trigger('submit')
    expect(w.get('#link-hint').text()).toContain('这不是公众号文章链接')
    // Editing the text clears the error.
    await box.setValue('')
    expect(w.get('#link-hint').classes()).not.toContain('is-error')
  })

  it('shows the copy-link screenshot only on request', async () => {
    const w = mount(SubmitBox)
    expect(w.find('#copy-link-help').exists()).toBe(false)
    const toggle = w.get('.help-toggle')
    expect(toggle.attributes('aria-expanded')).toBe('false')

    await toggle.trigger('click')
    expect(w.get('#copy-link-help img').attributes('src')).toContain('copy-link')
    expect(toggle.attributes('aria-expanded')).toBe('true')

    // Once a link is recognised the help goes away.
    await w.get('textarea').setValue(link)
    expect(w.find('.help-toggle').exists()).toBe(false)
    expect(w.find('#copy-link-help').exists()).toBe(false)
  })

  it('submits the link and clears the box', async () => {
    const w = mount(SubmitBox)
    const box = w.get('textarea')
    await box.setValue(link)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(posted).toEqual([{ url: link }])
    expect(w.emitted('submitted')).toHaveLength(1)
    expect((box.element as HTMLTextAreaElement).value).toBe('')
    expect(w.get('#link-hint').text()).toContain('复制链接')
  })
})
