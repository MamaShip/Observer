// Wording for the admin console.

// In the design system's words: an article being checked is 观察中, one
// nobody watches any more 已结束, a link that never opened 无法访问.
const articleStatuses: Record<string, { label: string; tone: string }> = {
  pending: { label: '备份中', tone: 'archiving' },
  alive: { label: '观察中', tone: 'watching' },
  deleted: { label: '已删除', tone: 'deleted' },
  invalid: { label: '无法访问', tone: 'unreachable' },
  dormant: { label: '已结束', tone: 'ended' },
}

/** articleStatusText names an article's state. */
export function articleStatusText(s: string): string {
  return articleStatuses[s]?.label ?? s
}

/** articleStatusTone is the zg-tag modifier for an article's state. */
export function articleStatusTone(s: string): string {
  return articleStatuses[s]?.tone ?? 'ended'
}

// failed: no usable backup or no verdict; partial: a backup with gaps.
const issues: Record<string, { label: string; detail: string; tone: 'failed' | 'partial' }> = {
  backup_failed: { label: '备份失败', tone: 'failed', detail: '最近一次抓取的页面没能生成备份；可以在文章详情里查看原因，修复后从已保存的原始页面重建。' },
  no_snapshot: { label: '缺少备份', tone: 'failed', detail: '文章抓取成功，但没有生成备份。' },
  missing_images: { label: '图片缺失', tone: 'partial', detail: '最新备份中有图片没能保存。' },
  short_text: { label: '正文过短', tone: 'partial', detail: '最新备份的正文不到 100 字，可能没抓到正文（纯图片文章也会这样）。' },
  failing_checks: { label: '检查异常', tone: 'failed', detail: '最近连续 3 次以上检查没有得到结论（被拦截或出错）。' },
}

export function issueLabel(code: string): string {
  return issues[code]?.label ?? code
}

/** issueTone is the zg-tag modifier for a problem. */
export function issueTone(code: string): 'failed' | 'partial' {
  return issues[code]?.tone ?? 'partial'
}

export function issueDetail(code: string): string {
  return issues[code]?.detail ?? ''
}

const captureStates: Record<string, string> = { pending: '生成中', done: '已生成', failed: '失败' }
const captureErrors: Record<string, string> = {
  no_content: '页面没有正文区域（图片或视频消息），暂不支持',
  error: '多次重试仍然出错',
}

/** captureStateText names what became of a fetched page. */
export function captureStateText(status: string, errorCode?: string): string {
  const s = captureStates[status] ?? status
  return errorCode ? `${s}：${captureErrors[errorCode] ?? errorCode}` : s
}

/** shortDay turns 2026-11-20 into 11-20. */
export function shortDay(day: string): string {
  return day.slice(5)
}
