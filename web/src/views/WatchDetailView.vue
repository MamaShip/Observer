<script setup lang="ts">
import { ApiError, api, call, messageOf, type Snapshot, type WatchDetail } from '@/api/client'
import PrintHelp from '@/components/PrintHelp.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { inWeChat } from '@/lib/device'
import { formatDate, formatDateTime, formatRelative } from '@/lib/format'
import { checkResultText, deletedReasonText, isOpen, watchSummary } from '@/lib/status'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const id = computed(() => String(route.params.id))

const detail = ref<WatchDetail | null>(null)
const loading = ref(true)
const error = ref('')
const notFound = ref(false)

const selected = ref<Snapshot | null>(null)
const view = ref<{ url: string; expiresAt: number; snapshotId: string } | null>(null)
const viewError = ref('')
const downloading = ref(false)
const showPrint = ref(false)
const confirmStop = ref(false)
// On phones a page-high frame inside a scrolling page is awkward: start
// collapsed there, and offer the new-window reader first.
const narrow = typeof matchMedia === 'function' && matchMedia('(max-width: 640px)').matches
const showPreview = ref(!narrow)
const stopping = ref(false)

const w = computed(() => detail.value?.watch)
const article = computed(() => w.value?.article)
const deleted = computed(() => w.value?.end_reason === 'deleted' || article.value?.status === 'deleted')
const waitingForBackup = computed(
  () => !!w.value && (w.value.status === 'resolving' || (isOpen(w.value) && !detail.value?.snapshots.length)),
)

async function load() {
  try {
    const d = await call(api.GET('/api/v1/watches/{id}', { params: { path: { id: id.value } } }))
    detail.value = d
    error.value = ''
    if (!selected.value || !d.snapshots.some((s) => s.id === selected.value?.id)) {
      selected.value = d.snapshots[0] ?? null
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = messageOf(e)
  } finally {
    loading.value = false
  }
}

// While the first backup is being made, poll: every 3 s for a minute, then
// every 10 s, for at most 10 minutes.
let poll: ReturnType<typeof setTimeout> | undefined
let polls = 0
function schedulePoll() {
  clearTimeout(poll)
  if (!waitingForBackup.value || notFound.value || polls >= 20 + 54) return
  poll = setTimeout(async () => {
    polls++
    if (document.visibilityState === 'visible') await load()
    schedulePoll()
  }, polls < 20 ? 3000 : 10_000)
}

// Signed snapshot links last an hour; renew one before it lapses.
let renew: ReturnType<typeof setTimeout> | undefined
async function fetchView(s: Snapshot) {
  viewError.value = ''
  clearTimeout(renew)
  try {
    const r = await call(api.POST('/api/v1/snapshots/{id}/view-url', { params: { path: { id: s.id } } }))
    const expiresAt = new Date(r.expires_at).getTime()
    view.value = { url: r.url, expiresAt, snapshotId: s.id }
    renew = setTimeout(() => void fetchView(s), Math.max(60_000, expiresAt - Date.now() - 120_000))
  } catch (e) {
    view.value = null
    viewError.value = messageOf(e)
  }
}

watch(selected, (s) => {
  if (s && view.value?.snapshotId !== s.id) void fetchView(s)
})
watch(id, () => {
  detail.value = null
  selected.value = null
  view.value = null
  loading.value = true
  notFound.value = false
  polls = 0
  void load().then(schedulePoll)
}, { immediate: true })
watch(waitingForBackup, schedulePoll)
onBeforeUnmount(() => {
  clearTimeout(poll)
  clearTimeout(renew)
})

async function download() {
  if (!selected.value) return
  downloading.value = true
  viewError.value = ''
  try {
    const r = await call(api.POST('/api/v1/snapshots/{id}/download-url', { params: { path: { id: selected.value.id } } }))
    location.href = r.url // served as an attachment: the page stays
  } catch (e) {
    viewError.value = messageOf(e)
  } finally {
    downloading.value = false
  }
}

async function stop() {
  stopping.value = true
  try {
    await call(api.DELETE('/api/v1/watches/{id}', { params: { path: { id: id.value } } }))
    confirmStop.value = false
    await load()
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    stopping.value = false
  }
}
</script>

<template>
  <p class="back"><RouterLink to="/watches">← 我的观察</RouterLink></p>

  <p v-if="loading" class="muted center"><span class="spinner" /> 加载中…</p>

  <div v-else-if="notFound" class="card stack">
    <h1>没有找到这个观察</h1>
    <p class="muted">
      它可能已经不存在。如果你刚刚提交了一篇已经在观察中的文章（例如同一篇文章的另一种链接），它会合并到原来的观察里。
    </p>
    <RouterLink to="/watches" class="btn">回到我的观察</RouterLink>
  </div>

  <p v-else-if="error && !detail" class="notice notice-danger" role="alert">
    {{ error }} <button type="button" class="btn-link" @click="load">重试</button>
  </p>

  <template v-else-if="detail && w">
    <article class="card stack">
      <div class="title-row">
        <h1>{{ article?.title || (w.status === 'resolving' ? '正在获取文章…' : '（无标题）') }}</h1>
        <StatusBadge :watch="w" />
      </div>
      <p v-if="article?.account_name || article?.author" class="muted byline">
        {{ [article?.account_name, article?.author].filter(Boolean).join(' · ') }}
        <template v-if="article?.published_at"> · 发布于 {{ formatDate(article.published_at) }}</template>
      </p>

      <div v-if="deleted" class="notice notice-danger">
        <p>
          <strong>文章已被删除：{{ deletedReasonText(article?.deleted_reason) }}</strong>
          <template v-if="article?.deleted_at">（发现于 {{ formatDateTime(article.deleted_at) }}）</template>
        </p>
        <p v-if="article?.deleted_message" class="quote">微信显示：「{{ article.deleted_message }}」</p>
      </div>
      <p v-else class="muted">{{ watchSummary(w) }}</p>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>

      <dl class="facts small">
        <div>
          <dt>提交时间</dt>
          <dd>{{ formatDateTime(w.created_at) }}</dd>
        </div>
        <div v-if="isOpen(w)">
          <dt>观察到</dt>
          <dd>{{ formatDate(w.expires_at) }}</dd>
        </div>
        <div v-else-if="w.ended_at">
          <dt>结束时间</dt>
          <dd>{{ formatDateTime(w.ended_at) }}</dd>
        </div>
        <div v-if="article?.last_checked_at">
          <dt>上次检查</dt>
          <dd>{{ formatRelative(article.last_checked_at) }}</dd>
        </div>
        <div v-if="w.status === 'active' && article?.next_check_at">
          <dt>下次检查</dt>
          <dd>{{ formatRelative(article.next_check_at) }}</dd>
        </div>
        <div class="wide">
          <dt>原文链接</dt>
          <dd><a :href="article?.url || w.submitted_url" target="_blank" rel="noopener noreferrer" class="url">{{ article?.url || w.submitted_url }}</a></dd>
        </div>
      </dl>
    </article>

    <section class="card stack">
      <h2>备份</h2>
      <p v-if="waitingForBackup" class="muted"><span class="spinner" /> 正在备份，通常一两分钟内完成，页面会自动更新。</p>
      <p v-else-if="!detail.snapshots.length" class="muted">没有备份。</p>

      <template v-if="selected">
        <div v-if="detail.snapshots.length > 1" class="row" role="tablist" aria-label="选择备份">
          <button
            v-for="s in detail.snapshots"
            :key="s.id"
            type="button"
            role="tab"
            class="chip"
            :aria-selected="selected.id === s.id"
            @click="selected = s"
          >
            {{ formatDateTime(s.captured_at) }}
          </button>
        </div>
        <p class="muted small">
          备份于 {{ formatDateTime(selected.captured_at) }}，共 {{ selected.images }} 张图片<template v-if="selected.missing_images">，其中 {{ selected.missing_images }} 张没能保存</template>。
        </p>

        <p v-if="inWeChat" class="notice notice-warn small">
          微信内置浏览器不能下载文件或打印。需要时请点右上角「···」，选择「在浏览器打开」。
        </p>

        <div class="row">
          <a v-if="view" :href="view.url" target="_blank" rel="noopener" class="btn">在新窗口阅读</a>
          <button type="button" class="btn" :disabled="downloading" @click="download">
            <span v-if="downloading" class="spinner" aria-hidden="true" />
            下载 HTML
          </button>
          <button type="button" class="btn" :aria-expanded="showPrint" @click="showPrint = !showPrint">打印 / 保存为 PDF</button>
        </div>
        <PrintHelp v-if="showPrint && view" :url="view.url" />
        <p v-if="viewError" class="notice notice-danger" role="alert">{{ viewError }}</p>

        <button v-if="view" type="button" class="btn-link small" :aria-expanded="showPreview" @click="showPreview = !showPreview">
          {{ showPreview ? '收起预览' : '在页面内预览' }}
        </button>
        <iframe
          v-if="view && showPreview"
          :key="view.snapshotId"
          class="preview"
          :src="view.url"
          title="文章备份"
          sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
          referrerpolicy="no-referrer"
        />
      </template>
    </section>

    <section v-if="detail.checks.length" class="card">
      <details>
        <summary><h2 class="inline">检查记录</h2> <span class="muted small">最近 {{ detail.checks.length }} 次</span></summary>
        <table class="checks small">
          <thead>
            <tr><th>时间</th><th>结果</th><th>说明</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in detail.checks" :key="c.checked_at">
              <td>{{ formatDateTime(c.checked_at) }}</td>
              <td :class="`r-${c.result}`">{{ checkResultText(c) }}</td>
              <td class="muted">{{ c.reason }}</td>
            </tr>
          </tbody>
        </table>
      </details>
    </section>

    <section v-if="isOpen(w)" class="section stop">
      <button v-if="!confirmStop" type="button" class="btn btn-danger" @click="confirmStop = true">停止观察</button>
      <div v-else class="card stack">
        <p>停止后不再检查这篇文章是否被删除，也不会再收到它的通知。已有的备份会保留。</p>
        <div class="row">
          <button type="button" class="btn btn-danger solid" :disabled="stopping" @click="stop">确定停止</button>
          <button type="button" class="btn" @click="confirmStop = false">取消</button>
        </div>
      </div>
    </section>
  </template>
</template>

<style scoped>
.back {
  margin: 0 0 12px;
  font-size: 0.9rem;
}
.title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.title-row h1 {
  font-size: 1.3rem;
  margin: 0;
}
.title-row .badge {
  margin-top: 4px;
}
.byline {
  margin-top: -4px;
}
.quote {
  margin: 0;
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
.chip {
  font: inherit;
  font-size: 0.85rem;
  padding: 2px 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  cursor: pointer;
}
.chip[aria-selected='true'] {
  border-color: var(--accent);
  color: var(--accent);
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
summary {
  cursor: pointer;
}
.inline {
  display: inline;
  margin: 0;
}
.checks {
  width: 100%;
  border-collapse: collapse;
  margin-top: 12px;
}
.checks th,
.checks td {
  text-align: left;
  padding: 6px 8px 6px 0;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}
.checks th {
  color: var(--muted);
  font-weight: 600;
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
.stop {
  margin-top: 24px;
}
</style>
