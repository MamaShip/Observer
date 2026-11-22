<script setup lang="ts">
// Inline Lucide 0.460.0 icons in the design system's style: 24 grid,
// 1.5 stroke, square caps, sharp joins, colour from the text.
type IconName = 'arrow-left' | 'download' | 'eye' | 'printer' | 'external-link' | 'mail' | 'log-out' | 'user-plus' | 'refresh' | 'chevron-down' | 'check'
defineProps<{ name: IconName; size?: 16 | 20 | 24 }>()
const icons: Record<IconName, string> = {
  'arrow-left': '<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>',
  download: '<path d="M21 15v6H3v-6"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>',
  eye: '<path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0"/><circle cx="12" cy="12" r="3"/>',
  printer: '<polyline points="6 9 6 2 18 2 18 9"/><path d="M6 18H2v-9h20v9h-4"/><rect width="12" height="8" x="6" y="14"/>',
  'external-link': '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v8H3V6h8"/>',
  mail: '<rect width="20" height="16" x="2" y="4"/><path d="m22 7-10 6L2 7"/>',
  'log-out': '<path d="M9 21H3V3h6"/><polyline points="16 17 21 12 16 7"/><line x1="21" x2="9" y1="12" y2="12"/>',
  'user-plus': '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><line x1="19" x2="19" y1="8" y2="14"/><line x1="22" x2="16" y1="11" y2="11"/>',
  refresh: '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
  'chevron-down': '<path d="m6 9 6 6 6-6"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
}
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- fixed markup from the table above -->
  <svg class="zg-icon" :class="size === 16 || size === undefined ? 'zg-icon--16' : size === 24 ? 'zg-icon--24' : ''" viewBox="0 0 24 24"
    fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="square" stroke-linejoin="miter" aria-hidden="true" v-html="icons[name]" />
</template>
