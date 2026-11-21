<script setup lang="ts">
import { api, call, messageOf, type Watch, type WatchDetail } from '@/api/client'
import StatusBadge from '@/components/StatusBadge.vue'
import SubmitBox from '@/components/SubmitBox.vue'
import { formatShort } from '@/lib/format'
import { daysLeft, endedText, endingSoonDays, isOpen } from '@/lib/status'
import { session } from '@/session'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const filters = [
  { key: 'all', label: '全部' }, { key: 'active', label: '观察中' }, { key: 'soon', label: '即将到期' },
  { key: 'deleted', label: '已删除' }, { key: 'ended', label: '已结束' },
] as const
type Filter = typeof filters[number]['key']
type Sort = 'recent' | 'expiry'
const route = useRoute()
const router = useRouter()
const filter = computed(() => filters.find(f => f.key === route.query.status)?.key ?? 'all')
const sort = ref<Sort>('recent')
const items = ref<Watch[]>([])
const cursor = ref<string>()
const visibleCount = ref(20)
const complete = ref(false)
const loading = ref(false)
const error = ref('')
const submitted = ref<WatchDetail>()
const notice = computed(() => typeof route.query.notice === 'string' ? route.query.notice : '')
const openCount = computed(() => items.value.filter(isOpen).length)

function matches(w: Watch, key: Filter) {
  switch (key) {
    case 'all': return true
    case 'active': return isOpen(w)
    case 'soon': return w.status === 'active' && daysLeft(w) <= endingSoonDays
    case 'deleted': return w.end_reason === 'deleted'
    // Links that never opened end at once, so they are among the ended.
    case 'ended': return (w.status === 'ended' && w.end_reason !== 'deleted') || w.status === 'failed'
  }
}
const counts = computed(() => filters.map(f => items.value.filter(w => matches(w, f.key)).length))
const results = computed(() => {
  const rows = items.value.filter(w => matches(w, filter.value))
  if (sort.value === 'expiry') rows.sort((a, b) => {
    if (isOpen(a) !== isOpen(b)) return isOpen(a) ? -1 : 1
    return isOpen(a) ? a.expires_at.localeCompare(b.expires_at) : 0
  })
  return rows
})

let generation = 0
let poll: ReturnType<typeof setTimeout> | undefined
// The API returns a cursor, not totals. Read additional pages on demand;
// global sorting and derived filters need the full collection.
async function load(reset = false, keepVisible = false) {
  const run = ++generation
  const target = keepVisible ? items.value.length : 0
  loading.value = true
  error.value = ''
  let after = reset ? undefined : cursor.value
  const collected = after ? [...items.value] : []
  const seen = new Set(collected.map(w => w.id))
  try {
    do {
      const page = await call(api.GET('/api/v1/watches', { params: { query: { cursor: after, limit: 20 } } }))
      if (run !== generation) return
      for (const w of page.items) {
        if (!seen.has(w.id)) collected.push(w)
        seen.add(w.id)
      }
      after = page.next_cursor
    } while (after && (filter.value !== 'all' || sort.value !== 'recent' || collected.length < target))
    items.value = collected
    cursor.value = after
    complete.value = !after
    if (reset && !keepVisible) visibleCount.value = 20
  } catch (e) {
    if (run === generation) error.value = messageOf(e)
  } finally {
    if (run === generation) loading.value = false
  }
}

watch([filter, sort], () => {
  visibleCount.value = 20
  if (!complete.value) void load()
}, { immediate: true })

// Refetch the loaded range: a resolving link may have merged into an older watch.
function schedulePoll() {
  clearTimeout(poll)
  if (!items.value.some(w => w.status === 'resolving')) return
  poll = setTimeout(async () => {
    if (document.visibilityState === 'visible' && !loading.value) await load(true, true)
    if (!disposed) schedulePoll()
  }, 5000)
}
let disposed = false
watch(items, schedulePoll)
onBeforeUnmount(() => { disposed = true; generation++; clearTimeout(poll) })

function more() {
  visibleCount.value += 20
  if (results.value.length < visibleCount.value && !complete.value) void load()
}
function onSubmitted(d: WatchDetail) {
  submitted.value = d
  void load(true)
}
</script>

<template>
    <div class="list-page">
      <header class="zg-pagehead">
        <div>
          <h1 class="zg-pagehead__title">我的观察</h1>
          <div v-if="complete" class="zg-pagehead__meta">正在观察 {{ openCount }} 篇文章 <span v-if="session.site" class="zg-meta">{{ openCount }} /
            {{ session.site.max_open_watches }}</span></div>
        </div>
      </header>
      <SubmitBox @submitted="onSubmitted">
        <div v-if="submitted" class="zg-shard zg-msg zg-msg--success" role="status">
          <i class="zg-msg__glyph" />已开始观察{{ submitted.watch.article?.title ? '「' + submitted.watch.article.title + '」' : '这篇文章' }}{{ submitted.watch.status === 'resolving' ? '，正在保存当前版本' : '' }}。
          <RouterLink class="zg-link zg-msg__action" :to="{ name: 'watch', params: { id: submitted.watch.id } }">查看</RouterLink>
        </div>
      </SubmitBox>
      <div class="list-toolbar">
        <div class="zg-tabs" role="group" aria-label="按状态筛选">
          <button v-for="(f, i) in filters" :key="f.key" type="button" :aria-pressed="filter === f.key"
            :class="{ 'is-selected': filter === f.key }"
            @click="router.replace({ query: { ...route.query, status: f.key === 'all' ? undefined : f.key } })">
            {{ f.label }}<span v-if="complete" class="zg-tabs__count">{{ counts[i] }}</span>
          </button>
        </div>
        <label class="zg-shard zg-select zg-select--sm">
          <select v-model="sort" aria-label="排序">
            <option value="recent">最近提交</option>
            <option value="expiry">最快到期</option>
          </select>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
        </label>
      </div>
      <p v-if="notice" class="notice notice-info">{{ notice }}</p>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="load()">重试</button></p>
      <p v-if="loading" class="muted" role="status"><span class="spinner" /> 加载中…</p>
      <p v-else-if="!results.length && !error" class="zg-empty__body">{{ filter === 'all' ? '还没有观察任何文章。粘贴上面的链接就可以开始。' : '没有符合这个状态的观察。' }}</p>
      <div class="watch-list">
        <RouterLink v-for="w in results.slice(0, visibleCount)" :key="w.id" :to="{ name: 'watch', params: { id: w.id } }"
          class="item zg-shard zg-watch" :class="{ 'is-deleted': w.end_reason === 'deleted' }">
          <div class="zg-watch__top">
            <StatusBadge :watch="w" />
            <span class="zg-meta" v-if="w.status === 'active'">还剩 {{ daysLeft(w) }} 天</span>
            <span class="zg-meta" v-else-if="w.status === 'failed'">{{ endedText(w) }}</span>
            <span class="zg-meta" v-else-if="w.ended_at">{{ endedText(w) }} {{ formatShort(w.ended_at) }}</span>
          </div>
          <h2 class="zg-watch__title">{{ w.article?.title || (w.status === 'resolving' ? '正在获取文章…' : w.submitted_url) }}</h2>
          <div v-if="w.article?.account_name" class="zg-watch__source">来自公众号「{{ w.article.account_name }}」</div>
          <div class="zg-watch__foot">
            <span class="zg-meta">提交于 {{ formatShort(w.created_at) }}</span>
            <span class="zg-link small">查看详情</span>
          </div>
        </RouterLink>
      </div>
      <button v-if="cursor || results.length > visibleCount" type="button" class="zg-shard zg-btn zg-btn--secondary more" :disabled="loading"
        @click="more">加载更多</button>
    </div>
</template>

<style scoped>
.list-toolbar { display: flex; align-items: flex-end; justify-content: space-between; flex-wrap: wrap; gap: var(--space-3); margin: var(--space-6) 0 var(--space-4); }
.zg-tabs { flex: 1 1 auto; flex-wrap: wrap; gap: var(--space-2) var(--space-4); }
.zg-select { margin-bottom: var(--space-2); }
.watch-list { display: grid; gap: var(--space-3); }
.zg-watch { max-width: none; text-decoration: none; }
.zg-watch:hover { --_edge: var(--line-200); }
.more { margin-top: var(--space-4); }
</style>
