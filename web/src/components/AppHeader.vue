<script setup lang="ts">
import { serviceName, session } from '@/session'
</script>

<template>
  <header class="header">
    <div class="inner">
      <RouterLink to="/" class="brand">
        <img src="/favicon.svg" alt="" width="24" height="24">
        <span>{{ serviceName() }}</span>
      </RouterLink>
      <nav class="nav" aria-label="主导航">
        <template v-if="session.user">
          <RouterLink to="/watches">我的观察</RouterLink>
          <RouterLink to="/settings">设置</RouterLink>
        </template>
        <RouterLink v-else-if="session.loaded" to="/login">登录</RouterLink>
      </nav>
    </div>
  </header>
</template>

<style scoped>
.header {
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  padding-top: env(safe-area-inset-top);
}
.inner {
  max-width: 760px;
  margin: 0 auto;
  padding: 0 16px;
  height: 52px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--fg);
  font-weight: 700;
  white-space: nowrap;
}
.brand:hover {
  text-decoration: none;
}
.nav {
  display: flex;
  gap: 18px;
}
.nav a {
  color: var(--muted);
  white-space: nowrap;
}
.nav a.router-link-active {
  color: var(--fg);
  font-weight: 600;
}
</style>
