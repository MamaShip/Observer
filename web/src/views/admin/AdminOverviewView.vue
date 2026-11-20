<script setup lang="ts">
import { api, call, messageOf, type AdminDay, type AdminOverview } from '@/api/client'
import AdminNav from '@/components/admin/AdminNav.vue'
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
onMounted(load)

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
  <AdminNav />
  <p v-if="error" class="notice notice-danger" role="alert">
    {{ error }} <button type="button" class="btn-link" @click="load">重试</button>
  </p>
  <p v-if="loading && !o" class="muted"><span class="spinner" /> 加载中…</p>

  <template v-if="o">
    <section class="health small" :class="healthy ? 'ok' : 'bad'">
      <span><strong>{{ healthy ? '运行正常' : '服务异常' }}</strong></span>
      <span>数据库：{{ o.health.database === 'ok' ? '正常' : '不可用' }}</span>
      <span>后台任务：{{ { ok: '正常', starting: '启动中', stalled: '停滞' }[o.health.worker] ?? o.health.worker }}
        <template v-if="o.health.last_heartbeat">（心跳 {{ formatRelative(o.health.last_heartbeat) }}）</template></span>
      <span>版本 {{ o.health.version }}</span>
      <button type="button" class="btn-link" :disabled="loading" @click="load">刷新</button>
    </section>

    <h2>本月用量 <span class="muted small">自 {{ formatDate(o.month_start) }}（北京时间）</span></h2>
    <div class="tiles">
      <StatTile
        label="今日邮件（服务商额度）"
        :value="`${o.emails_today} / ${o.limits.daily_emails}`"
        note="UTC 0 点重置；通知类最多用到 90%"
      />
      <StatTile
        label="本月邮件"
        :value="formatNumber(o.month.emails)"
        :note="`验证码 ${o.month.login_emails} · 通知 ${o.month.notification_emails} · 告警 ${o.month.alert_emails}${o.month.failed_emails ? ` · 失败 ${o.month.failed_emails}` : ''}`"
      />
      <StatTile
        label="本月检查"
        :value="formatNumber(o.month.checks)"
        :note="`被拦截 ${o.month.blocked}（${formatPercent(o.month.blocked, o.month.checks)}）· 出错 ${o.month.errors}`"
      />
      <StatTile label="页面抓取流量" :value="formatBytes(o.month.page_bytes)" note="微信页面（压缩后），不含图片" />
      <StatTile label="新图片" :value="formatBytes(o.month.new_image_bytes)" note="首次保存的图片，约等于图片下载流量" />
      <StatTile
        label="本月新增"
        :value="`${o.month.new_watches} 个观察`"
        :note="`账户 ${o.month.new_users} · 文章 ${o.month.new_articles} · 备份 ${o.month.new_snapshots}`"
      />
      <StatTile label="本月发现删除" :value="formatNumber(o.month.deletions)" :note="`今天 ${o.today.deletions}`" />
      <StatTile
        label="今天"
        :value="`${o.today.new_watches} 个观察`"
        :note="`检查 ${o.today.checks} · 拦截 ${o.today.blocked} · 邮件 ${o.today.emails}`"
      />
    </div>

    <h2>累计</h2>
    <div class="tiles">
      <StatTile
        label="账户"
        :value="formatNumber(o.totals.users)"
        :note="`30 天内登录 ${o.totals.active_users_30d}${o.totals.disabled_users ? ` · 已停用 ${o.totals.disabled_users}` : ''}`"
      />
      <StatTile label="观察中" :value="formatNumber(o.totals.open_watches)" :note="`累计 ${o.totals.watches} 个观察`" />
      <StatTile
        label="文章"
        :value="formatNumber(o.totals.articles)"
        :note="`在线 ${o.totals.articles_alive} · 已删除 ${o.totals.articles_deleted} · 休眠 ${o.totals.articles_dormant} · 无效 ${o.totals.articles_invalid}${o.totals.articles_pending ? ` · 解析中 ${o.totals.articles_pending}` : ''}`"
      />
      <StatTile label="备份" :value="formatNumber(o.totals.snapshots)" :note="`图片 ${formatNumber(o.totals.images)} 张`" />
      <StatTile
        label="对象存储"
        :value="formatBytes(o.totals.image_bytes + o.totals.snapshot_bytes)"
        :note="`图片 ${formatBytes(o.totals.image_bytes)} · 页面 ${formatBytes(o.totals.snapshot_bytes)}（不含原始页面与数据库备份）`"
      />
    </div>

    <h2>近 30 天</h2>
    <div class="charts">
      <BarChart v-for="s in series" :key="s.key" :title="s.title" :days="days" :values="values(s.key)" />
    </div>
    <details class="table-view">
      <summary>查看数据表</summary>
      <div class="table-wrap">
        <table class="small">
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
      </div>
    </details>
  </template>
</template>

<style scoped>
h2 {
  margin: 28px 0 12px;
  font-size: 1.05rem;
}
h2 .small {
  font-weight: normal;
}
.health {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
  align-items: center;
  padding: 10px 14px;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  background: var(--surface);
}
.health.ok strong {
  color: var(--ok);
}
.health.bad {
  border-color: var(--danger);
}
.health.bad strong {
  color: var(--danger);
}
.health .btn-link {
  margin-left: auto;
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 10px;
}
.charts {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 10px;
}
.table-view {
  margin-top: 14px;
}
.table-view summary {
  cursor: pointer;
  color: var(--accent);
}
.table-wrap {
  overflow-x: auto;
  margin-top: 8px;
}
table {
  border-collapse: collapse;
  width: 100%;
  background: var(--surface);
}
th,
td {
  padding: 4px 10px;
  border-bottom: 1px solid var(--border);
  text-align: right;
  white-space: nowrap;
}
th:first-child,
td:first-child {
  text-align: left;
}
th {
  color: var(--muted);
  font-weight: 600;
}
.num {
  font-variant-numeric: tabular-nums;
}
</style>
