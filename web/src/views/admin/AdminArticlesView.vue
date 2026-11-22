<script setup lang="ts">
import { api, call, type AdminArticle } from '@/api/client'
import ResultPager from '@/components/admin/ResultPager.vue'
import { articleStatusText, articleStatusTone, issueDetail, issueLabel, issueTone } from '@/lib/admin'
import { useAdminPages } from '@/lib/adminPages'
import { formatNumber, formatShort } from '@/lib/format'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

type Issue = 'backup_failed' | 'no_snapshot' | 'missing_images' | 'short_text' | 'failing_checks'
type Filter = { key: string; label: string; status?: 'alive' | 'deleted' | 'dormant' | 'invalid' | 'pending'; issue?: Issue; issues?: boolean }
// The tabs the design shows; the others are reached by links (the overview's
// "需要处理") and appear as a tab while selected.
const tabs: Filter[] = [
  { key: 'all', label: '全部' },
  { key: 'alive', label: '观察中', status: 'alive' },
  { key: 'deleted', label: '已删除', status: 'deleted' },
  { key: 'dormant', label: '已结束', status: 'dormant' },
  { key: 'backup_failed', label: issueLabel('backup_failed'), issue: 'backup_failed' },
  { key: 'missing_images', label: issueLabel('missing_images'), issue: 'missing_images' },
  { key: 'failing_checks', label: issueLabel('failing_checks'), issue: 'failing_checks' },
]
const linked: Filter[] = [
  { key: 'issues', label: '全部问题', issues: true },
  { key: 'no_snapshot', label: issueLabel('no_snapshot'), issue: 'no_snapshot' },
  { key: 'short_text', label: issueLabel('short_text'), issue: 'short_text' },
  { key: 'invalid', label: articleStatusText('invalid'), status: 'invalid' },
  { key: 'pending', label: articleStatusText('pending'), status: 'pending' },
]
const route = useRoute()
const router = useRouter()
const filter = computed(() => [...tabs, ...linked].find(f => f.key === route.query.filter) ?? tabs[0]!)
const shown = computed(() => tabs.includes(filter.value) ? tabs : [...tabs, filter.value])
const query = computed(() => typeof route.query.q === 'string' ? route.query.q : '')
const search = ref(query.value)
const { items, page, loading, error, hasNext, go, reset, matchCount, total } = useAdminPages<AdminArticle>(cursor => {
  const f = filter.value
  return call(api.GET('/api/v1/admin/articles', { params: { query: {
    status: f.status, issue: f.issue, issues: f.issues || undefined, search: query.value || undefined, cursor, limit: 50,
  } } }))
}, () => true)
watch([filter, query], () => { search.value = query.value; void reset() }, { immediate: true })
</script>
<template>
  <header class="zg-pagehead"><h1 class="zg-pagehead__title">文章管理</h1></header>
  <div class="zg-tabs admin-tabs" role="group" aria-label="筛选文章">
    <button v-for="f in shown" :key="f.key" type="button" :aria-pressed="filter === f" :class="{ 'is-selected': filter === f }"
      @click="router.replace({ query: { ...route.query, filter: f.key === 'all' ? undefined : f.key } })">{{ f.label }}</button>
  </div>
  <form class="zg-toolbar" role="search" @submit.prevent="router.replace({ query: { ...route.query, q: search.trim() || undefined } })">
    <label class="zg-shard zg-input zg-input--search"><input v-model="search" type="search" placeholder="搜索标题或公众号" aria-label="搜索标题或公众号"></label>
    <button class="zg-shard zg-btn zg-btn--secondary zg-btn--sm">搜索</button>
    <span v-if="matchCount !== undefined" class="zg-toolbar__count" :title="'匹配 / 全部文章'">{{ formatNumber(matchCount) }}<template
      v-if="total !== undefined"> / {{ formatNumber(total) }}</template></span>
  </form>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="reset">重试</button></p>
  <p v-if="loading" class="muted" role="status">加载中…</p>
  <div class="zg-shard zg-tablewrap"><div class="zg-tablewrap__scroll"><table class="zg-table">
    <thead><tr><th>标题 / 公众号</th><th>状态</th><th>备份</th><th class="is-num">观察者</th><th>最近检查</th></tr></thead>
    <tbody><tr v-for="a in items" :key="a.id">
      <td><RouterLink class="zg-table__title" :to="{ name: 'admin-article', params: { id: a.id } }">{{ a.title || '（无标题）' }}</RouterLink>
        <span class="zg-table__sub">{{ a.account_name || '—' }} · 收录于 {{ formatShort(a.first_seen_at) }}</span>
      </td>
      <td><span class="zg-shard zg-tag" :class="'zg-tag--' + articleStatusTone(a.status)"><i class="zg-tag__glyph" />{{ articleStatusText(a.status) }}</span></td>
      <td>{{ a.snapshots }} 份 <span v-if="a.latest" class="zg-table__sub">{{ a.latest.images }} 张图片<template v-if="a.latest.missing_images">，缺 {{ a.latest.missing_images }} 张</template><template v-if="a.latest.text_runes != null"> · {{ a.latest.text_runes }} 字</template></span>
        <span v-if="a.issues.length" class="issues"><span v-for="i in a.issues" :key="i" class="zg-shard zg-tag" :class="'zg-tag--' + issueTone(i)"
          :title="issueDetail(i)"><i class="zg-tag__glyph" />{{ issueLabel(i) }}</span></span>
      </td><td class="is-num">{{ a.watchers }}</td><td class="is-mono">{{ formatShort(a.last_checked_at) || '—' }}</td>
    </tr><tr v-if="!items.length && !loading && !error"><td colspan="5">没有符合条件的文章。</td></tr></tbody>
  </table></div></div>
  <ResultPager :page="page" :has-next="hasNext" :loading="loading" @go="go" />
</template>
<style scoped>
.admin-tabs { flex-wrap: wrap; margin-bottom: var(--space-4); gap: var(--space-2) var(--space-4); }
.issues { display: flex; flex-wrap: wrap; gap: var(--space-1); margin-top: var(--space-1); }
.zg-table__title { max-width: 30em; white-space: normal; min-width: 10em; }
</style>
