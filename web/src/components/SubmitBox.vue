<script setup lang="ts">
import { api, call, messageOf, type WatchDetail } from '@/api/client'
import { extractLink } from '@/lib/link'
import { ref } from 'vue'

const emit = defineEmits<{ submitted: [detail: WatchDetail] }>()

const text = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  const url = extractLink(text.value)
  if (!url) {
    error.value = text.value.trim()
      ? '没有找到公众号文章链接。链接应以 https://mp.weixin.qq.com/ 开头。'
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
  <form class="submit card" novalidate @submit.prevent="submit">
    <label class="field">
      <span>观察一篇新文章</span>
      <textarea
        v-model="text"
        name="url"
        rows="2"
        inputmode="url"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        placeholder="粘贴公众号文章链接，例如 https://mp.weixin.qq.com/s/…"
        @keydown.enter.exact.prevent="submit"
      />
    </label>
    <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
    <div class="row actions">
      <span class="muted small">在微信里打开文章，点右上角「···」→「复制链接」。</span>
      <button class="btn btn-primary" type="submit" :disabled="busy">
        <span v-if="busy" class="spinner" aria-hidden="true" />
        开始观察
      </button>
    </div>
  </form>
</template>

<style scoped>
.submit {
  display: grid;
  gap: 10px;
}
.submit .notice {
  margin: 0;
}
.actions {
  justify-content: space-between;
}
</style>
