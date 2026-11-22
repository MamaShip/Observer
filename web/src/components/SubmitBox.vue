<script setup lang="ts">
import { api, call, messageOf, type WatchDetail } from '@/api/client'
import { session } from '@/session'
import ZgIcon from '@/components/ZgIcon.vue'
import { extractLink } from '@/lib/link'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const emit = defineEmits<{ submitted: [detail: WatchDetail] }>()

// The box grows with its content up to this many lines, then scrolls.
const maxLines = 4

const text = ref('')
const busy = ref(false)
const error = ref('')
const box = ref<HTMLTextAreaElement | null>(null)
const link = computed(() => extractLink(text.value))

watch(text, (now, before) => {
  error.value = ''
  // A paste (from the context menu or a keyboard's clipboard panel, which
  // fires no paste event) adds many characters at once. Keep only the link
  // so the box shows it whole instead of a share text around it. Typing adds
  // one character at a time and is left alone.
  if (now.length - before.length > 1) {
    const url = extractLink(now)
    if (url && url !== now) text.value = url
  }
  nextTick(fit)
})

function fit() {
  const el = box.value
  if (!el) return
  el.style.height = 'auto'
  // Not laid out (hidden, or a test DOM): fall back to the stylesheet height.
  if (!el.scrollHeight) {
    el.style.height = ''
    return
  }
  const lineHeight = parseFloat(getComputedStyle(el).lineHeight) || 24
  const max = lineHeight * maxLines
  el.style.height = `${Math.min(el.scrollHeight, max)}px`
  el.style.overflowY = el.scrollHeight > max ? 'auto' : 'hidden'
}

onMounted(() => {
  fit()
  window.addEventListener('resize', fit)
})
onBeforeUnmount(() => window.removeEventListener('resize', fit))

async function submit() {
  if (busy.value) return
  error.value = ''
  const url = link.value
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
          <textarea id="article-url" ref="box" v-model="text" name="url" rows="1" inputmode="url" spellcheck="false"
            autocapitalize="off" autocomplete="off" placeholder="https://mp.weixin.qq.com/s/…" aria-describedby="link-hint"
            @keydown.enter.exact.prevent="submit" />
        </div>
        <button class="zg-shard zg-btn" type="submit" :disabled="busy">
          <span v-if="busy" class="spinner" aria-hidden="true" />
          <ZgIcon v-else name="eye" />
          开始观察
        </button>
      </div>
      <span v-if="error" id="link-hint" class="zg-field__hint is-error" role="alert">{{ error }}</span>
      <span v-else-if="link" id="link-hint" class="zg-field__hint is-ok" aria-live="polite">
        <ZgIcon name="check" />已识别文章链接，点「开始观察」提交。
      </span>
      <span v-else id="link-hint" class="zg-field__hint" aria-live="polite">在微信里打开文章，点右上角「···」复制链接。提交后会立即备份。</span>
    </div>
    <slot />
  </form>
</template>
<style scoped>
.submit { --_c: var(--chamfer-l); padding: var(--space-5); }
.zg-field { max-width: none; }
.zg-field__row { align-items: flex-start; }
/* The input grows with the link; keep the 44px single-line look when empty. */
.zg-input { height: auto; min-height: 44px; padding-top: 10px; padding-bottom: 10px; box-sizing: border-box; }
textarea { display: block; resize: none; color: inherit; background: transparent; border: 0; outline: 0; font-family: var(--font-mono);
  line-height: 24px; height: 24px; padding: 0; overflow-y: hidden; overflow-wrap: anywhere; }
.zg-field__label-row { flex-wrap: wrap; gap: var(--space-2); }
.zg-field__hint.is-ok { display: flex; align-items: center; gap: var(--space-1); color: var(--ok); }
.zg-field__hint.is-ok .zg-icon { width: 14px; height: 14px; flex: none; }
@media (max-width: 480px) {
  .submit { padding: var(--space-4); }
  /* Stack the button under the input so the link gets the full width. */
  .zg-field__row { flex-direction: column; align-items: stretch; }
}
</style>
