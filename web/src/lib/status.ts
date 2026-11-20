import type { Check, Watch } from '@/api/client'

export type Tone = 'info' | 'ok' | 'danger' | 'muted' | 'warn'

export interface Badge {
  label: string
  tone: Tone
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

/** watchBadge is the one-word state of a watch, as the list shows it. */
export function watchBadge(w: Watch): Badge {
  switch (w.status) {
    case 'resolving':
      return { label: '备份中', tone: 'info' }
    case 'active':
      return { label: '观察中', tone: 'ok' }
    case 'failed':
      return { label: '提交失败', tone: 'warn' }
  }
  switch (w.end_reason) {
    case 'deleted':
      return { label: '已删除', tone: 'danger' }
    case 'expired':
      return { label: '已到期', tone: 'muted' }
    case 'user_stopped':
      return { label: '已停止', tone: 'muted' }
    case 'invalid':
      return { label: '链接失效', tone: 'warn' }
    case 'unreachable':
      return { label: '无法访问', tone: 'warn' }
    default:
      return { label: '已结束', tone: 'muted' }
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
  alive: '正常',
  deleted: '已删除',
  invalid: '链接无效',
  blocked: '被微信拦截',
  error: '检查出错',
}

export function checkResultText(c: Check): string {
  return checkResults[c.result] ?? c.result
}

/** isOpen is true while the service is still working on a watch. */
export function isOpen(w: Watch): boolean {
  return w.status === 'resolving' || w.status === 'active'
}
