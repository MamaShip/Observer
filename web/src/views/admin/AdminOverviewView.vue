<script setup lang="ts">
import { api, call, messageOf, type AdminArticle, type AdminDay, type AdminOverview } from '@/api/client'
import ActivityTrend from '@/components/admin/ActivityTrend.vue'
import ZgIcon from '@/components/ZgIcon.vue'
import { articleStatusText, issueLabel, issueTone } from '@/lib/admin'
import BarChart from '@/components/admin/BarChart.vue'
import StatTile from '@/components/admin/StatTile.vue'
import { formatBytes, formatDate, formatNumber, formatPercent, formatRelative } from '@/lib/format'
import { computed, onMounted, ref } from 'vue'

const o = ref<AdminOverview | null>(null)
const error = ref('')
const loading = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    o.value = await call(api.GET('/api/v1/admin/overview'))
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    loading.value = false
  }
}
const issues = ref<AdminArticle[]>([])
const issuesError = ref('')
const issuesLoading = ref(true)
async function loadIssues() {
  issuesLoading.value = true
  issuesError.value = ''
  try {
    const page = await call(api.GET('/api/v1/admin/articles', { params: { query: { issues: true, limit: 5 } } }))
    issues.value = page.items
  } catch (e) { issuesError.value = messageOf(e) }
  finally { issuesLoading.value = false }
}
onMounted(load)
onMounted(loadIssues)

const series: { key: keyof Omit<AdminDay, 'day'>; title: string }[] = [
  { key: 'watches', title: '新观察' },
  { key: 'users', title: '新账户' },
  { key: 'snapshots', title: '新备份' },
  { key: 'deletions', title: '发现删除' },
  { key: 'checks', title: '检查次数' },
  { key: 'blocked', title: '被微信拦截' },
  { key: 'emails', title: '发出邮件' },
]
const days = computed(() => o.value?.days.map((d) => d.day) ?? [])
const values = (key: keyof Omit<AdminDay, 'day'>) => o.value?.days.map((d) => d[key]) ?? []

const healthy = computed(() => o.value?.health.status === 'ok')
</script>

<template>
    <header class="zg-pagehead">
      <div>
        <h1 class="zg-pagehead__title">后台概览</h1>
        <div v-if="o" class="zg-pagehead__meta">服务版本 {{ o.health.version }}</div>
      </div>
      <button type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="loading" @click="load(); loadIssues()"><ZgIcon name="refresh" />刷新</button>
    </header>
    <p v-if="error" class="notice notice-danger" role="alert">
      {{ error }} <button type="button" class="btn-link" @click="load">重试</button>
    </p>
    <p v-if="loading && !o" class="muted"><span class="spinner" /> 加载中…</p>

    <template v-if="o">
      <section>
        <h2 class="zg-panel__title">服务状态</h2>
        <ul class="zg-shard zg-health">
          <li>
            <span>服务</span><span class="zg-meta">{{ o.health.version }}</span><span class="zg-health__state" :class="healthy ? 'is-ok' : 'is-down'"><i />{{ healthy ? '运行正常' : '服务异常' }}</span>
          </li>
          <li>
            <span>数据库</span><span /><span class="zg-health__state" :class="o.health.database === 'ok' ? 'is-ok' : 'is-down'"><i />{{ o.health.database === 'ok' ? '正常' : '不可用' }}</span>
          </li>
          <li>
            <span>后台任务</span><span class="zg-meta">{{ o.health.last_heartbeat ? '心跳 ' + formatRelative(o.health.last_heartbeat) : '' }}</span>
            <span class="zg-health__state" :class="o.health.worker === 'ok' ? 'is-ok' : 'is-slow'"><i />{{ { ok: '正常', starting: '启动中', stalled: '停滞' }[o.health.worker] ?? o.health.worker }}</span>
          </li>
        </ul>
      </section>
      <div class="zg-stats summary">
        <StatTile label="账户" :value="formatNumber(o.totals.users)" :note="`近 30 天活跃 ${o.totals.active_users_30d}`" />
        <StatTile label="观察中" :value="formatNumber(o.totals.open_watches)" :note="`累计 ${o.totals.watches} 个观察`" />
        <StatTile label="备份" :value="formatNumber(o.totals.snapshots)" :note="`共 ${o.totals.images} 张图片`" />
        <StatTile class="zg-stat--deleted" label="本月发现删除" :value="formatNumber(o.month.deletions)" :note="`今天 ${o.today.deletions}`" />
        <StatTile label="本月通知邮件" :value="formatNumber(o.month.notification_emails)" />
      </div>
      <h2 class="zg-panel__title">近 30 天趋势</h2>
      <div class="charts">
        <ActivityTrend :days="o.days" />
        <BarChart title="发现删除" :days="days" :values="values('deletions')" class="deletions" />
      </div>
      <section class="zg-shard zg-panel queue">
        <div class="zg-panel__head">
          <h2 class="zg-panel__title">需要处理</h2>
          <RouterLink class="zg-link small" to="/admin/articles?filter=issues">查看全部</RouterLink>
        </div>
        <p v-if="issuesError" class="notice notice-danger" role="alert">{{ issuesError }} <button class="btn-link"
            @click="loadIssues">重试</button></p>
        <div v-for="a in issues" :key="a.id" class="zg-setrow">
          <RouterLink :to="{ name: 'admin-article', params: { id: a.id } }" class="zg-link">{{ a.title || '（无标题）' }}</RouterLink>
          <span class="issue-tags"><span v-for="i in a.issues" :key="i" class="zg-shard zg-tag" :class="'zg-tag--' + issueTone(i)"><i
            class="zg-tag__glyph" />{{ issueLabel(i) }}</span></span>
        </div>
        <p v-if="!issues.length && !issuesError && !issuesLoading" class="muted small">暂无需要处理的文章。</p>
      </section>
      <details class="additional">
        <summary>查看完整用量与统计</summary>
        <h2>本月用量 <span class="muted small">自 {{ formatDate(o.month_start) }}（北京时间）</span></h2>
        <div class="zg-stats">
          <StatTile label="今日邮件（服务商额度）" :value="`${o.emails_today} / ${o.limits.daily_emails}`" note="UTC 0 点重置；通知类最多用到 90%" />
          <StatTile label="本月邮件" :value="formatNumber(o.month.emails)"
            :note="`验证码 ${o.month.login_emails}${o.month.join_emails ? `（邀请链接 ${o.month.join_emails}）` : ''} · 邀请 ${o.month.invite_emails} · 通知 ${o.month.notification_emails} · 告警 ${o.month.alert_emails}${o.month.failed_emails ? ` · 失败 ${o.month.failed_emails}` : ''}`" />
          <StatTile label="本月检查" :value="formatNumber(o.month.checks)"
            :note="`被拦截 ${o.month.blocked}（${formatPercent(o.month.blocked, o.month.checks)}）· 出错 ${o.month.errors}`" />
          <StatTile label="页面抓取流量" :value="formatBytes(o.month.page_bytes)" note="微信页面（压缩后），不含图片" />
          <StatTile label="新图片" :value="formatBytes(o.month.new_image_bytes)" note="首次保存的图片，约等于图片下载流量" />
          <StatTile label="本月新增" :value="`${o.month.new_watches} 个观察`"
            :note="`账户 ${o.month.new_users} · 文章 ${o.month.new_articles} · 备份 ${o.month.new_snapshots}`" />
          <StatTile label="本月发现删除" :value="formatNumber(o.month.deletions)" :note="`今天 ${o.today.deletions}`" />
          <StatTile label="今天" :value="`${o.today.new_watches} 个观察`"
            :note="`检查 ${o.today.checks} · 拦截 ${o.today.blocked} · 邮件 ${o.today.emails}`" />
        </div>

        <h2>累计</h2>
        <div class="zg-stats">
          <StatTile label="账户" :value="formatNumber(o.totals.users)"
            :note="`30 天内登录 ${o.totals.active_users_30d}${o.totals.disabled_users ? ` · 已停用 ${o.totals.disabled_users}` : ''}`" />
          <StatTile label="观察中" :value="formatNumber(o.totals.open_watches)" :note="`累计 ${o.totals.watches} 个观察`" />
          <StatTile label="文章" :value="formatNumber(o.totals.articles)"
            :note="`${articleStatusText('alive')} ${o.totals.articles_alive} · ${articleStatusText('deleted')} ${o.totals.articles_deleted} · ${articleStatusText('dormant')} ${o.totals.articles_dormant} · ${articleStatusText('invalid')} ${o.totals.articles_invalid}${o.totals.articles_pending ? ` · ${articleStatusText('pending')} ${o.totals.articles_pending}` : ''}`" />
          <StatTile label="备份" :value="formatNumber(o.totals.snapshots)" :note="`图片 ${formatNumber(o.totals.images)} 张`" />
          <StatTile label="对象存储" :value="formatBytes(o.totals.image_bytes + o.totals.snapshot_bytes)"
            :note="`图片 ${formatBytes(o.totals.image_bytes)} · 页面 ${formatBytes(o.totals.snapshot_bytes)}（不含原始页面与数据库备份）`" />
        </div>

        <h2>近 30 天</h2>
        <div class="charts">
          <BarChart v-for="s in series" :key="s.key" :title="s.title" :days="days" :values="values(s.key)" />
        </div>
        <details class="table-view">
          <summary>查看数据表</summary>
          <div class="zg-shard zg-tablewrap"><div class="zg-tablewrap__scroll">
            <table class="zg-table">
              <thead>
                <tr>
                  <th>日期</th>
                  <th v-for="s in series" :key="s.key">{{ s.title }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in [...o.days].reverse()" :key="d.day">
                  <td>{{ d.day }}</td>
                  <td v-for="s in series" :key="s.key" class="num">{{ d[s.key] }}</td>
                </tr>
              </tbody>
            </table>
          </div></div>
        </details>
      </details>
    </template>
</template>

<style scoped>
.issue-tags { display: flex; flex-wrap: wrap; gap: var(--space-1); }
h2 { margin: var(--space-5) 0 var(--space-3); font-size: 16px; }
.zg-panel__head h2 { margin: 0; }
.summary, .queue, .additional { margin-top: var(--space-5); }
.charts { display: grid; gap: var(--space-4); grid-template-columns: repeat(auto-fit, minmax(min(100%, 270px), 1fr)); }
.deletions { --bar-color: var(--chart-deleted); }
.additional summary { color: var(--link); }
.table-view { margin-top: var(--space-4); }
@media (max-width: 767px) { .summary { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
