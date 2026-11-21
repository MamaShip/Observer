<script setup lang="ts">
// The Official Account's QR code (web/public/wechat-qr.jpg), shown only when
// the account entry is on. In WeChat's browser it can be long-pressed.
import { session } from '@/session'
import { computed } from 'vue'

const name = computed(() => session.site?.wechat?.name || session.site?.service_name || '')
</script>

<template>
  <figure v-if="session.site?.wechat" class="wx-qr">
    <img src="/wechat-qr.jpg" width="112" height="112" :alt="`公众号「${name}」的二维码`" loading="lazy">
    <figcaption>微信扫码或长按识别<br>关注「{{ name }}」</figcaption>
  </figure>
</template>

<style scoped>
.wx-qr { display: flex; align-items: center; gap: var(--space-4); margin: 0; }
/* The code needs its white quiet zone, in the dark theme too. */
.wx-qr img { flex: none; display: block; padding: 4px; background: #fff; border: var(--border-hair) solid var(--line-100); }
.wx-qr figcaption { font: 400 13px/20px var(--font-sans); color: var(--ink-300); }
</style>
