<script setup lang="ts">
import type { Watch } from '@/api/client'
import { watchBadge } from '@/lib/status'
import { computed } from 'vue'

const props = defineProps<{ watch: Watch }>()
const badge = computed(() => watchBadge(props.watch))
</script>
<template>
  <span class="zg-shard zg-tag" :class="'zg-tag--' + badge.tag"><i class="zg-tag__glyph" aria-hidden="true" />{{ badge.label }}</span>
</template>
