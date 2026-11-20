<script setup lang="ts">
defineProps<{ label: string; value: string | number; note?: string }>()
</script>
<template>
  <div class="zg-shard zg-stat">
    <span class="zg-stat__label">{{ label }}</span>
    <span class="zg-stat__value">{{ value }}</span>
    <span v-if="note" class="zg-stat__sub">{{ note }}</span>
  </div>
</template>
