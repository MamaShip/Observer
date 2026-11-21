<script setup lang="ts">
import { ApiError, api, call, messageOf, type Check, type Snapshot, type WatchDetail } from '@/api/client'
import PrintHelp from '@/components/PrintHelp.vue'
import ZgIcon from '@/components/ZgIcon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { inWeChat } from '@/lib/device'
import { formatDate, formatDateTime, formatShort } from '@/lib/format'
import { checkResultText, daysLeft, deletedReasonText, endingSoonDays, isOpen, renewedUntil, watchSummary } from '@/lib/status'
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
const dayMs = 24 * 60 * 60 * 1000
const deletedAt = computed(() => article.value?.deleted_at ?? (deleted.value ? w.value?.ended_at : undefined))
// What the page said, in WeChat's words when we have them.
const pageReason = computed(() => {
  const a = article.value
  if (a?.deleted_message) return a.deleted_message
  return a?.deleted_reason && a.deleted_reason !== 'unknown' ? deletedReasonText(a.deleted_reason) : ''
})

const noticeText = computed(() => {
  const at = deletedAt.value ? `原文在 ${formatShort(deletedAt.value)} 的检查中确认已删除` : '原文已确认删除'
  const reason = pageReason.value ? `，页面显示「${pageReason.value}」` : ''
  return `${at}${reason}。${selected.value ? '你提交时的版本还在，随时可以查看。' : ''}`
})

const meter = computed(() => {
  const x = w.value
  if (!x) return null
  const start = Date.parse(x.created_at)
  const total = Math.max(1, Math.round((Date.parse(x.expires_at) - start) / dayMs))
  const end = deletedAt.value ?? x.ended_at
  const at = end ? Date.parse(end) : Date.now()
  const days = Math.min(total, Math.max(0, Math.floor((at - start) / dayMs)))
  const value = `${Math.min(100, Math.max(0, 100 * (at - start) / (total * dayMs)))}%`
  if (deleted.value) {
    return { kind: 'deleted', value, label: `观察第 ${Math.max(1, Math.ceil((at - start) / dayMs))} 天时删除`, figure: `${days} / ${total} 天` }
  }
  if (isOpen(x)) {
    const left = daysLeft(x)
    return { kind: left <= endingSoonDays ? 'expiring' : '', value, label: `已观察 ${days} 天`, figure: `还剩 ${left} 天` }
  }
  const label = x.end_reason === 'user_stopped' ? '已停止观察' : x.status === 'failed' ? '没能开始观察' : '观察期已结束'
  return { kind: 'ended', value: x.end_reason === 'expired' ? '100%' : value, label, figure: `${days} / ${total} 天` }
})

const selectedIndex = computed(() => detail.value?.snapshots.findIndex(s => s.id === selected.value?.id) ?? -1)
const versionName = (i: number) => `v${(detail.value?.snapshots.length ?? 0) - i}`

type LogRow =
  | { key: string; kind: 'check'; at: string; check: Check }
  | { key: string; kind: 'backup'; at: string; text: string }
  | { key: string; kind: 'start'; at: string }
  | { key: string; kind: 'more'; at: string; checks: Check[] }
// Checks, backups and the start in one timeline, newest first. Long runs of
// "在线" keep their newest and oldest check; the rest fold into one line.
const logRows = computed<LogRow[]>(() => {
  const d = detail.value
  if (!d) return []
  const snaps = d.snapshots
  const events: LogRow[] = [
    ...d.checks.map(c => ({ key: 'c' + c.checked_at, kind: 'check' as const, at: c.checked_at, check: c })),
    ...snaps.map((s, i) => ({
      key: 's' + s.id, kind: 'backup' as const, at: s.captured_at,
      text: i === snaps.length - 1 ? `已保存初次备份 ${versionName(i)}` : `已另存版本 ${versionName(i)}`,
    })),
    { key: 'start', kind: 'start', at: d.watch.created_at },
  ]
  events.sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
  const rows: LogRow[] = []
  let run: Check[] = []
  const flush = () => {
    if (run.length >= 3) {
      rows.push({ key: 'c' + run[0]!.checked_at, kind: 'check', at: run[0]!.checked_at, check: run[0]! })
      const middle = run.slice(1, -1)
      rows.push({ key: 'm' + middle[0]!.checked_at, kind: 'more', at: middle[0]!.checked_at, checks: middle })
      const last = run[run.length - 1]!
      rows.push({ key: 'c' + last.checked_at, kind: 'check', at: last.checked_at, check: last })
    } else {
      for (const c of run) rows.push({ key: 'c' + c.checked_at, kind: 'check', at: c.checked_at, check: c })
    }
    run = []
  }
  for (const e of events) {
    if (e.kind === 'check' && e.check.result === 'alive') {
      run.push(e.check)
      continue
    }
    flush()
    rows.push(e)
  }
  flush()
  return rows
})
</script>

<template>
  <header class="zg-pagehead">
    <div>
      <RouterLink to="/watches" class="zg-pagehead__back"><ZgIcon name="arrow-left" />我的观察</RouterLink>
      <h1 class="zg-pagehead__title zg-pagehead__title--article">{{ article?.title || (w?.status === 'resolving' ? '正在获取文章…' : '观察详情') }}</h1>
      <div v-if="w" class="zg-pagehead__meta">
        <StatusBadge :watch="w" />
        <span v-if="article?.account_name">公众号「{{ article.account_name }}」</span>
        <span class="zg-meta">提交于 {{ formatShort(w.created_at) }}</span>
      </div>
    </div>
    <div v-if="w" class="zg-pagehead__actions">
      <button v-if="canWatchAgain || canExtend" type="button" class="zg-shard zg-btn" :class="{ 'zg-btn--secondary': canExtend }"
        :disabled="renewing" :title="'观察到 ' + formatDate(renewedUntil(watchDays))" @click="renewWatch">{{ canWatchAgain ? '重新开始观察' : '延长观察' }}</button>
      <button v-if="isOpen(w)" type="button" class="zg-shard zg-btn zg-btn--danger" @click="confirmStop = true">停止观察</button>
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
    <section v-if="deleted" class="zg-shard zg-notice" aria-labelledby="notice-title">
      <span class="zg-notice__kicker">DELETED<template v-if="deletedAt"> · {{ formatShort(deletedAt) }}</template></span>
      <h2 id="notice-title" class="zg-notice__title">这篇文章已被删除</h2>
      <p class="zg-notice__body">{{ noticeText }}</p>
      <div v-if="selected" class="zg-notice__actions">
        <RouterLink :to="{ name: 'reader', params: { id, version: selected.id } }" class="zg-shard zg-btn">查看备份</RouterLink>
        <button type="button" class="zg-shard zg-btn zg-btn--secondary" :disabled="downloading" @click="download">下载离线 HTML</button>
      </div>
    </section>
    <div class="zg-detail">
      <div class="zg-stack">
        <section class="zg-shard zg-panel" aria-labelledby="preview-title">
          <div class="zg-panel__head"><h2 id="preview-title" class="zg-panel__title">备份预览</h2>
            <RouterLink v-if="selected" :to="{ name: 'reader', params: { id, version: selected.id } }" class="zg-link open-link" target="_blank" rel="noopener">独立打开<ZgIcon name="external-link" /></RouterLink>
          </div>
          <p v-if="waitingForBackup" class="muted"><span class="spinner" /> 正在备份，通常一两分钟内完成，页面会自动更新。</p>
          <div v-else-if="detail.backup_status === 'failed'" class="notice notice-warn">
            <p v-if="detail.backup_error === 'no_content'">这篇文章是图片或视频形式的消息，没有正文区域，暂时无法生成备份。原始页面已经保存，以后支持这种格式时可以补做备份。</p>
            <p v-else>生成备份时多次出错，暂时没有备份。原始页面已经保存，管理员处理后可以重新生成。</p>
            <p v-if="isOpen(w)">我们仍会照常检查文章是否被删除。</p>
          </div>
          <p v-else-if="!selected" class="muted">没有备份。</p>
          <template v-if="selected">
            <div class="frame">
              <div class="zg-shard zg-provenance" :class="{ 'is-deleted': deleted }">
                <span class="zg-provenance__label">BACKUP</span><span class="zg-provenance__time">{{ formatDateTime(selected.captured_at) }}</span>
                <span v-if="article?.account_name">来源「{{ article.account_name }}」</span>
                <span v-if="deleted" class="zg-provenance__gone">原文已删除</span>
              </div>
              <iframe v-if="showPreview && viewURL" class="preview" :src="viewURL" title="文章备份" sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" />
              <button v-else-if="!showPreview" type="button" class="zg-shard zg-btn zg-btn--ghost zg-btn--sm" :aria-expanded="false" @click="showPreview = true">在页面内展开预览</button>
            </div>
            <div class="row">
              <RouterLink :to="{ name: 'reader', params: { id, version: selected.id } }" class="zg-shard zg-btn zg-btn--sm zg-btn--secondary"><ZgIcon name="eye" />在线预览</RouterLink>
              <button type="button" class="zg-shard zg-btn zg-btn--sm zg-btn--secondary" :disabled="downloading" @click="download"><ZgIcon name="download" />下载离线 HTML</button>
              <button type="button" class="zg-shard zg-btn zg-btn--sm zg-btn--secondary" :aria-expanded="showPrint" @click="showPrint = !showPrint"><ZgIcon name="printer" />打印为 PDF</button>
            </div>
            <p v-if="inWeChat" class="notice notice-warn small">微信内置浏览器不能下载文件或打印。请点右上角「···」，选择「在浏览器打开」。</p>
            <PrintHelp v-if="showPrint && viewURL" :url="viewURL" />
            <p v-if="linkError" role="alert" class="notice notice-danger">{{ linkError }} <button class="btn-link" @click="retryView">重新获取链接</button></p>
          </template>
        </section>
        <section class="zg-shard zg-panel" aria-labelledby="log-title">
          <div class="zg-panel__head"><h2 id="log-title" class="zg-panel__title">检查记录</h2><span class="zg-meta">最近 {{ detail.checks.length }} 次</span></div>
          <ol class="zg-log">
            <li v-for="r in logRows" :key="r.key" class="zg-log__item" :class="{
              'is-deleted': r.kind === 'check' && r.check.result === 'deleted',
              'is-unreachable': r.kind === 'check' && (r.check.result === 'blocked' || r.check.result === 'error'),
              'is-archived': r.kind === 'backup' }">
              <template v-if="r.kind === 'more'">
                <span class="zg-log__time">…</span>
                <details class="zg-log__text">
                  <summary>另有 {{ r.checks.length }} 次检查，均在线</summary>
                  <span v-for="c in r.checks" :key="c.checked_at" class="zg-log__note">{{ formatShort(c.checked_at) }} · 在线</span>
                </details>
              </template>
              <template v-else>
                <time class="zg-log__time" :datetime="r.at">{{ formatShort(r.at) }}</time>
                <span v-if="r.kind === 'check'" class="zg-log__text">{{ checkResultText(r.check) }}<span
                  v-if="r.check.result === 'deleted' && pageReason" class="zg-log__note">页面显示「{{ pageReason }}」。</span></span>
                <span v-else-if="r.kind === 'backup'" class="zg-log__text">{{ r.text }}</span>
                <span v-else class="zg-log__text">开始观察</span>
              </template>
            </li>
          </ol>
        </section>
      </div>
      <aside class="zg-stack">
        <section class="zg-shard zg-panel" aria-labelledby="period-title">
          <div class="zg-panel__head"><h2 id="period-title" class="zg-panel__title">观察期限</h2></div>
          <div v-if="meter" class="zg-meter" :class="meter.kind && 'zg-meter--' + meter.kind" :style="{ '--value': meter.value }">
            <div class="zg-meter__row"><span>{{ meter.label }}</span><span class="zg-meter__value">{{ meter.figure }}</span></div>
            <div class="zg-meter__track"><div class="zg-meter__fill" /></div>
          </div>
          <dl class="zg-facts">
            <dt>开始</dt><dd class="is-mono">{{ formatDateTime(w.created_at) }}</dd>
            <dt>到期</dt><dd class="is-mono">{{ formatDateTime(w.expires_at) }}</dd>
            <template v-if="w.status === 'active' && article?.next_check_at"><dt>下次检查</dt><dd class="is-mono">{{ formatShort(article.next_check_at) }}</dd></template>
            <template v-if="deleted">
              <template v-if="deletedAt"><dt>删除时间</dt><dd class="is-warm is-mono">{{ formatDateTime(deletedAt) }}</dd></template>
              <template v-if="pageReason"><dt>页面原因</dt><dd class="is-warm">{{ pageReason }}</dd></template>
            </template>
            <template v-else-if="w.ended_at"><dt>结束</dt><dd class="is-mono">{{ formatDateTime(w.ended_at) }}</dd></template>
          </dl>
          <p v-if="!deleted" class="zg-field__hint">{{ watchSummary(w) }}</p>
          <p v-if="canExtend || canWatchAgain" class="zg-field__hint">从今天起可再观察 {{ watchDays }} 天，到 {{ formatDate(renewedUntil(watchDays)) }}。</p>
        </section>
        <section v-if="detail.snapshots.length" class="zg-shard zg-panel" aria-labelledby="backup-title">
          <div class="zg-panel__head"><h2 id="backup-title" class="zg-panel__title">备份版本</h2>
            <span v-if="selected" class="zg-shard zg-tag" :class="selected.missing_images ? 'zg-tag--partial' : 'zg-tag--archived'"><i
              class="zg-tag__glyph" />{{ selected.missing_images ? '图片缺失' : '已备份' }}</span>
          </div>
          <div v-if="selected && selected.images" class="zg-meter" :class="selected.missing_images ? 'zg-meter--partial' : 'zg-meter--backup'"
            :style="{ '--value': (100 * (selected.images - selected.missing_images) / selected.images) + '%' }">
            <div class="zg-meter__row"><span>正文与图片</span><span class="zg-meter__value">{{ selected.images - selected.missing_images }} / {{ selected.images }} 张图片</span></div>
            <div class="zg-meter__track"><div class="zg-meter__fill" /></div>
          </div>
          <ul class="zg-versions" role="radiogroup" aria-label="选择备份">
            <li v-for="(s, i) in detail.snapshots" :key="s.id">
              <label class="zg-version">
                <input v-model="selected" type="radio" name="snapshot" :value="s">
                <span class="zg-version__radio" />
                <span class="zg-version__main"><span class="zg-version__name">{{ versionName(i) }} · {{ formatShort(s.captured_at) }}</span>
                  <span class="zg-version__meta">{{ s.images }} 张图片<template v-if="s.missing_images"> · 缺 {{ s.missing_images }} 张</template></span></span>
                <span class="version-note">{{ i === 0 ? '最新' : i === detail.snapshots.length - 1 ? '初次' : '' }}</span>
              </label>
            </li>
          </ul>
          <p v-if="detail.snapshots.length > 1 && selectedIndex > 0" class="zg-field__hint">正在查看较早的版本。</p>
        </section>
        <section class="zg-shard zg-panel" aria-labelledby="source-title">
          <div class="zg-panel__head"><h2 id="source-title" class="zg-panel__title">原文</h2></div>
          <dl class="zg-facts">
            <template v-if="article?.account_name"><dt>公众号</dt><dd>{{ article.account_name }}</dd></template>
            <template v-if="article?.author"><dt>作者</dt><dd>{{ article.author }}</dd></template>
            <template v-if="article?.published_at"><dt>发布</dt><dd class="is-mono">{{ formatDateTime(article.published_at) }}</dd></template>
            <template v-if="article?.last_checked_at"><dt>上次检查</dt><dd class="is-mono">{{ formatShort(article.last_checked_at) }}</dd></template>
            <dt>链接</dt><dd class="is-mono"><a :href="article?.url || w.submitted_url" target="_blank" rel="noopener noreferrer" class="source-link">{{
              (article?.url || w.submitted_url).replace(/^https?:\/\//, '') }}</a></dd>
          </dl>
        </section>
      </aside>
    </div>
  </template>
</template>
<style scoped>
.zg-notice, .confirm { max-width: none; margin-bottom: var(--space-5); }
.zg-pagehead__back, .open-link { display: inline-flex; align-items: center; gap: var(--space-1); }
.open-link { font: 500 13px/18px var(--font-sans); }
.frame { display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-4); background: var(--surface-000); border: 1px solid var(--line-100); }
.preview { width: 100%; height: 360px; border: 0; background: var(--surface-000); }
.frame > .zg-btn { align-self: flex-start; }
.zg-versions { margin: 0; }
.version-note { font: 400 12px/18px var(--font-sans); color: var(--ink-300); }
.source-link { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
