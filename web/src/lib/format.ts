const two = (n: number) => String(n).padStart(2, '0')

function parse(v: string | Date | undefined | null): Date | null {
  if (!v) return null
  const d = v instanceof Date ? v : new Date(v)
  return Number.isNaN(d.getTime()) ? null : d
}

/** 2026-11-20 (local time) */
export function formatDate(v: string | Date | undefined | null): string {
  const d = parse(v)
  return d ? `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}` : ''
}

/** 2026-11-20 14:05 (local time, 24-hour clock) */
export function formatDateTime(v: string | Date | undefined | null): string {
  const d = parse(v)
  return d ? `${formatDate(d)} ${two(d.getHours())}:${two(d.getMinutes())}` : ''
}

/** Local time today, month/day and time this year, otherwise an ISO date. */
export function formatShort(v: string | Date | undefined | null, now = new Date()): string {
  const d = parse(v)
  if (!d) return ''
  const date = `${two(d.getMonth() + 1)}-${two(d.getDate())}`
  if (d.getFullYear() !== now.getFullYear()) return `${d.getFullYear()}-${date}`
  const time = `${two(d.getHours())}:${two(d.getMinutes())}`
  return d.toDateString() === now.toDateString() ? time : `${date} ${time}`
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

const numberFmt = new Intl.NumberFormat('zh-CN')

/** 12,345 */
export function formatNumber(n: number): string {
  return numberFmt.format(n)
}

/** 1.5 MB (binary units, one decimal from MB up) */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

/** 12.5% of total, or "—" when there is no total */
export function formatPercent(part: number, total: number): string {
  if (!total) return '—'
  const p = (part / total) * 100
  return `${p < 10 ? p.toFixed(1) : Math.round(p)}%`
}
