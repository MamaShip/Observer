<script setup lang="ts">
import { computed, ref } from 'vue'
import type { AdminDay } from '@/api/client'
import { shortDay } from '@/lib/admin'

const props = defineProps<{ days: AdminDay[] }>()
const peak = computed(() => Math.max(1, ...props.days.map(d => Math.max(d.watches, d.snapshots))))
const active = ref<number | null>(null)
const selected = computed(() => active.value === null ? undefined : props.days[active.value])
const x = (i: number) => 20 + 276 * i / Math.max(1, props.days.length - 1)
const y = (n: number) => 100 - 90 * n / peak.value
const points = (series: 'watches' | 'snapshots') => props.days.map((d, i) => x(i) + ',' + y(d[series])).join(' ')
function move(offset: number) {
  active.value = Math.min(props.days.length - 1, Math.max(0, (active.value ?? props.days.length - 1) + offset))
}
</script>
<template>
  <figure class="zg-shard zg-chart">
    <figcaption class="zg-chart__head"><h3 class="zg-chart__title">新增观察与备份</h3>
      <div class="zg-legend"><span><i class="watches" />新观察</span><span><i class="snapshots" />新备份</span></div>
    </figcaption>
    <svg viewBox="0 0 300 108" role="img" aria-label="每日新增观察与新增备份，可用方向键查看每日数值" tabindex="0"
      @focus="move(0)" @keydown.left.prevent="move(-1)" @keydown.right.prevent="move(1)" @blur="active = null" @pointerleave="active = null">
      <line v-for="n in [0, 1, 2]" :key="n" class="grid" x1="20" x2="296" :y1="10 + n * 45" :y2="10 + n * 45" />
      <text class="axis" x="0" y="14">{{ peak }}</text><text class="axis" x="0" y="100">0</text>
      <polyline class="s1" :points="points('watches')" /><polyline class="s2" :points="points('snapshots')" />
      <rect v-for="(d, i) in days" :key="d.day" class="hit" :x="x(i) - 5" y="6" :width="276 / Math.max(1, days.length - 1)" height="96" @pointerenter="active = i" />
    </svg>
    <div class="zg-meter__row"><span>{{ shortDay(days[0]?.day ?? '') }}</span>
      <span v-if="selected" role="status">{{ shortDay(selected.day) }} · 观察 {{ selected.watches }} · 备份 {{ selected.snapshots }}</span>
      <span>{{ shortDay(days[days.length - 1]?.day ?? '') }}</span></div>
  </figure>
</template>
<style scoped>
figure { margin: 0; }
.watches { background: var(--chart-1); }
.snapshots { background: var(--chart-2); }
</style>
