import type { Check, Watch } from '@/api/client'

/** Tag is a zg-tag modifier: the design system's status words. */
export type Tag = 'watching' | 'expiring' | 'ended' | 'deleted' | 'unreachable' | 'archiving'

export interface Badge {
  label: string
  tag: Tag
}

const deletedReasons: Record<string, string> = {
  author_deleted: '作者删除',
  violation: '违规被删',
  complaint: '被投诉删除',
  account_blocked: '账号被封',
  unknown: '原因不明',
}

/** deletedReasonText names why WeChat removed an article. */
export function deletedReasonText(code: string | undefined): string {
  return (code && deletedReasons[code]) || deletedReasons.unknown!
}

/**
 * watchBadge is the state of a watch in the design system's words only:
 * 观察中, 即将到期, 已结束, 已删除, 无法访问, and 备份中 while the first
 * backup is made. How a watch ended is told in words elsewhere.
 */
export function watchBadge(w: Watch, now = Date.now()): Badge {
  switch (w.status) {
    case 'resolving':
      return { label: '备份中', tag: 'archiving' }
    case 'active':
      return daysLeft(w, now) <= endingSoonDays ? { label: '即将到期', tag: 'expiring' } : { label: '观察中', tag: 'watching' }
    case 'failed':
      return { label: '无法访问', tag: 'unreachable' }
  }
  switch (w.end_reason) {
    case 'deleted':
      return { label: '已删除', tag: 'deleted' }
    case 'invalid':
    case 'unreachable':
      return { label: '无法访问', tag: 'unreachable' }
    default:
      return { label: '已结束', tag: 'ended' }
  }
}

/** endedText says how and when a finished watch ended, for its card. */
export function endedText(w: Watch): string {
  if (w.status === 'failed') return '没能开始观察'
  switch (w.end_reason) {
    case 'deleted': return '确认删除'
    case 'expired': return '到期于'
    case 'user_stopped': return '停止于'
    default: return '结束于'
  }
}

const day = 24 * 60 * 60 * 1000

/** daysLeft is how many days an open watch still runs, rounded up. */
export function daysLeft(w: Pick<Watch, 'expires_at'>, now = Date.now()): number {
  return Math.max(0, Math.ceil((new Date(w.expires_at).getTime() - now) / day))
}

/** Within this many days of its end a watch is shown as ending soon. */
export const endingSoonDays = 7

/** renewedUntil is when a watch renewed now would end. */
export function renewedUntil(watchDays: number, now = Date.now()): Date {
  return new Date(now + watchDays * day)
}

/** watchSummary explains a watch's state in a sentence. */
export function watchSummary(w: Watch): string {
  switch (w.status) {
    case 'resolving':
      return '正在获取文章并备份，通常一两分钟内完成。'
    case 'active':
      return '文章目前可以访问，服务会定期检查。'
    case 'failed':
      return w.end_reason === 'unreachable'
        ? '多次尝试都无法打开这篇文章，没能备份。请确认链接在微信里能正常打开后重新提交。'
        : '这个链接打不开或已过期，没能备份。请在微信里打开文章，点右上角「···」→「复制链接」后重新提交。'
  }
  switch (w.end_reason) {
    case 'deleted':
      return '文章已被删除。下面是删除前的备份。'
    case 'expired':
      return '观察期已结束，文章在此期间没有被删除。备份会继续保留。'
    case 'user_stopped':
      return '你已停止观察这篇文章。备份会继续保留。'
    case 'invalid':
    case 'unreachable':
      return '文章链接后来无法访问，观察已结束。'
    default:
      return '观察已结束。'
  }
}

const checkResults: Record<Check['result'], string> = {
  alive: '在线',
  deleted: '确认已删除',
  invalid: '链接无效',
  blocked: '无法访问，稍后复查',
  error: '检查出错，稍后复查',
}

export function checkResultText(c: Check): string {
  return checkResults[c.result] ?? c.result
}

/** isOpen is true while the service is still working on a watch. */
export function isOpen(w: Watch): boolean {
  return w.status === 'resolving' || w.status === 'active'
}
