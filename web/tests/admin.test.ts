import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import BarChart from '@/components/admin/BarChart.vue'
import { articleStatusText, articleStatusTone, captureStateText, issueDetail, issueLabel, issueTone, shortDay } from '@/lib/admin'
import { formatBytes, formatPercent } from '@/lib/format'

describe('format', () => {
  it.each([
    [0, '0 B'],
    [1023, '1023 B'],
    [1536, '2 KB'], // whole KB; one decimal from MB up
    [5 * 1024 * 1024, '5.0 MB'],
    [150 * 1024 * 1024, '150 MB'],
    [3.25 * 1024 ** 3, '3.3 GB'],
  ])('formatBytes(%d) = %s', (n, want) => {
    expect(formatBytes(n)).toBe(want)
  })
  it('formatPercent', () => {
    expect(formatPercent(1, 0)).toBe('—')
    expect(formatPercent(1, 40)).toBe('2.5%')
    expect(formatPercent(30, 40)).toBe('75%')
  })
})

describe('admin wording', () => {
  it('names statuses and issues', () => {
    expect(articleStatusText('dormant')).toBe('已结束')
    expect(articleStatusTone('invalid')).toBe('unreachable')
    expect(articleStatusTone('new-state')).toBe('ended')
    expect(issueTone('missing_images')).toBe('partial')
    expect(issueTone('failing_checks')).toBe('failed')
    expect(articleStatusText('new-state')).toBe('new-state')
    for (const code of ['backup_failed', 'no_snapshot', 'missing_images', 'short_text', 'failing_checks']) {
      expect(issueLabel(code)).not.toBe(code)
      expect(issueDetail(code)).toMatch(/。$/)
    }
    expect(shortDay('2026-11-20')).toBe('11-20')
    expect(captureStateText('done')).toBe('已生成')
    expect(captureStateText('failed', 'no_content')).toContain('图片或视频消息')
    expect(captureStateText('failed', 'new-code')).toBe('失败：new-code')
  })
})

describe('BarChart', () => {
  const days = Array.from({ length: 30 }, (_, i) => `2026-11-${String(i + 1).padStart(2, '0')}`)
  const values = days.map((_, i) => (i === 29 ? 12 : i % 3))

  it('draws one column per non-zero day, with a summary for screen readers', () => {
    const w = mount(BarChart, { props: { title: '新观察', days, values } })
    const drawn = w.findAll('path.bar').filter((p) => p.attributes('d'))
    expect(drawn).toHaveLength(values.filter((v) => v > 0).length)
    expect(w.findAll('rect')).toHaveLength(30) // hit targets cover every day
    expect(w.text()).toContain('共 ' + values.reduce((a, b) => a + b, 0))
    expect(w.text()).toContain('最高 12')
    expect(w.find('svg').attributes('aria-label')).toContain('新观察')
    // No column is wider than 24 units, and columns never touch.
    // d is M{left},… Q…  …Q{right},… V…: the column spans left..right.
    const widths = drawn.map((p) => {
      const d = p.attributes('d')!
      const left = Number(/^M([\d.]+),/.exec(d)![1])
      const right = Number([...d.matchAll(/Q([\d.]+),/g)].at(-1)![1])
      return right - left
    })
    expect(Math.max(...widths)).toBeLessThanOrEqual(24)
    expect(Math.max(...widths)).toBeLessThanOrEqual(300 / 30 - 2) // a 2-unit gap between columns
  })

  it('shows a day on hover and moves with the arrow keys', async () => {
    const w = mount(BarChart, { props: { title: '检查次数', days, values } })
    await w.findAll('rect')[29]!.trigger('pointerenter')
    expect(w.find('.tip').text()).toContain('12')
    expect(w.find('.tip').text()).toContain('11-30')
    await w.find('svg').trigger('keydown', { key: 'ArrowLeft' })
    expect(w.find('.tip').text()).toContain('11-29')
    await w.find('svg').trigger('keydown', { key: 'Escape' })
    expect(w.find('.tip').exists()).toBe(false)
  })

  it('copes with an empty month', () => {
    const w = mount(BarChart, { props: { title: '发现删除', days, values: days.map(() => 0) } })
    expect(w.findAll('path.bar').filter((p) => p.attributes('d'))).toHaveLength(0)
    expect(w.text()).toContain('共 0')
    expect(w.text()).toContain('最高 0')
  })
})
