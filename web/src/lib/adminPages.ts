import { computed, ref, shallowRef } from 'vue'
import { messageOf } from '@/api/client'

/** Deliver complete result pages even when a display filter is finer than the API filter. */
export function useAdminPages<T>(
  fetchPage: (cursor?: string) => Promise<{ items: T[]; next_cursor?: string; matched?: number; total?: number }>,
  accepts: (item: T) => boolean,
) {
  const size = 20
  const rows = shallowRef<T[]>([])
  const page = ref(0)
  const loading = ref(false)
  const error = ref('')
  const more = ref(true)
  // Counts the API reports with the first page, when it does.
  const matched = ref<number>()
  const total = ref<number>()
  let cursor: string | undefined
  let generation = 0
  const items = computed(() => rows.value.slice(page.value * size, (page.value + 1) * size))
  const hasNext = computed(() => rows.value.length > (page.value + 1) * size)

  async function go(target: number) {
    const run = ++generation
    loading.value = true
    error.value = ''
    try {
      while (more.value && rows.value.length <= (target + 1) * size) {
        const first = cursor === undefined
        const result = await fetchPage(cursor)
        if (run !== generation) return
        if (first) {
          matched.value = result.matched
          total.value = result.total
        }
        rows.value = [...rows.value, ...result.items.filter(accepts)]
        cursor = result.next_cursor
        more.value = !!cursor
      }
      page.value = Math.min(target, Math.max(0, Math.ceil(rows.value.length / size) - 1))
    } catch (e) {
      if (run === generation) error.value = messageOf(e)
    } finally {
      if (run === generation) loading.value = false
    }
  }

  function reset() {
    rows.value = []
    page.value = 0
    matched.value = undefined
    total.value = undefined
    cursor = undefined
    more.value = true
    return go(0)
  }
  function replace(match: (item: T) => boolean, updated: T) {
    rows.value = rows.value.map(item => match(item) ? updated : item).filter(accepts)
    page.value = Math.min(page.value, Math.max(0, Math.ceil(rows.value.length / size) - 1))
  }
  const matchCount = computed(() => matched.value ?? (more.value ? undefined : rows.value.length))
  return { items, page, loading, error, hasNext, go, reset, replace, matchCount, total }
}
