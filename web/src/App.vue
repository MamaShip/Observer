<script setup lang="ts">
import AppHeader from '@/components/AppHeader.vue'
import AdminNav from '@/components/admin/AdminNav.vue'
import { serviceName } from '@/session'
import { useRoute } from 'vue-router'

const route = useRoute()
</script>

<template>
  <div v-if="route.meta.admin" class="zg-admin">
    <AdminNav />
    <main class="zg-admin__main"><RouterView /></main>
  </div>
  <div v-else class="zg-page">
    <AppHeader />
    <main class="zg-container page" :class="{ 'reader-page': route.name === 'reader' }">
      <!-- Keyed by route name: /login and /join share LoginView, which reads its mode once. -->
      <RouterView v-slot="{ Component, route: r }"><component :is="Component" :key="r.name" /></RouterView>
    </main>
    <footer class="zg-footer">
      <div class="zg-container zg-footer__inner">
        <div>
          <p v-if="route.name === 'home'" class="footer-motto">时间从来不回答。</p>
          <p class="zg-footer__fine">© {{ new Date().getFullYear() }} {{ serviceName() }} · 开源项目，可自行部署</p>
        </div>
        <nav class="zg-footer__links" aria-label="页脚">
          <RouterLink to="/about">关于</RouterLink>
          <RouterLink to="/privacy">隐私说明</RouterLink>
          <RouterLink to="/terms">服务条款</RouterLink>
          <a href="https://github.com/MamaShip/Observer" target="_blank" rel="noopener noreferrer">源代码</a>
        </nav>
      </div>
    </footer>
  </div>
</template>
