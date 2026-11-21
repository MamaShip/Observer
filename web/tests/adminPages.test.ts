import { expect, it } from 'vitest'
import { useAdminPages } from '@/lib/adminPages'

it('paginates matching rows across API cursors, including pages with no matches', async () => {
  const values = Array.from({ length: 52 }, (_, i) => i)
  const pages = useAdminPages<number>(async cursor => {
    const start = Number(cursor ?? 0)
    const next = start + 5
    return { items: values.slice(start, next), next_cursor: next < values.length ? String(next) : undefined }
  }, n => n >= 10 && n % 2 === 0)
  await pages.reset()
  expect(pages.items.value).toHaveLength(20)
  expect(pages.items.value[0]).toBe(10)
  expect(pages.hasNext.value).toBe(true)
  await pages.go(1)
  expect(pages.items.value).toEqual([50])
  expect(pages.matchCount.value).toBe(21)
  expect(pages.hasNext.value).toBe(false)
  await pages.go(0)
  expect(pages.items.value).toHaveLength(20)
})

it('takes the counts the API reports with the first page', async () => {
  const pages = useAdminPages<number>(async cursor => cursor
    ? { items: [3], matched: 99 }
    : { items: [1, 2], next_cursor: 'b', matched: 3, total: 10 }, () => true)
  await pages.reset()
  expect(pages.matchCount.value).toBe(3)
  expect(pages.total.value).toBe(10)
})
