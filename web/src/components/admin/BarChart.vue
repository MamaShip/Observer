<script setup lang="ts">
import { formatNumber } from '@/lib/format'
import { shortDay } from '@/lib/admin'
import { computed, ref } from 'vue'

// One series over days, as columns. Single series: no legend; the title
// names it. Hover (or focus and arrow keys) shows a day's value; the
// overview's data table has every number without hovering.
const props = defineProps<{
  title: string
  days: string[] // YYYY-MM-DD, oldest first
  values: number[]
}>()

const W = 300 // viewBox width; the SVG scales to its container
const H = 64 // plot height
const TOP = 6 // room above the tallest column
const slot = computed(() => W / Math.max(1, props.values.length))
const barW = computed(() => Math.min(24, slot.value - 2)) // 2px surface gap between columns
const peak = computed(() => Math.max(0, ...props.values)) // shown
const max = computed(() => Math.max(1, peak.value)) // the scale; never divide by 0
const total = computed(() => props.values.reduce((a, b) => a + b, 0))
const active = ref<number | null>(null)

function bar(i: number) {
  const v = props.values[i] ?? 0
  const h = v === 0 ? 0 : Math.max(2, (v / max.value) * H)
  const x = i * slot.value + (slot.value - barW.value) / 2
  const y = TOP + H - h
  const r = Math.min(4, barW.value / 2, h)
  // Rounded data end, square at the baseline.
  const d =
    h === 0
      ? ''
      : `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + barW.value - r}Q${x + barW.value},${y} ${x + barW.value},${y + r}V${y + h}Z`
  return { d, cx: x + barW.value / 2 }
}

const bars = computed(() => props.values.map((_, i) => bar(i)))

function onKey(e: KeyboardEvent) {
  const n = props.values.length
  if (!n) return
  if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
    e.preventDefault()
    const cur = active.value ?? n - 1
    active.value = Math.min(n - 1, Math.max(0, cur + (e.key === 'ArrowLeft' ? -1 : 1)))
  } else if (e.key === 'Escape') {
    active.value = null
  }
}

const tipLeft = computed(() => (active.value === null ? 0 : (bars.value[active.value]!.cx / W) * 100))
</script>

<template>
  <figure class="chart">
    <figcaption>
      <span class="title">{{ title }}</span>
      <span class="sum">共 {{ formatNumber(total) }}</span>
    </figcaption>
    <div class="plot">
      <svg
        :viewBox="`0 0 ${W} ${H + TOP}`"
        role="img"
        :aria-label="`${title}，近 ${values.length} 天共 ${total}，单日最高 ${peak}。用左右方向键逐日查看。`"
        tabindex="0"
        @keydown="onKey"
        @focus="active ??= values.length - 1"
        @blur="active = null"
        @pointerleave="active = null"
      >
        <line class="grid" x1="0" :x2="W" :y1="TOP" :y2="TOP" />
        <line class="baseline" x1="0" :x2="W" :y1="TOP + H" :y2="TOP + H" />
        <path v-for="(b, i) in bars" :key="i" :d="b.d" class="bar" :class="{ dim: active !== null && active !== i }" />
        <!-- Hit targets: the whole day's column, bigger than the bar. -->
        <rect
          v-for="(_, i) in values"
          :key="`hit-${i}`"
          :x="i * slot"
          y="0"
          :width="slot"
          :height="H + TOP"
          fill="transparent"
          @pointerenter="active = i"
        />
      </svg>
      <div v-if="active !== null" class="tip" :style="{ left: `${tipLeft}%` }" role="status">
        <strong>{{ formatNumber(values[active] ?? 0) }}</strong>
        <span>{{ shortDay(days[active] ?? '') }}</span>
      </div>
    </div>
    <div class="axis">
      <span>{{ shortDay(days[0] ?? '') }}</span>
      <span>最高 {{ formatNumber(peak) }}</span>
      <span>{{ shortDay(days[days.length - 1] ?? '') }}</span>
    </div>
  </figure>
</template>

<style scoped>
.chart {
  margin: 0;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px 14px 8px;
  min-width: 0;
}
figcaption {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 6px;
}
.title {
  font-weight: 600;
  font-size: 0.9rem;
}
.sum {
  color: var(--muted);
  font-size: 0.8rem;
  font-variant-numeric: tabular-nums;
}
.plot {
  position: relative;
}
svg {
  display: block;
  width: 100%;
  height: auto;
  overflow: visible;
  border-radius: 4px;
}
svg:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 4px;
}
.bar {
  fill: var(--accent);
  transition: opacity 0.1s;
}
.bar.dim {
  opacity: 0.35;
}
.grid {
  stroke: var(--border);
  stroke-width: 0.5;
  stroke-dasharray: 2 3;
}
.baseline {
  stroke: var(--border);
  stroke-width: 1;
}
.tip {
  position: absolute;
  bottom: calc(100% + 4px);
  transform: translateX(-50%);
  background: var(--fg);
  color: var(--bg);
  border-radius: 6px;
  padding: 2px 8px;
  font-size: 0.8rem;
  white-space: nowrap;
  pointer-events: none;
  display: flex;
  gap: 6px;
  align-items: baseline;
  font-variant-numeric: tabular-nums;
}
.tip span {
  opacity: 0.75;
}
.axis {
  display: flex;
  justify-content: space-between;
  color: var(--faint);
  font-size: 0.75rem;
  margin-top: 4px;
  font-variant-numeric: tabular-nums;
}
</style>
