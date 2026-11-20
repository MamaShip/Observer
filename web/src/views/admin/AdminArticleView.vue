<script setup lang="ts">
import { ApiError, api, call, messageOf, type AdminArticleDetail, type AdminSnapshot } from '@/api/client'
import AdminNav from '@/components/admin/AdminNav.vue'
import { articleStatusText, issueDetail, issueLabel } from '@/lib/admin'
import { formatBytes, formatDate, formatDateTime, formatNumber, formatRelative } from '@/lib/format'
import { deletedReasonText } from '@/lib/status'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const id = computed(() => String(route.params.id))
const d = ref<AdminArticleDetail | null>(null)
const error = ref('')
const notFound = ref(false)
const viewing = ref<{ id: string; url: string } | null>(null)
const resnapshotting = ref(false)
const notice = ref('')

const checkResults: Record<string, string> = { alive: '正常', deleted: '已删除', invalid: '无效', blocked: '被拦截', error: '出错' }
const watchStates: Record<string, string> = { resolving: '备份中', active: '观察中', ended: '已结束', failed: '失败' }
const endReasons: Record<string, string> = {
  deleted: '文章被删', expired: '到期', user_stopped: '主动停止', invalid: '链接无效', unreachable: '无法访问',
}

async function load() {
  try {
    d.value = await call(api.GET('/api/v1/admin/articles/{id}', { params: { path: { id: id.value } } }))
    error.value = ''
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = messageOf(e)
  }
}
watch(id, () => {
  d.value = null
  viewing.value = null
  notFound.value = false
  void load()
}, { immediate: true })

async function view(s: AdminSnapshot) {
  if (viewing.value?.id === s.id) {
    viewing.value = null
    return
  }
  try {
    const r = await call(api.POST('/api/v1/snapshots/{id}/view-url', { params: { path: { id: s.id } } }))
    viewing.value = { id: s.id, url: r.url }
  } catch (e) {
    error.value = messageOf(e)
  }
}

async function download(s: AdminSnapshot) {
  try {
    const r = await call(api.POST('/api/v1/snapshots/{id}/download-url', { params: { path: { id: s.id } } }))
    location.href = r.url
  } catch (e) {
    error.value = messageOf(e)
  }
}

// After asking for a new backup, poll until it appears (fetching waits for
// the global rate limit, then images are downloaded).
let poll: ReturnType<typeof setTimeout> | undefined
async function resnapshot() {
  resnapshotting.value = true
  error.value = ''
  try {
    await call(api.POST('/api/v1/admin/articles/{id}/resnapshot', { params: { path: { id: id.value } } }))
    const before = d.value?.snapshots.length ?? 0
    notice.value = '已排队重新抓取，新备份生成后会出现在下面的列表里。'
    let tries = 0
    const tick = async () => {
      await load()
      if ((d.value?.snapshots.length ?? 0) > before) {
        notice.value = '新备份已生成。'
        resnapshotting.value = false
      } else if (++tries < 60) {
        poll = setTimeout(tick, 5000)
      } else {
        notice.value = '新备份还没生成，可能被限速或抓取失败，请稍后刷新，或查看下面的检查记录。'
        resnapshotting.value = false
      }
    }
    poll = setTimeout(tick, 3000)
  } catch (e) {
    error.value = messageOf(e)
    resnapshotting.value = false
  }
}
onBeforeUnmount(() => clearTimeout(poll))
</script>

<template>
  <AdminNav />
  <p class="back"><RouterLink :to="{ name: 'admin-articles' }">← 文章列表</RouterLink></p>

  <div v-if="notFound" class="card">没有这篇文章。</div>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>

  <template v-if="d">
    <section class="card stack">
      <div class="head">
        <h1>{{ d.article.title || '（无标题）' }}</h1>
        <span class="status">{{ articleStatusText(d.article.status) }}</span>
      </div>
      <p class="muted">
        {{ [d.article.account_name, d.article.author].filter(Boolean).join(' · ') || '—' }}
        <template v-if="d.article.published_at"> · 发布于 {{ formatDate(d.article.published_at) }}</template>
      </p>
      <div v-if="d.article.status === 'deleted'" class="notice notice-danger small">
        {{ deletedReasonText(d.article.deleted_reason) }}，发现于 {{ formatDateTime(d.article.deleted_at) }}
        <template v-if="d.article.deleted_message">。微信显示：「{{ d.article.deleted_message }}」</template>
      </div>
      <div v-if="d.issues.length" class="notice notice-warn small">
        <p v-for="i in d.issues" :key="i"><strong>{{ issueLabel(i) }}</strong>：{{ issueDetail(i) }}</p>
      </div>
      <dl class="facts small">
        <div><dt>首次提交</dt><dd>{{ formatDateTime(d.first_seen_at) }}</dd></div>
        <div><dt>上次检查</dt><dd>{{ d.article.last_checked_at ? formatRelative(d.article.last_checked_at) : '—' }}</dd></div>
        <div><dt>下次检查</dt><dd>{{ d.article.next_check_at ? formatRelative(d.article.next_check_at) : '—' }}</dd></div>
        <div><dt>连续失败</dt><dd>{{ d.consecutive_failures }} 次</dd></div>
        <div class="wide"><dt>原文</dt><dd><a v-if="d.article.url" :href="d.article.url" target="_blank" rel="noopener noreferrer" class="url">{{ d.article.url }}</a><span v-else>—</span></dd></div>
      </dl>
    </section>

    <section class="card stack">
      <div class="row between">
        <h2>备份（{{ d.snapshots.length }}）</h2>
        <button v-if="d.can_resnapshot" type="button" class="btn" :disabled="resnapshotting" @click="resnapshot">
          <span v-if="resnapshotting" class="spinner" aria-hidden="true" /> 重新备份
        </button>
      </div>
      <p v-if="notice" class="notice notice-info small" role="status">{{ notice }}</p>
      <p v-if="!d.snapshots.length" class="muted">没有备份。</p>
      <div class="table-wrap">
        <table v-if="d.snapshots.length" class="small">
          <thead>
            <tr><th>时间</th><th class="num">图片</th><th class="num">缺失</th><th class="num">正文字数</th><th class="num">页面大小</th><th /></tr>
          </thead>
          <tbody>
            <tr v-for="s in d.snapshots" :key="s.id">
              <td>{{ formatDateTime(s.captured_at) }}</td>
              <td class="num">{{ s.images }}</td>
              <td class="num" :class="{ warn: s.missing_images }">{{ s.missing_images }}</td>
              <td class="num" :class="{ warn: s.text_runes != null && s.text_runes < 100 }">{{ s.text_runes != null ? formatNumber(s.text_runes) : '—' }}</td>
              <td class="num">{{ s.size_bytes ? formatBytes(s.size_bytes) : '—' }}</td>
              <td>
                <div class="links">
                  <button type="button" class="btn-link" @click="view(s)">{{ viewing?.id === s.id ? '收起' : '预览' }}</button>
                  <button type="button" class="btn-link" @click="download(s)">下载</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <template v-if="viewing">
        <p class="small"><a :href="viewing.url" target="_blank" rel="noopener">在新窗口打开</a></p>
        <iframe :key="viewing.id" class="preview" :src="viewing.url" title="备份预览"
          sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" />
      </template>
    </section>

    <section class="card stack">
      <h2>观察者（{{ d.watchers.length }}）</h2>
      <ul class="watchers small">
        <li v-for="w in d.watchers" :key="w.email + w.created_at">
          <strong>{{ w.email }}</strong>
          <span class="muted">{{ watchStates[w.status] ?? w.status }}<template v-if="w.end_reason">（{{ endReasons[w.end_reason] ?? w.end_reason }}）</template> · 提交于 {{ formatDate(w.created_at) }}</span>
        </li>
      </ul>
    </section>

    <section class="card stack">
      <h2>检查记录（最近 {{ d.checks.length }} 次）</h2>
      <div class="table-wrap">
        <table class="small">
          <thead>
            <tr><th>时间</th><th>方式</th><th>结果</th><th>规则 / 原因</th><th class="num">HTTP</th><th class="num">流量</th><th class="num">耗时</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in d.checks" :key="c.checked_at">
              <td>{{ formatDateTime(c.checked_at) }}</td>
              <td>{{ c.mode }}</td>
              <td :class="`r-${c.result}`">{{ checkResults[c.result] ?? c.result }}</td>
              <td class="muted">{{ [c.rule, c.reason].filter(Boolean).join(' / ') || '—' }}</td>
              <td class="num">{{ c.http_status ?? '—' }}</td>
              <td class="num">{{ c.wire_bytes != null ? formatBytes(c.wire_bytes) : '—' }}</td>
              <td class="num">{{ c.duration_ms != null ? `${c.duration_ms} ms` : '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </template>
</template>

<style scoped>
.back {
  margin: 0 0 12px;
  font-size: 0.9rem;
}
.head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}
.head h1 {
  font-size: 1.25rem;
  margin: 0;
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
h2 {
  margin: 0;
  font-size: 1.05rem;
}
.between {
  justify-content: space-between;
}
.facts {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px 16px;
  margin: 0;
}
.facts .wide {
  grid-column: 1 / -1;
}
.facts dt {
  color: var(--muted);
}
.facts dd {
  margin: 0;
}
.url {
  word-break: break-all;
}
.table-wrap {
  overflow-x: auto;
}
table {
  border-collapse: collapse;
  width: 100%;
}
th,
td {
  padding: 6px 10px 6px 0;
  border-bottom: 1px solid var(--border);
  text-align: left;
  white-space: nowrap;
}
th {
  color: var(--muted);
  font-weight: 600;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.warn {
  color: var(--warn);
  font-weight: 600;
}
.links {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
}
.preview {
  display: block;
  width: 100%;
  height: 75vh;
  min-height: 420px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: #fff;
}
.watchers {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 4px;
}
.watchers li {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
}
.r-alive {
  color: var(--ok);
}
.r-deleted {
  color: var(--danger);
}
.r-blocked,
.r-error,
.r-invalid {
  color: var(--warn);
}
</style>
