<script setup lang="ts">
import type { Watch } from '@/api/client'
import { watchBadge } from '@/lib/status'
import { computed } from 'vue'

const props = defineProps<{ watch: Watch }>()
const badge = computed(() => watchBadge(props.watch))
</script>

<template>
  <span class="badge" :class="`badge-${badge.tone}`">{{ badge.label }}</span>
</template>

<style scoped>
.badge {
  display: inline-block;
  flex: none;
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 0.8rem;
  font-weight: 600;
  line-height: 1.6;
  white-space: nowrap;
}
.badge-info {
  background: var(--accent-soft);
  color: var(--accent);
}
.badge-ok {
  background: var(--ok-soft);
  color: var(--ok);
}
.badge-danger {
  background: var(--danger-soft);
  color: var(--danger);
}
.badge-warn {
  background: var(--warn-soft);
  color: var(--warn);
}
.badge-muted {
  background: var(--muted-soft);
  color: var(--muted);
}
</style>
