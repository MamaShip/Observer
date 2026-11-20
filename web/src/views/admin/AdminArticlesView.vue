<script setup lang="ts">
import { api, call, messageOf, type AdminArticle } from '@/api/client'
import AdminNav from '@/components/admin/AdminNav.vue'
import { articleStatusText, issueDetail, issueLabel } from '@/lib/admin'
import { formatNumber, formatRelative, formatShort } from '@/lib/format'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

type Filter = 'all' | 'issues' | 'alive' | 'deleted' | 'dormant' | 'invalid' | 'pending'
const filters: { key: Filter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'issues', label: '有问题' },
  { key: 'alive', label: '在线' },
  { key: 'deleted', label: '已删除' },
  { key: 'dormant', label: '休眠' },
  { key: 'invalid', label: '无效' },
  { key: 'pending', label: '解析中' },
]

const route = useRoute()
const router = useRouter()
const filter = computed<Filter>(() => {
  const f = route.query.filter
  return filters.some((x) => x.key === f) ? (f as Filter) : 'all'
})
const search = ref(typeof route.query.q === 'string' ? route.query.q : '')
const items = ref<AdminArticle[]>([])
const next = ref<string | undefined>()
const loading = ref(false)
const error = ref('')

async function load(more = false) {
  loading.value = true
  error.value = ''
  const f = filter.value
  try {
    const page = await call(
      api.GET('/api/v1/admin/articles', {
        params: {
          query: {
            issues: f === 'issues' || undefined,
            status: f === 'all' || f === 'issues' ? undefined : f,
            search: search.value.trim() || undefined,
            cursor: more ? next.value : undefined,
            limit: 50,
          },
        },
      }),
    )
    items.value = more ? [...items.value, ...page.items] : page.items
    next.value = page.next_cursor
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    loading.value = false
  }
}
watch(() => route.query, () => load(), { immediate: true })

function setFilter(f: Filter) {
  void router.replace({ query: { ...route.query, filter: f === 'all' ? undefined : f } })
}
function doSearch() {
  void router.replace({ query: { ...route.query, q: search.value.trim() || undefined } })
}
</script>

<template>
  <AdminNav />

  <div class="toolbar">
    <div class="filters" role="tablist" aria-label="筛选">
      <button v-for="f in filters" :key="f.key" type="button" role="tab" class="chip" :aria-selected="filter === f.key" @click="setFilter(f.key)">
        {{ f.label }}
      </button>
    </div>
    <form class="row" role="search" @submit.prevent="doSearch">
      <input v-model="search" type="text" placeholder="标题或公众号" aria-label="按标题或公众号搜索">
      <button type="submit" class="btn">搜索</button>
    </form>
  </div>

  <p v-if="filter === 'issues'" class="muted small">
    "有问题"包括：抓取成功却没有备份、有图片没存下来、正文不到 100 字、检查连续失败。可以进入详情查看备份，文章仍在线时可以重新备份。
  </p>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
  <p v-if="loading && !items.length" class="muted"><span class="spinner" /> 加载中…</p>
  <p v-else-if="!items.length && !error" class="card muted center">没有符合条件的文章。</p>

  <ul class="items">
    <li v-for="a in items" :key="a.id">
      <RouterLink :to="{ name: 'admin-article', params: { id: a.id } }" class="item card">
        <div class="main">
          <div class="title">{{ a.title || '（无标题）' }}</div>
          <div class="muted small">
            {{ a.account_name || '—' }} · 首次提交 {{ formatShort(a.first_seen_at) }}
            <template v-if="a.last_checked_at"> · 检查于 {{ formatRelative(a.last_checked_at) }}</template>
          </div>
          <div class="facts small">
            <span>{{ a.watchers }} 人观察</span>
            <span>{{ a.snapshots }} 份备份</span>
            <template v-if="a.latest">
              <span>图片 {{ a.latest.images }}<template v-if="a.latest.missing_images">（缺 {{ a.latest.missing_images }}）</template></span>
              <span v-if="a.latest.text_runes != null">正文 {{ formatNumber(a.latest.text_runes) }} 字</span>
            </template>
          </div>
          <div v-if="a.issues.length" class="issues">
            <span v-for="i in a.issues" :key="i" class="issue" :title="issueDetail(i)">{{ issueLabel(i) }}</span>
          </div>
        </div>
        <span class="status" :class="`s-${a.status}`">{{ articleStatusText(a.status) }}</span>
      </RouterLink>
    </li>
  </ul>
  <div v-if="next" class="center more">
    <button type="button" class="btn" :disabled="loading" @click="load(true)">加载更多</button>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 12px;
}
.toolbar input {
  width: 220px;
  max-width: 100%;
}
.toolbar form {
  flex-wrap: nowrap;
}
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
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
  gap: 12px;
  align-items: flex-start;
  color: inherit;
  padding: 12px 14px;
}
.item:hover {
  text-decoration: none;
  border-color: var(--accent);
}
.main {
  flex: 1;
  min-width: 0;
}
.title {
  font-weight: 600;
}
.facts {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 14px;
  margin-top: 2px;
  font-variant-numeric: tabular-nums;
}
.issues {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}
.issue {
  font-size: 0.75rem;
  padding: 0 8px;
  border-radius: 999px;
  background: var(--warn-soft);
  color: var(--warn);
  border: 1px solid color-mix(in srgb, var(--warn) 35%, transparent);
}
.status {
  flex: none;
  font-size: 0.8rem;
  font-weight: 600;
  padding: 1px 8px;
  border-radius: 999px;
  background: var(--muted-soft);
  color: var(--muted);
}
.s-alive {
  background: var(--ok-soft);
  color: var(--ok);
}
.s-deleted {
  background: var(--danger-soft);
  color: var(--danger);
}
.s-pending {
  background: var(--accent-soft);
  color: var(--accent);
}
.more {
  margin-top: 14px;
}
</style>
