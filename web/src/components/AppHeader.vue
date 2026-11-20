<script setup lang="ts">
import { serviceName, session } from '@/session'
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement>()
watch(() => route.fullPath, () => { menuOpen.value = false })

function closeMenu() {
  if (menuOpen.value) {
    menuOpen.value = false
    menuButton.value?.focus()
  }
}
</script>

<template>
  <header class="zg-header" @keydown.esc="closeMenu">
    <div class="zg-container zg-header__inner">
      <RouterLink to="/" class="zg-header__brand" :aria-label="serviceName() + ' 首页'">
        <template v-if="serviceName() === '折光观察者'">
          <img class="zg-only-light" src="@/assets/logo/zheguang-lockup-light-bg.svg" alt="">
          <img class="zg-only-dark" src="@/assets/logo/zheguang-lockup-dark-bg.svg" alt="">
        </template>
        <template v-else><img src="/favicon.svg" alt="">{{ serviceName() }}</template>
      </RouterLink>
      <nav id="main-nav" class="zg-nav" :class="{ 'menu-open': menuOpen }" aria-label="主导航" @click="menuOpen = false">
        <template v-if="session.user">
          <RouterLink to="/watches" :class="{ 'is-current': route.name === 'watch' }">我的观察</RouterLink>
          <RouterLink to="/settings">设置</RouterLink>
        </template>
        <RouterLink v-else to="/">首页</RouterLink>
        <RouterLink to="/about">关于</RouterLink>
      </nav>
      <div class="zg-header__actions">
        <RouterLink v-if="session.user?.role === 'admin'" class="zg-link small" to="/admin">管理后台</RouterLink>
        <RouterLink v-if="session.user" class="zg-account" to="/settings">{{ session.user.email }}</RouterLink>
        <RouterLink v-else-if="session.loaded && route.name !== 'login'" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" to="/login">登录</RouterLink>
        <button ref="menuButton" type="button" class="menu-toggle zg-shard zg-btn zg-btn--secondary zg-btn--sm"
          aria-controls="main-nav" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">菜单</button>
      </div>
    </div>
  </header>
</template>

<style scoped>
.zg-header { padding-top: env(safe-area-inset-top); }
.zg-header__actions { margin-left: auto; }
.zg-header__brand { gap: 8px; color: var(--ink-100); font-family: var(--font-display); }
.zg-account { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 220px; }
.menu-toggle { display: none; }
@media (max-width: 767px) {
  .menu-toggle { display: inline-flex; }
  .zg-header__inner { gap: var(--space-2); }
  .menu-open { position: absolute; top: 100%; left: 0; right: 0; display: flex; height: auto; flex-wrap: wrap; background: var(--surface-100); border-bottom: 1px solid var(--line-100); padding: 0 var(--space-4); }
  .menu-open a { min-height: 44px; }
}
</style>
