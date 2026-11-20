const dateFmt = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
const dateTimeFmt = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: 'long',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})
const shortFmt = new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric' })

function parse(v: string | Date | undefined | null): Date | null {
  if (!v) return null
  const d = v instanceof Date ? v : new Date(v)
  return Number.isNaN(d.getTime()) ? null : d
}

/** 2026年11月20日 */
export function formatDate(v: string | Date | undefined | null): string {
  const d = parse(v)
  return d ? dateFmt.format(d) : ''
}

/** 2026年11月20日 14:05 */
export function formatDateTime(v: string | Date | undefined | null): string {
  const d = parse(v)
  return d ? dateTimeFmt.format(d) : ''
}

/** 11/20, this year's dates without the year */
export function formatShort(v: string | Date | undefined | null, now = new Date()): string {
  const d = parse(v)
  if (!d) return ''
  return d.getFullYear() === now.getFullYear() ? shortFmt.format(d) : dateFmt.format(d)
}

/** "3 分钟前", "2 小时后" … for times within a few days, else the date. */
export function formatRelative(v: string | Date | undefined | null, now = new Date()): string {
  const d = parse(v)
  if (!d) return ''
  const diff = d.getTime() - now.getTime()
  const abs = Math.abs(diff)
  const suffix = diff < 0 ? '前' : '后'
  if (abs < 60_000) return diff < 0 ? '刚刚' : '即将'
  if (abs < 3_600_000) return `${Math.round(abs / 60_000)} 分钟${suffix}`
  if (abs < 86_400_000) return `${Math.round(abs / 3_600_000)} 小时${suffix}`
  if (abs < 7 * 86_400_000) return `${Math.round(abs / 86_400_000)} 天${suffix}`
  return formatDate(d)
}
