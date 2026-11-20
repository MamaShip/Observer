<script setup lang="ts">
defineProps<{
  label: string
  value: string | number
  /** A second line: a breakdown or a comparison. */
  note?: string
}>()
</script>

<template>
  <div class="tile">
    <div class="label">{{ label }}</div>
    <div class="value">{{ value }}</div>
    <div v-if="note" class="note">{{ note }}</div>
  </div>
</template>

<style scoped>
.tile {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px 14px;
  min-width: 0;
}
.label {
  color: var(--muted);
  font-size: 0.85rem;
}
.value {
  font-size: 1.5rem;
  font-weight: 650;
  line-height: 1.3;
  font-variant-numeric: tabular-nums;
}
.note {
  color: var(--muted);
  font-size: 0.8rem;
  margin-top: 2px;
}
</style>
