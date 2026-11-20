import { describe, expect, it } from 'vitest'
import { ApiError, call, errorFrom, messageOf } from '@/api/client'

function response(status: number, headers: Record<string, string> = {}) {
  return new Response(null, { status, headers })
}

describe('errorFrom', () => {
  it('prefers the server detail', () => {
    const e = errorFrom(response(422), { title: 'Unprocessable Entity', status: 422, detail: '这不是有效的链接。' })
    expect(e.message).toBe('这不是有效的链接。')
    expect(e.status).toBe(422)
  })
  it('falls back by status', () => {
    expect(errorFrom(response(429), undefined).message).toContain('频繁')
    expect(errorFrom(response(502), 'Bad Gateway').message).toContain('服务器出错')
    expect(errorFrom(response(418), {}).message).toContain('418')
  })
  it('reads Retry-After', () => {
    expect(errorFrom(response(429, { 'Retry-After': '42' }), {}).retryAfter).toBe(42)
    expect(errorFrom(response(429), {}).retryAfter).toBeUndefined()
  })
})

describe('call', () => {
  it('returns data on success', async () => {
    await expect(call(Promise.resolve({ data: { a: 1 }, response: response(200) }))).resolves.toEqual({ a: 1 })
  })
  it('throws ApiError on an error response', async () => {
    const p = call(Promise.resolve({ error: { detail: '请先登录。' }, response: response(401) }))
    await expect(p).rejects.toBeInstanceOf(ApiError)
    await expect(p).rejects.toMatchObject({ status: 401, message: '请先登录。' })
  })
  it('turns network failures into status 0', async () => {
    await expect(call(Promise.reject(new TypeError('Failed to fetch')))).rejects.toMatchObject({ status: 0 })
  })
})

describe('messageOf', () => {
  it('shows ApiError messages and hides everything else', () => {
    expect(messageOf(new ApiError(400, '验证码不正确或已失效。'))).toBe('验证码不正确或已失效。')
    expect(messageOf(new Error('internal detail'))).not.toContain('internal')
  })
})
