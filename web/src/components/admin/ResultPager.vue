<script setup lang="ts">
defineProps<{ page: number; hasNext: boolean; loading: boolean }>()
defineEmits<{ go: [page: number] }>()
</script>
<template>
  <nav class="zg-pager" aria-label="结果分页"><span>第 {{ page + 1 }} 页</span>
    <div class="zg-pager__btns">
      <button type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="loading || page === 0" @click="$emit('go', page - 1)">上一页</button>
      <button type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="loading || !hasNext" @click="$emit('go', page + 1)">下一页</button>
    </div>
  </nav>
</template>
