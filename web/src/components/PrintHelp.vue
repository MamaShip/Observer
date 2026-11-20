<script setup lang="ts">
import { inWeChat, isAndroid, isIOS } from '@/lib/device'

defineProps<{ url: string }>()
</script>

<template>
  <div class="notice notice-info print-help">
    <p v-if="inWeChat">
      微信内置浏览器不支持打印。请点右上角「···」，选择「在浏览器打开」，再回到这里操作。
    </p>
    <template v-else>
      <p><a :href="url" target="_blank" rel="noopener">在新窗口打开备份</a>，然后：</p>
      <ol>
        <li v-if="isIOS">点浏览器的「分享」按钮，选择「打印」；在打印预览里再点「分享」，选择「存储到文件」即可保存为 PDF。</li>
        <li v-else-if="isAndroid">点浏览器右上角「⋮」→「分享」→「打印」，打印机选择「另存为 PDF」。</li>
        <li v-else>按 <kbd>Ctrl</kbd> + <kbd>P</kbd>（Mac 上是 <kbd>⌘</kbd> + <kbd>P</kbd>），目标打印机选择「另存为 PDF」。</li>
      </ol>
      <p class="small muted">备份页面已经为打印排好版：图片适应纸宽，页脚会注明原文链接和备份时间。</p>
    </template>
  </div>
</template>

<style scoped>
.print-help ol {
  margin: 0 0 0.6em;
  padding-left: 1.3em;
}
kbd {
  font-family: var(--mono);
  font-size: 0.85em;
  padding: 0 4px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
}
</style>
