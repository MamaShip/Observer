// Wording for the admin console.

const articleStatuses: Record<string, string> = {
  pending: '解析中',
  alive: '在线',
  deleted: '已删除',
  invalid: '无效',
  dormant: '休眠',
}

/** articleStatusText names an article's state (dormant: nobody watches it any more). */
export function articleStatusText(s: string): string {
  return articleStatuses[s] ?? s
}

const issues: Record<string, { label: string; detail: string }> = {
  backup_failed: { label: '备份失败', detail: '最近一次抓取的页面没能生成备份；可以在文章详情里查看原因，修复后从已保存的原始页面重建。' },
  no_snapshot: { label: '缺少备份', detail: '文章抓取成功，但没有生成备份。' },
  missing_images: { label: '图片缺失', detail: '最新备份中有图片没能保存。' },
  short_text: { label: '正文过短', detail: '最新备份的正文不到 100 字，可能没抓到正文（纯图片文章也会这样）。' },
  failing_checks: { label: '检查连续失败', detail: '最近连续 3 次以上检查没有得到结论（被拦截或出错）。' },
}

export function issueLabel(code: string): string {
  return issues[code]?.label ?? code
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
