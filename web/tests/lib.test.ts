import { describe, expect, it } from 'vitest'
import type { Watch } from '@/api/client'
import { formatRelative, formatShort } from '@/lib/format'
import { extractLink } from '@/lib/link'
import { daysLeft, deletedReasonText, isOpen, renewedUntil, watchBadge, watchSummary } from '@/lib/status'
import { sourceURL } from '@/lib/version'
import { safeNext } from '@/router'

describe('extractLink', () => {
  it.each([
    ['https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A', 'https://mp.weixin.qq.com/s/q-WkvTApjcwqtgk9LY7Q-A'],
    ['  https://mp.weixin.qq.com/s/abc  ', 'https://mp.weixin.qq.com/s/abc'],
    ['分享一篇文章 https://mp.weixin.qq.com/s/abc 来自公众号', 'https://mp.weixin.qq.com/s/abc'],
    ['【标题】https://mp.weixin.qq.com/s/abc，快看', 'https://mp.weixin.qq.com/s/abc'],
    ['(https://mp.weixin.qq.com/s/abc).', 'https://mp.weixin.qq.com/s/abc'],
    [
      'https://mp.weixin.qq.com/s?__biz=MzA5&mid=2650&idx=1&sn=abc&chksm=8b#rd',
      'https://mp.weixin.qq.com/s?__biz=MzA5&mid=2650&idx=1&sn=abc&chksm=8b#rd',
    ],
    ['HTTP://MP.WEIXIN.QQ.COM/s/ABC', 'HTTP://MP.WEIXIN.QQ.COM/s/ABC'],
  ])('finds the link in %j', (text, want) => {
    expect(extractLink(text)).toBe(want)
  })

  it.each(['', 'hello', 'https://example.com/s/abc', 'https://mp.weixin.qq.com/', 'mp.weixin.qq.com/s/abc'])(
    'finds nothing in %j',
    (text) => {
      expect(extractLink(text)).toBeNull()
    },
  )
})

const base: Watch = {
  id: 'w1',
  status: 'active',
  submitted_url: 'https://mp.weixin.qq.com/s/abc',
  created_at: '2026-11-20T02:00:00Z',
  expires_at: '2027-01-19T02:00:00Z',
  can_renew: true,
}

describe('watch period', () => {
  const now = Date.parse('2027-01-12T03:00:00Z')
  it('counts the days left, rounded up and never below 0', () => {
    expect(daysLeft(base, now)).toBe(7) // 6 days 23 hours
    expect(daysLeft({ expires_at: '2027-01-12T02:00:00Z' }, now)).toBe(0)
    expect(daysLeft({ expires_at: '2027-01-12T03:00:01Z' }, now)).toBe(1)
  })
  it('says when a renewal would end', () => {
    expect(renewedUntil(60, now).toISOString()).toBe('2027-03-13T03:00:00.000Z')
  })
})

describe('watch status', () => {
  it.each<[Partial<Watch>, string, string]>([
    [{ status: 'resolving' }, '备份中', 'info'],
    [{ status: 'active' }, '观察中', 'ok'],
    [{ status: 'failed', end_reason: 'invalid' }, '提交失败', 'warn'],
    [{ status: 'ended', end_reason: 'deleted' }, '已删除', 'danger'],
    [{ status: 'ended', end_reason: 'expired' }, '已到期', 'muted'],
    [{ status: 'ended', end_reason: 'user_stopped' }, '已停止', 'muted'],
    [{ status: 'ended', end_reason: 'something-new' }, '已结束', 'muted'],
  ])('%j → %s', (patch, label, tone) => {
    expect(watchBadge({ ...base, ...patch })).toEqual({ label, tone })
  })

  it('explains every state in a sentence', () => {
    for (const patch of [
      { status: 'resolving' },
      { status: 'active' },
      { status: 'failed', end_reason: 'invalid' },
      { status: 'failed', end_reason: 'unreachable' },
      { status: 'ended', end_reason: 'deleted' },
      { status: 'ended', end_reason: 'expired' },
      { status: 'ended', end_reason: 'user_stopped' },
    ] as Partial<Watch>[]) {
      expect(watchSummary({ ...base, ...patch })).toMatch(/。$/)
    }
    expect(watchSummary({ ...base, status: 'failed', end_reason: 'unreachable' })).toContain('多次尝试')
  })

  it('names deletion reasons', () => {
    expect(deletedReasonText('complaint')).toBe('被投诉删除')
    expect(deletedReasonText(undefined)).toBe('原因不明')
    expect(deletedReasonText('new-code')).toBe('原因不明')
  })

  it('knows which watches are still open', () => {
    expect(isOpen({ ...base, status: 'resolving' })).toBe(true)
    expect(isOpen({ ...base, status: 'ended' })).toBe(false)
  })
})

describe('format', () => {
  const now = new Date('2026-11-20T12:00:00+08:00')
  it('relative times', () => {
    expect(formatRelative('2026-11-20T11:59:30+08:00', now)).toBe('刚刚')
    expect(formatRelative('2026-11-20T11:55:00+08:00', now)).toBe('5 分钟前')
    expect(formatRelative('2026-11-20T15:00:00+08:00', now)).toBe('3 小时后')
    expect(formatRelative('2026-11-18T12:00:00+08:00', now)).toBe('2 天前')
    expect(formatRelative('2026-10-01T12:00:00+08:00', now)).toContain('2026')
    expect(formatRelative(undefined, now)).toBe('')
    expect(formatRelative('not a date', now)).toBe('')
  })
  it('short dates drop this year', () => {
    expect(formatShort('2026-11-21T12:00:00+08:00', now)).not.toContain('2026')
    expect(formatShort('2027-01-19T12:00:00+08:00', now)).toContain('2027')
  })
})

describe('safeNext', () => {
  it.each([
    ['/settings', '/settings'],
    ['/watches/abc?x=1', '/watches/abc?x=1'],
    ['//evil.example/x', '/watches'],
    ['/\\evil.example', '/watches'],
    ['https://evil.example', '/watches'],
    [undefined, '/watches'],
    [['/a'], '/watches'],
  ])('%j → %s', (input, want) => {
    expect(safeNext(input)).toBe(want)
  })
})

describe('version', () => {
  it.each([
    ['v0.3.0', 'https://github.com/MamaShip/Observer/tree/v0.3.0'],
    ['v1.0.0-rc.1', 'https://github.com/MamaShip/Observer/tree/v1.0.0-rc.1'],
    ['v0.3.0-2-g1ec2416', 'https://github.com/MamaShip/Observer/commit/1ec2416'],
    ['v0.3.0-2-g1ec2416-dirty', null],
    ['1ec2416', null],
    ['dev', null],
    [undefined, null],
  ])('%s → %s', (v, want) => {
    expect(sourceURL(v)).toBe(want)
  })
})
