<script setup lang="ts">
import { api, call, messageOf, type WatchDetail } from '@/api/client'
import PrintHelp from '@/components/PrintHelp.vue'
import { formatDateTime } from '@/lib/format'
import { useSnapshotLinks } from '@/lib/snapshotLinks'
import { inWeChat } from '@/lib/device'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()
const watchId = computed(() => String(route.params.id))
const detail = ref<WatchDetail>()
const error = ref('')
const attempt = ref(0)
const printHelp = ref(false)
watch([watchId, attempt], async ([id], _, onCleanup) => {
  let active = true
  onCleanup(() => { active = false })
  detail.value = undefined
  error.value = ''
  try {
    const d = await call(api.GET('/api/v1/watches/{id}', { params: { path: { id } } }))
    if (active) detail.value = d
  } catch (e) {
    if (active) error.value = messageOf(e)
  }
}, { immediate: true })
const selected = computed(() => detail.value?.snapshots.find(s => s.id === route.params.version))
const { viewURL, linkError, retryView, download, downloading } = useSnapshotLinks(computed(() => selected.value?.id))
const deleted = computed(() => detail.value?.watch.article?.status === 'deleted' || detail.value?.watch.end_reason === 'deleted')

function changeVersion(e: Event) {
  const version = (e.target as HTMLSelectElement).value
  void router.push({ name: 'reader', params: { id: watchId.value, version } })
}
</script>

<template>
    <header class="zg-pagehead">
      <div>
        <RouterLink :to="{ name: 'watch', params: { id: watchId } }" class="zg-pagehead__back">← 返回观察详情</RouterLink>
        <h1 class="zg-pagehead__title zg-pagehead__title--article">{{ detail?.watch.article?.title || '文章备份' }}</h1>
      </div>
    </header>
    <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="attempt++">重试</button></p>
    <p v-else-if="!detail" class="muted" role="status">正在加载备份…</p>
    <p v-else-if="!selected" class="notice notice-info">没有找到这个备份版本。请返回观察详情选择可用版本。</p>
    <template v-else>
      <div class="zg-shard zg-provenance" :class="{ 'is-deleted': deleted }">
        <span class="zg-provenance__label">BACKUP</span>
        <time class="zg-provenance__time" :datetime="selected.captured_at">{{ formatDateTime(selected.captured_at) }}</time>
        <span v-if="detail.watch.article?.account_name">来源「{{ detail.watch.article.account_name }}」</span>
        <span v-if="deleted" class="zg-provenance__gone">原文已删除</span>
        <div class="zg-provenance__actions reader-actions">
          <label class="zg-shard zg-select zg-select--sm"><select aria-label="备份版本" :value="selected.id" @change="changeVersion">
              <option v-for="(s, i) in detail.snapshots" :key="s.id" :value="s.id">版本 v{{ detail.snapshots.length - i }}</option>
            </select><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg></label>
          <button class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="downloading" @click="download">下载 HTML</button>
          <button class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :aria-expanded="printHelp" @click="printHelp = !printHelp">打印为
            PDF</button>
        </div>
      </div>
      <p v-if="inWeChat" class="notice notice-warn">微信内置浏览器不能下载文件或打印。请使用系统浏览器打开。</p>
      <PrintHelp v-if="printHelp && viewURL" :url="viewURL" />
      <p v-if="linkError" role="alert" class="notice notice-danger">{{ linkError }} <button class="btn-link"
          @click="retryView">重新获取链接</button></p>
      <iframe v-if="viewURL" class="reader-frame" :src="viewURL" title="文章备份正文"
        sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" />
    </template>
</template>

<style scoped>
.reader-actions { flex-wrap: wrap; }
.reader-frame { display: block; margin-top: var(--space-4); width: 100%; height: 80vh; border: 1px solid var(--line-100); }
@media (max-width: 767px) { .reader-actions { width: 100%; margin-left: 0; } }
</style>
