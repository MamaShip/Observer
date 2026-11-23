<script setup lang="ts">
import { serviceName } from '@/session'
import { useRoute } from 'vue-router'
const route = useRoute()
</script>

<template>
  <nav class="zg-side" aria-label="管理后台">
    <RouterLink to="/" class="zg-side__brand" :aria-label="serviceName() + ' 首页'"><img src="/favicon.svg" alt="">{{ serviceName() }}<small>ADMIN</small></RouterLink>
    <span class="zg-side__label">运营</span>
    <RouterLink to="/admin">概览</RouterLink>
    <RouterLink to="/admin/users">账户管理</RouterLink>
    <RouterLink to="/admin/invites">邀请链接</RouterLink>
    <RouterLink to="/admin/articles" :class="{ 'is-current': route.name === 'admin-article' }">文章管理</RouterLink>
    <span class="zg-side__label">其他</span>
    <RouterLink to="/watches">返回我的观察</RouterLink>
  </nav>
</template>
