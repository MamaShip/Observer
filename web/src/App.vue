<script setup lang="ts">
import AppHeader from '@/components/AppHeader.vue'
</script>

<template>
  <AppHeader />
  <main class="page">
    <RouterView />
  </main>
  <footer class="footer">
    <RouterLink to="/about">关于</RouterLink>
    <RouterLink to="/privacy">隐私</RouterLink>
    <RouterLink to="/terms">服务条款</RouterLink>
    <a href="https://github.com/MamaShip/Observer" target="_blank" rel="noopener noreferrer">源代码</a>
  </footer>
</template>

<style scoped>
.footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px 20px;
  padding: 0 16px calc(32px + env(safe-area-inset-bottom));
  font-size: 0.875rem;
}
.footer a {
  color: var(--faint);
}
</style>
