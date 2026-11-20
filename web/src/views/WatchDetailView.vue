<script setup lang="ts">
import { ApiError, api, call, messageOf, type Check, type Snapshot, type WatchDetail } from '@/api/client'
import PrintHelp from '@/components/PrintHelp.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { inWeChat } from '@/lib/device'
import { formatDate, formatDateTime, formatShort } from '@/lib/format'
import { checkResultText, daysLeft, deletedReasonText, isOpen, renewedUntil, watchSummary } from '@/lib/status'
import { useSnapshotLinks } from '@/lib/snapshotLinks'
import { session } from '@/session'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const id = computed(() => String(route.params.id))

const detail = ref<WatchDetail | null>(null)
const loading = ref(true)
const error = ref('')
const notFound = ref(false)

const selected = ref<Snapshot | null>(null)
const { viewURL, linkError, download, downloading, retryView } = useSnapshotLinks(computed(() => selected.value?.id))
const showPrint = ref(false)
const confirmStop = ref(false)
// On phones a page-high frame inside a scrolling page is awkward: start
// collapsed there, and offer the new-window reader first.
const narrow = typeof matchMedia === 'function' && matchMedia('(max-width: 640px)').matches
const showPreview = ref(!narrow)
const stopping = ref(false)
const renewing = ref(false)
const renewed = ref('')

const w = computed(() => detail.value?.watch)
const article = computed(() => w.value?.article)
const deleted = computed(() => w.value?.end_reason === 'deleted' || article.value?.status === 'deleted')
const waitingForBackup = computed(
  () => !!w.value && (w.value.status === 'resolving' || detail.value?.backup_status === 'pending'),
)

let disposed = false
async function load() {
  const requested = id.value
  try {
    const d = await call(api.GET('/api/v1/watches/{id}', { params: { path: { id: id.value } } }))
    if (disposed || requested !== id.value) return
    detail.value = d
    error.value = ''
    if (!selected.value || !d.snapshots.some((s) => s.id === selected.value?.id)) {
      selected.value = d.snapshots[0] ?? null
    }
  } catch (e) {
    if (disposed || requested !== id.value) return
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = messageOf(e)
  } finally {
    if (!disposed && requested === id.value) loading.value = false
  }
}

// While the first backup is being made, poll: every 3 s for a minute, then
// every 10 s, for at most 10 minutes.
let poll: ReturnType<typeof setTimeout> | undefined
let polls = 0
function schedulePoll() {
  clearTimeout(poll)
  if (disposed || !waitingForBackup.value || notFound.value || polls >= 20 + 54) return
  poll = setTimeout(async () => {
    polls++
    if (document.visibilityState === 'visible') await load()
    schedulePoll()
  }, polls < 20 ? 3000 : 10_000)
}

watch(id, () => {
  detail.value = null
  selected.value = null
  error.value = ''
  renewed.value = ''
  confirmStop.value = false
  showPrint.value = false
  clearTimeout(poll)
  loading.value = true
  notFound.value = false
  polls = 0
  void load().then(schedulePoll)
}, { immediate: true })
watch(waitingForBackup, schedulePoll)
onBeforeUnmount(() => {
  clearTimeout(poll)
  disposed = true
})

const watchDays = computed(() => session.site?.watch_days ?? 60)
// Offered once renewing would add at least a day.
const canExtend = computed(() => !!w.value?.can_renew && w.value.status === 'active' && daysLeft(w.value) < watchDays.value)
const canWatchAgain = computed(() => !!w.value?.can_renew && w.value.status === 'ended')

async function renewWatch() {
  renewing.value = true
  renewed.value = ''
  error.value = ''
  const requested = id.value
  try {
    const again = w.value?.status === 'ended'
    const d = await call(api.POST('/api/v1/watches/{id}/renew', { params: { path: { id: id.value } } }))
    if (disposed || id.value !== requested) return
    detail.value = d
    renewed.value = `${again ? '已重新开始观察' : '已延长观察期'}，观察到 ${formatDate(d.watch.expires_at)}。`
  } catch (e) {
    if (!disposed && requested === id.value) error.value = messageOf(e)
  } finally {
    renewing.value = false
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
const elapsed = computed(() => {
  if (!w.value) return 0
  const start = Date.parse(w.value.created_at)
  const end = Date.parse(w.value.expires_at)
  const current = w.value.ended_at ? Date.parse(w.value.ended_at) : Date.now()
  return end > start ? Math.max(0, Math.min(100, 100 * (current - start) / (end - start))) : 0
})

const logGroups = computed(() => {
  const groups: Check[][] = []
  for (const check of detail.value?.checks ?? []) {
    const last = groups[groups.length - 1]
    if (check.result === 'alive' && last?.[0]?.result === 'alive') last.push(check)
    else groups.push([check])
  }
  return groups
})
</script>

<template>
  <header class="zg-pagehead">
    <div>
      <RouterLink to="/watches" class="zg-pagehead__back">← 我的观察</RouterLink>
      <h1 class="zg-pagehead__title zg-pagehead__title--article">{{ article?.title || (w?.status === 'resolving' ? '正在获取文章…' : '观察详情') }}</h1>
      <div v-if="w" class="zg-pagehead__meta">
        <StatusBadge :watch="w" />
        <span v-if="article?.account_name">公众号「{{ article.account_name }}」</span>
        <span class="zg-meta">提交于 {{ formatShort(w.created_at) }}</span>
      </div>
    </div>
    <div v-if="w" class="zg-pagehead__actions">
      <button v-if="canWatchAgain || canExtend" type="button" class="zg-shard zg-btn zg-btn--sm" :class="{ 'zg-btn--secondary': canExtend }"
        :disabled="renewing" :title="'观察到 ' + formatDate(renewedUntil(watchDays))" @click="renewWatch">{{ canWatchAgain ? '重新观察' : '延长观察' }}</button>
      <button v-if="isOpen(w)" type="button" class="zg-shard zg-btn zg-btn--sm zg-btn--danger" @click="confirmStop = true">停止观察</button>
    </div>
  </header>
  <p v-if="loading" class="muted"><span class="spinner" /> 加载中…</p>
  <div v-else-if="notFound" class="zg-shard zg-panel">
    <h2 class="zg-panel__title">没有找到这个观察</h2>
    <p>它可能已经不存在。同一篇文章的其他链接可能已合并到原来的观察。</p>
    <RouterLink to="/watches">回到我的观察</RouterLink>
  </div>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="load">重试</button></p>
  <p v-if="renewed" class="notice notice-ok" role="status">{{ renewed }}</p>
  <section v-if="confirmStop" class="zg-shard zg-panel confirm">
    <h2 class="zg-panel__title">确定停止观察？</h2>
    <p>停止后不再检查这篇文章，也不会再收到它的通知。已有的备份会保留。</p>
    <div class="row"><button class="zg-shard zg-btn zg-btn--danger" :disabled="stopping" @click="stop">确定停止</button><button class="zg-shard zg-btn zg-btn--secondary" @click="confirmStop = false">取消</button></div>
  </section>
  <template v-if="detail && w">
    <section v-if="deleted" class="zg-shard zg-notice">
      <span class="zg-notice__kicker">DELETED <template v-if="article?.deleted_at">· {{ formatShort(article.deleted_at) }}</template></span>
      <h2 class="zg-notice__title">这篇文章已被删除</h2>
      <p class="zg-notice__body">{{ deletedReasonText(article?.deleted_reason) }}。<template v-if="article?.deleted_message">微信显示「{{ article.deleted_message }}」。</template></p>
      <div v-if="selected" class="zg-notice__actions">
        <RouterLink :to="{ name: 'reader', params: { id, version: selected.id } }" class="zg-shard zg-btn">查看备份</RouterLink>
        <button class="zg-shard zg-btn zg-btn--secondary" :disabled="downloading" @click="download">下载 HTML</button>
      </div>
    </section>
    <div class="zg-detail">
      <div class="zg-stack">
        <section class="zg-shard zg-panel">
          <div class="zg-panel__head"><h2 class="zg-panel__title">备份预览</h2>
            <RouterLink v-if="selected" :to="{ name: 'reader', params: { id, version: selected.id } }" class="zg-link small" target="_blank" rel="noopener">在新窗口阅读 ↗</RouterLink>
          </div>
          <p v-if="waitingForBackup" class="muted"><span class="spinner" /> 正在备份，通常一两分钟内完成，页面会自动更新。</p>
          <div v-else-if="detail.backup_status === 'failed'" class="notice notice-warn">
            <p v-if="detail.backup_error === 'no_content'">这篇文章是图片或视频形式的消息，没有正文区域，暂时无法生成备份。原始页面已经保存，以后支持这种格式时可以补做备份。</p>
            <p v-else>生成备份时多次出错，暂时没有备份。原始页面已经保存，管理员处理后可以重新生成。</p>
            <p v-if="isOpen(w)">服务仍会照常检查文章是否被删除。</p>
          </div>
          <p v-else-if="!selected" class="muted">没有备份。</p>
          <template v-if="selected">
            <div class="zg-shard zg-provenance"><span class="zg-provenance__label">BACKUP</span><span class="zg-provenance__time">{{ formatShort(selected.captured_at) }}</span>
              <span>共 {{ selected.images }} 张图片</span><span v-if="selected.missing_images" class="partial">缺失 {{ selected.missing_images }} 张</span>
            </div>
            <iframe v-if="showPreview && viewURL" class="preview" :src="viewURL" title="文章备份" sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" />
            <div class="row">
              <button class="zg-shard zg-btn zg-btn--sm zg-btn--secondary" :aria-expanded="showPreview" @click="showPreview = !showPreview">{{ showPreview ? '收起预览' : '在页面内预览' }}</button>
              <button class="zg-shard zg-btn zg-btn--sm zg-btn--secondary" :disabled="downloading" @click="download">下载 HTML</button>
              <button class="zg-shard zg-btn zg-btn--sm zg-btn--secondary" :aria-expanded="showPrint" @click="showPrint = !showPrint">打印 / 保存为 PDF</button>
            </div>
            <p v-if="inWeChat" class="notice notice-warn small">微信内置浏览器不能下载文件或打印。请点右上角「···」，选择「在浏览器打开」。</p>
            <PrintHelp v-if="showPrint && viewURL" :url="viewURL" />
            <p v-if="linkError" role="alert" class="notice notice-danger">{{ linkError }} <button class="btn-link" @click="retryView">重新获取链接</button></p>
          </template>
        </section>
        <section class="zg-shard zg-panel">
          <div class="zg-panel__head"><h2 class="zg-panel__title">检查记录</h2><span class="zg-meta">最近 {{ detail.checks.length }} 次</span></div>
          <p v-if="!logGroups.length" class="muted small">暂无检查记录。</p>
          <ol v-else class="zg-log">
            <li v-for="group in logGroups" :key="group[0]!.checked_at" class="zg-log__item" :class="{ 'is-deleted': group[0]!.result === 'deleted', 'is-unreachable': group[0]!.result === 'blocked' }">
              <time class="zg-log__time" :datetime="group[0]!.checked_at">{{ formatShort(group[0]!.checked_at) }}</time>
              <details v-if="group.length > 2" class="zg-log__text">
                <summary>连续 {{ group.length }} 次检查均在线</summary>
                <div v-for="c in group" :key="c.checked_at" class="zg-log__note">{{ formatDateTime(c.checked_at) }} · 正常</div>
              </details>
              <span v-else class="zg-log__text">{{ checkResultText(group[0]!) }}<span v-if="group.length === 2" class="zg-log__note">{{ formatShort(group[1]!.checked_at) }} · 正常</span></span>
            </li>
          </ol>
        </section>
      </div>
      <aside class="zg-stack">
        <section class="zg-shard zg-panel">
          <h2 class="zg-panel__title">观察期限</h2>
          <div class="zg-meter" :class="{ 'zg-meter--deleted': deleted }" :style="{ '--value': elapsed + '%' }">
            <div class="zg-meter__row"><span>{{ isOpen(w) ? '观察中' : '已结束' }}</span><span v-if="isOpen(w)" class="zg-meter__value">还剩 {{ daysLeft(w) }} 天</span></div>
            <div class="zg-meter__track"><div class="zg-meter__fill" /></div>
          </div>
          <dl class="zg-facts"><dt>开始</dt><dd class="is-mono">{{ formatDateTime(w.created_at) }}</dd><dt>到期</dt><dd class="is-mono">{{ formatDateTime(w.expires_at) }}</dd>
            <template v-if="w.ended_at"><dt>结束</dt><dd class="is-mono">{{ formatDateTime(w.ended_at) }}</dd></template>
          </dl>
          <p v-if="!deleted" class="zg-field__hint">{{ watchSummary(w) }}</p>
          <p v-if="canExtend || canWatchAgain" class="zg-field__hint">从今天起可再观察 {{ watchDays }} 天，到 {{ formatDate(renewedUntil(watchDays)) }}。</p>
        </section>
        <section v-if="detail.snapshots.length" class="zg-shard zg-panel">
          <div class="zg-panel__head"><h2 class="zg-panel__title">备份版本</h2><span class="zg-meta">{{ detail.snapshots.length }} 份</span></div>
          <div class="zg-shard zg-versions" role="radiogroup" aria-label="选择备份">
            <label v-for="(s, i) in detail.snapshots" :key="s.id" class="zg-version">
              <input v-model="selected" type="radio" name="snapshot" :value="s">
              <span class="zg-version__radio" />
              <span class="zg-version__main"><span class="zg-version__name">v{{ detail.snapshots.length - i }}</span><span class="zg-version__meta">{{ formatShort(s.captured_at) }}</span></span>
              <span class="zg-shard zg-tag" :class="s.missing_images ? 'zg-tag--partial' : 'zg-tag--archived'"><i class="zg-tag__glyph" />{{ s.missing_images ? '图片缺失' : '已备份' }}</span>
            </label>
          </div>
        </section>
        <section class="zg-shard zg-panel"><h2 class="zg-panel__title">原文信息</h2>
          <dl class="zg-facts">
            <template v-if="article?.account_name"><dt>公众号</dt><dd>{{ article.account_name }}</dd></template>
            <template v-if="article?.author"><dt>作者</dt><dd>{{ article.author }}</dd></template>
            <template v-if="article?.published_at"><dt>发布于</dt><dd>{{ formatDate(article.published_at) }}</dd></template>
            <template v-if="article?.last_checked_at"><dt>上次检查</dt><dd class="is-mono">{{ formatShort(article.last_checked_at) }}</dd></template>
            <template v-if="w.status === 'active' && article?.next_check_at"><dt>下次检查</dt><dd class="is-mono">{{ formatShort(article.next_check_at) }}</dd></template>
            <dt>原文</dt><dd><a :href="article?.url || w.submitted_url" target="_blank" rel="noopener noreferrer">打开原文 ↗</a></dd>
          </dl>
        </section>
      </aside>
    </div>
  </template>
</template>
<style scoped>
.zg-notice, .confirm { max-width: none; margin-bottom: var(--space-5); }
.preview { width: 100%; height: 360px; border: 1px solid var(--line-100); }
.partial { color: var(--status-partial); }
</style>
