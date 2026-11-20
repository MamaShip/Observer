<script setup lang="ts">
import type { Watch } from '@/api/client'
import { daysLeft, endingSoonDays, watchBadge } from '@/lib/status'
import { computed } from 'vue'

const props = defineProps<{ watch: Watch }>()
const state = computed(() => {
  const w = props.watch
  const badge = watchBadge(w)
  if (w.status === 'resolving') return ['archiving', badge.label]
  if (w.status === 'failed') return ['failed', badge.label]
  if (w.end_reason === 'deleted') return ['deleted', badge.label]
  if (w.status === 'active') {
    return daysLeft(w) <= endingSoonDays ? ['expiring', '即将到期'] : ['watching', badge.label]
  }
  return [badge.tone === 'warn' ? 'unreachable' : 'ended', badge.label]
})
</script>
<template>
  <span class="zg-shard zg-tag" :class="'zg-tag--' + state[0]"><i class="zg-tag__glyph" aria-hidden="true" />{{ state[1] }}</span>
</template>
