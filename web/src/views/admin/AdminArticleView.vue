<script setup lang="ts">
import { ApiError, api, call, messageOf, type AdminArticleDetail, type AdminSnapshot } from '@/api/client'
import { articleStatusText, captureStateText, issueDetail, issueLabel } from '@/lib/admin'
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

// Rebuild a failed backup from its stored page (WeChat is not contacted).
const rebuilding = ref('')
async function rebuild(captureId: string) {
  rebuilding.value = captureId
  error.value = ''
  try {
    await call(api.POST('/api/v1/admin/captures/{id}/rebuild', { params: { path: { id: captureId } } }))
    notice.value = '已排队，从保存的原始页面重新生成备份。'
    let tries = 0
    const tick = async () => {
      await load()
      const c = d.value?.captures.find((x) => x.id === captureId)
      if (c && c.status !== 'pending') {
        notice.value = c.status === 'done' ? '备份已重新生成。' : '重新生成仍然失败，原因见下表。'
        rebuilding.value = ''
      } else if (++tries < 60) {
        poll = setTimeout(tick, 3000)
      } else {
        notice.value = '还在生成中，请稍后刷新。'
        rebuilding.value = ''
      }
    }
    poll = setTimeout(tick, 2000)
  } catch (e) {
    error.value = messageOf(e)
    rebuilding.value = ''
  }
}
</script>

<template>
    <RouterLink :to="{ name: 'admin-articles' }" class="zg-pagehead__back">← 文章管理</RouterLink>
    <p v-if="notFound" class="notice notice-info">没有这篇文章。</p>
    <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
    <p v-if="!d && !error && !notFound" class="muted" role="status">加载中…</p>
    <template v-if="d">
      <header class="zg-pagehead">
        <div>
          <h1 class="zg-pagehead__title zg-pagehead__title--article">{{ d.article.title || '（无标题）' }}</h1>
          <div class="zg-pagehead__meta">
            <span class="zg-shard zg-tag" :class="d.article.status === 'deleted' ? 'zg-tag--deleted' : 'zg-tag--watching'"><i class="zg-tag__glyph" />{{ articleStatusText(d.article.status) }}</span>
            <span>{{ [d.article.account_name, d.article.author].filter(Boolean).join(' · ') }}</span>
            <span v-if="d.article.published_at" class="zg-meta">发布于 {{ formatDate(d.article.published_at) }}</span>
          </div>
        </div>
        <button v-if="d.can_resnapshot" type="button" class="zg-shard zg-btn zg-btn--secondary" :disabled="resnapshotting"
          @click="resnapshot">
          <span v-if="resnapshotting" class="spinner" aria-hidden="true" /> 重新备份
        </button>
      </header>
      <div v-if="d.article.status === 'deleted'" class="notice deletion small">
        {{ deletedReasonText(d.article.deleted_reason) }}，发现于 {{ formatDateTime(d.article.deleted_at) }}
        <template v-if="d.article.deleted_message">。微信显示：「{{ d.article.deleted_message }}」</template>
      </div>
      <div v-if="d.issues.length" class="notice notice-warn small">
        <p v-for="i in d.issues" :key="i"><strong>{{ issueLabel(i) }}</strong>：{{ issueDetail(i) }}</p>
      </div>
      <div class="zg-detail">
        <div class="zg-stack">
          <section class="zg-shard zg-panel">
            <div class="row between">
              <h2 class="zg-panel__title">历次备份（{{ d.snapshots.length }}）</h2>

            </div>
            <p v-if="notice" class="notice notice-info small" role="status">{{ notice }}</p>
            <p v-if="!d.snapshots.length" class="muted">没有备份。</p>
            <div class="zg-tablewrap">
              <table v-if="d.snapshots.length" class="zg-table">
                <thead>
                  <tr>
                    <th>版本</th>
                    <th>时间</th>
                    <th class="num">图片</th>
                    <th class="num">缺失</th>
                    <th class="num">正文字数</th>
                    <th class="num">页面大小</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(s, i) in d.snapshots" :key="s.id">
                    <td class="is-mono">v{{ d.snapshots.length - i }}</td>
                    <td>{{ formatDateTime(s.captured_at) }}</td>
                    <td class="num">{{ s.images }}</td>
                    <td class="num" :class="{ warn: s.missing_images }">{{ s.missing_images }}</td>
                    <td class="num" :class="{ warn: s.text_runes != null && s.text_runes < 100 }">
                      {{ s.text_runes != null ? formatNumber(s.text_runes) : '—' }}</td>
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
          <section class="zg-shard zg-panel">
            <h2 class="zg-panel__title">检查记录（最近 {{ d.checks.length }} 次）</h2>
            <div class="zg-tablewrap">
              <table class="zg-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>方式</th>
                    <th>结果</th>
                    <th>规则 / 原因</th>
                    <th class="num">HTTP</th>
                    <th class="num">流量</th>
                    <th class="num">耗时</th>
                  </tr>
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
        </div>
        <aside class="zg-stack">
          <section class="zg-shard zg-panel">
            <h2 class="zg-panel__title">原文信息</h2>
            <dl class="zg-facts">
              <dt>首次提交</dt>
              <dd>{{ formatDateTime(d.first_seen_at) }}</dd>
              <dt>上次检查</dt>
              <dd>{{ d.article.last_checked_at ? formatRelative(d.article.last_checked_at) : '—' }}</dd>
              <dt>下次检查</dt>
              <dd>{{ d.article.next_check_at ? formatRelative(d.article.next_check_at) : '—' }}</dd>
              <dt>连续失败</dt>
              <dd>{{ d.consecutive_failures }} 次</dd>
              <dt>原文</dt>
              <dd><a v-if="d.article.url" :href="d.article.url" target="_blank" rel="noopener noreferrer"
                  class="url">{{ d.article.url }}</a><span v-else>—</span></dd>
            </dl>
          </section>
          <section v-if="d.captures.length" class="zg-shard zg-panel">
            <h2 class="zg-panel__title">抓取记录（{{ d.captures.length }}）</h2>
            <p class="muted small">每次为备份抓取的原始页面及其处理结果。失败的可以在修复原因后从原始页面重建，不需要再访问微信，文章已删除也可以。</p>
            <div class="zg-tablewrap">
              <table class="zg-table">
                <thead>
                  <tr>
                    <th>抓取时间</th>
                    <th>结果</th>
                    <th>详情</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="c in d.captures" :key="c.id">
                    <td>{{ formatDateTime(c.captured_at) }}</td>
                    <td :class="{ warn: c.status === 'failed' }">{{ captureStateText(c.status, c.error_code) }}</td>
                    <td class="muted detail" :title="c.error">
                      {{ c.error || (c.status === 'pending' ? `更新于 ${formatRelative(c.updated_at)}` : '—') }}</td>
                    <td>
                      <button v-if="c.status !== 'done'" type="button" class="btn-link" :disabled="!!rebuilding" @click="rebuild(c.id)">
                        <span v-if="rebuilding === c.id" class="spinner" aria-hidden="true" /> 重建
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
          <section class="zg-shard zg-panel">
            <h2 class="zg-panel__title">观察者（{{ d.watchers.length }}）</h2>
            <ul class="watchers small">
              <li v-for="w in d.watchers" :key="w.email + w.created_at">
                <strong>{{ w.email }}</strong>
                <span class="muted">{{ watchStates[w.status] ?? w.status }}<template
                  v-if="w.end_reason">（{{ endReasons[w.end_reason] ?? w.end_reason }}）</template> · 提交于 {{ formatDate(w.created_at) }}</span>
              </li>
            </ul>
          </section>
        </aside>
      </div>
    </template>
</template>
<style scoped>
.deletion { background: var(--status-deleted-soft); color: var(--ember-ink); }
.zg-table th, .zg-table td { white-space: nowrap; padding: var(--space-2); }
.zg-table .detail { white-space: normal; min-width: 8em; font-family: var(--font-mono); }
.between, .links { display: flex; gap: var(--space-2); justify-content: space-between; }
.preview { width: 100%; height: 360px; border: 1px solid var(--line-100); }
.watchers { list-style: none; padding: 0; margin: 0; }
.watchers li + li { border-top: 1px solid var(--line-100); padding-top: var(--space-2); }
.watchers span { display: block; }
.warn, .r-blocked, .r-error, .r-invalid { color: var(--status-partial); }
.r-deleted { color: var(--ember-ink); }
.r-alive { color: var(--status-archived); }
</style>
