<script setup lang="ts">
import { api, call, messageOf, type Watch, type WatchDetail } from '@/api/client'
import StatusBadge from '@/components/StatusBadge.vue'
import SubmitBox from '@/components/SubmitBox.vue'
import { formatShort } from '@/lib/format'
import { deletedReasonText } from '@/lib/status'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

type Filter = 'all' | 'active' | 'ended' | 'failed'
const filters: { key: Filter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'active', label: '观察中' },
  { key: 'ended', label: '已结束' },
  { key: 'failed', label: '失败' },
]

const route = useRoute()
const router = useRouter()

const filter = computed<Filter>(() => {
  const f = route.query.status
  return filters.some((x) => x.key === f) ? (f as Filter) : 'all'
})
const items = ref<Watch[]>([])
const next = ref<string | undefined>()
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const notice = ref(typeof route.query.notice === 'string' ? route.query.notice : '')

async function fetchPage(cursor?: string) {
  const status = filter.value === 'all' ? undefined : filter.value
  return call(api.GET('/api/v1/watches', { params: { query: { status, cursor, limit: 20 } } }))
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const page = await fetchPage()
    items.value = page.items
    next.value = page.next_cursor
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!next.value) return
  loadingMore.value = true
  try {
    const page = await fetchPage(next.value)
    items.value.push(...page.items)
    next.value = page.next_cursor
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    loadingMore.value = false
  }
}

// Watches still being backed up change on their own; refresh while any are.
let poll: ReturnType<typeof setTimeout> | undefined
function schedulePoll() {
  clearTimeout(poll)
  if (items.value.some((w) => w.status === 'resolving')) {
    poll = setTimeout(async () => {
      if (document.visibilityState === 'visible') {
        try {
          const page = await fetchPage()
          // Replace only the first page; keep what "load more" added.
          const rest = items.value.slice(page.items.length)
          items.value = [...page.items, ...rest.filter((w) => !page.items.some((p) => p.id === w.id))]
        } catch {
          // try again next time
        }
      }
      schedulePoll()
    }, 5000)
  }
}
watch(items, schedulePoll)
onBeforeUnmount(() => clearTimeout(poll))

watch(filter, load)
onMounted(load)

function setFilter(f: Filter) {
  void router.replace({ query: f === 'all' ? {} : { status: f } })
}

function onSubmitted(d: WatchDetail) {
  void router.push({ name: 'watch', params: { id: d.watch.id } })
}

function title(w: Watch): string {
  return w.article?.title || (w.status === 'resolving' ? '正在获取文章…' : w.submitted_url)
}

function meta(w: Watch): string {
  const parts: string[] = []
  if (w.article?.account_name) parts.push(w.article.account_name)
  if (w.status === 'active') parts.push(`观察至 ${formatShort(w.expires_at)}`)
  else if (w.end_reason === 'deleted') parts.push(deletedReasonText(w.article?.deleted_reason))
  else if (w.ended_at) parts.push(`结束于 ${formatShort(w.ended_at)}`)
  else parts.push(`提交于 ${formatShort(w.created_at)}`)
  return parts.join(' · ')
}
</script>

<template>
  <SubmitBox @submitted="onSubmitted" />

  <section class="section">
    <div class="list-head">
      <h1>我的观察</h1>
      <div class="filters" role="tablist" aria-label="按状态筛选">
        <button
          v-for="f in filters"
          :key="f.key"
          type="button"
          role="tab"
          class="chip"
          :aria-selected="filter === f.key"
          @click="setFilter(f.key)"
        >
          {{ f.label }}
        </button>
      </div>
    </div>

    <p v-if="notice" class="notice notice-info">{{ notice }}</p>
    <p v-if="error" class="notice notice-danger" role="alert">
      {{ error }} <button type="button" class="btn-link" @click="load">重试</button>
    </p>

    <p v-if="loading && !items.length" class="muted center"><span class="spinner" /> 加载中…</p>
    <div v-else-if="!items.length && !error" class="empty card center muted">
      <template v-if="filter === 'all'">还没有观察任何文章。把文章链接粘贴到上面的输入框就可以开始。</template>
      <template v-else>没有这个状态的观察。</template>
    </div>

    <ul v-else class="items">
      <li v-for="w in items" :key="w.id">
        <RouterLink :to="{ name: 'watch', params: { id: w.id } }" class="item card">
          <div class="item-main">
            <div class="item-title">{{ title(w) }}</div>
            <div class="muted small">{{ meta(w) }}</div>
          </div>
          <StatusBadge :watch="w" />
        </RouterLink>
      </li>
    </ul>

    <div v-if="next" class="center more">
      <button type="button" class="btn" :disabled="loadingMore" @click="loadMore">
        <span v-if="loadingMore" class="spinner" aria-hidden="true" />
        加载更多
      </button>
    </div>
  </section>
</template>

<style scoped>
.list-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px 16px;
  margin: 24px 0 12px;
}
.list-head h1 {
  margin: 0;
  font-size: 1.25rem;
}
.filters {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.chip {
  font: inherit;
  font-size: 0.875rem;
  padding: 2px 12px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  cursor: pointer;
}
.chip[aria-selected='true'] {
  background: var(--fg);
  border-color: var(--fg);
  color: var(--bg);
}
.items {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 8px;
}
.item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
  color: inherit;
}
.item:hover {
  text-decoration: none;
  border-color: var(--accent);
}
.item-main {
  flex: 1;
  min-width: 0;
}
.item-title {
  font-weight: 600;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.empty {
  padding: 32px 16px;
}
.more {
  margin-top: 16px;
}
</style>
