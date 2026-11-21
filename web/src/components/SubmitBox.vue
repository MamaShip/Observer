<script setup lang="ts">
import { api, call, messageOf, type WatchDetail } from '@/api/client'
import { session } from '@/session'
import ZgIcon from '@/components/ZgIcon.vue'
import { extractLink } from '@/lib/link'
import { ref } from 'vue'

const emit = defineEmits<{ submitted: [detail: WatchDetail] }>()

const text = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  if (busy.value) return
  error.value = ''
  const url = extractLink(text.value)
  if (!url) {
    error.value = text.value.trim()
      ? '这不是公众号文章链接。请在微信里打开文章，点右上角「···」复制链接。'
      : '请先粘贴文章链接。'
    return
  }
  busy.value = true
  try {
    const detail = await call(api.POST('/api/v1/watches', { body: { url } }))
    text.value = ''
    emit('submitted', detail)
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="submit zg-shard zg-panel" novalidate @submit.prevent="submit">
    <div class="zg-field">
      <div class="zg-field__label-row">
        <label for="article-url" class="zg-field__label">提交文章链接</label>
        <span v-if="session.site" class="zg-field__counter">观察 {{ session.site.watch_days }} 天 · 定期检查</span>
      </div>
      <div class="zg-field__row">
        <div class="zg-shard zg-input" :class="{ 'is-invalid': error }">
          <textarea id="article-url" v-model="text" name="url" rows="1" inputmode="url" spellcheck="false"
            autocapitalize="off" autocomplete="off" placeholder="https://mp.weixin.qq.com/s/…" aria-describedby="link-hint"
            @keydown.enter.exact.prevent="submit" />
        </div>
        <button class="zg-shard zg-btn" type="submit" :disabled="busy">
          <span v-if="busy" class="spinner" aria-hidden="true" />
          <ZgIcon v-else name="eye" />
          开始观察
        </button>
      </div>
      <span id="link-hint" class="zg-field__hint" :class="{ 'is-error': error }" :role="error ? 'alert' : undefined">
        {{ error || '在微信里打开文章，点右上角「···」复制链接。提交后会立即备份。' }}
      </span>
    </div>
    <slot />
  </form>
</template>
<style scoped>
.submit { --_c: var(--chamfer-l); padding: var(--space-5); }
.zg-field { max-width: none; }
textarea { resize: none; color: inherit; background: transparent; border: 0; outline: 0; font-family: var(--font-mono); line-height: 24px; height: 24px; padding: 0; }
.zg-field__label-row { flex-wrap: wrap; gap: var(--space-2); }
@media (max-width: 480px) { .submit { padding: var(--space-4); } .zg-btn { padding: 0 var(--space-3); } }
</style>
