<script setup lang="ts">
import { api, call, type AdminArticle } from '@/api/client'
import ResultPager from '@/components/admin/ResultPager.vue'
import { articleStatusText, issueDetail, issueLabel } from '@/lib/admin'
import { useAdminPages } from '@/lib/adminPages'
import { formatShort } from '@/lib/format'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const filters = [
  ['all', '全部'], ['alive', '在线'], ['deleted', '已删除'], ['dormant', '休眠'], ['issues', '有问题'],
  ['backup_failed', '备份失败'], ['missing_images', '图片缺失'], ['failing_checks', '检查异常'], ['invalid', '无效'], ['pending', '解析中'],
] as const
const route = useRoute()
const router = useRouter()
const filter = computed(() => filters.find(([key]) => key === route.query.filter)?.[0] ?? 'all')
const query = computed(() => typeof route.query.q === 'string' ? route.query.q : '')
const search = ref(query.value)
const issue = computed(() => ['backup_failed', 'missing_images', 'failing_checks'].includes(filter.value))
const { items, page, loading, error, hasNext, go, reset, matchCount } = useAdminPages<AdminArticle>(cursor => {
  const f = filter.value
  const status = f === 'alive' || f === 'deleted' || f === 'dormant' || f === 'invalid' || f === 'pending' ? f : undefined
  return call(api.GET('/api/v1/admin/articles', { params: { query: {
    status, issues: f === 'issues' || issue.value || undefined, search: query.value || undefined, cursor, limit: 50,
  } } }))
}, a => !issue.value || a.issues.includes(filter.value))
watch([filter, query], () => { search.value = query.value; void reset() }, { immediate: true })
const tones = { alive: 'watching', deleted: 'deleted', dormant: 'ended', invalid: 'unreachable', pending: 'archiving' }
</script>
<template>
  <header class="zg-pagehead"><h1 class="zg-pagehead__title">文章管理</h1></header>
  <div class="zg-tabs admin-tabs" role="group" aria-label="筛选文章">
    <button v-for="[key, label] in filters" :key="key" :aria-pressed="filter === key" :class="{ 'is-selected': filter === key }"
      @click="router.replace({ query: { ...route.query, filter: key === 'all' ? undefined : key } })">{{ label }}</button>
  </div>
  <form class="zg-toolbar" role="search" @submit.prevent="router.replace({ query: { ...route.query, q: search.trim() || undefined } })">
    <label class="zg-shard zg-input zg-input--search"><input v-model="search" type="search" placeholder="搜索标题或公众号" aria-label="搜索标题或公众号"></label>
    <button class="zg-shard zg-btn zg-btn--secondary zg-btn--sm">搜索</button>
    <span v-if="matchCount !== undefined" class="zg-toolbar__count">匹配 {{ matchCount }} 篇</span>
  </form>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="reset">重试</button></p>
  <p v-if="loading" class="muted" role="status">加载中…</p>
  <div class="zg-shard zg-tablewrap"><table class="zg-table">
    <thead><tr><th>标题 / 公众号</th><th>状态</th><th>备份</th><th class="is-num">观察者</th><th>最近检查</th></tr></thead>
    <tbody><tr v-for="a in items" :key="a.id">
      <td><RouterLink class="zg-table__title" :to="{ name: 'admin-article', params: { id: a.id } }">{{ a.title || '（无标题）' }}</RouterLink>
        <span class="zg-table__sub">{{ a.account_name || '—' }} · 收录于 {{ formatShort(a.first_seen_at) }}</span>
      </td>
      <td><span class="zg-shard zg-tag" :class="'zg-tag--' + tones[a.status]"><i class="zg-tag__glyph" />{{ articleStatusText(a.status) }}</span></td>
      <td>{{ a.snapshots }} 份 <span v-if="a.latest" class="zg-table__sub">{{ a.latest.images }} 张图片<template v-if="a.latest.missing_images">，缺 {{ a.latest.missing_images }} 张</template><template v-if="a.latest.text_runes != null"> · {{ a.latest.text_runes }} 字</template></span>
        <span v-for="i in a.issues" :key="i" class="issue" :title="issueDetail(i)">{{ issueLabel(i) }}</span>
      </td><td class="is-num">{{ a.watchers }}</td><td class="is-mono">{{ formatShort(a.last_checked_at) || '—' }}</td>
    </tr><tr v-if="!items.length && !loading && !error"><td colspan="5">没有符合条件的文章。</td></tr></tbody>
  </table></div>
  <ResultPager :page="page" :has-next="hasNext" :loading="loading" @go="go" />
</template>
<style scoped>
.admin-tabs { flex-wrap: wrap; margin-bottom: var(--space-4); gap: var(--space-2) var(--space-4); }
.issue { display: inline-block; font-size: 12px; background: var(--status-partial-soft); color: var(--status-partial); margin-right: 4px; padding: 0 4px; }
.zg-table__title { max-width: 30em; white-space: normal; min-width: 10em; }
</style>
