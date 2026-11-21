<script setup lang="ts">
import { api, call, messageOf } from '@/api/client'
import { session, setUser } from '@/session'
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()
const error = ref('')
const confirmSwitch = ref(false)
let token = ''

onMounted(async () => {
  token = new URLSearchParams(route.hash.slice(1)).get('t') ?? ''
  if (route.hash) await router.replace({ path: '/invite' })
  if (!token) {
    error.value = '邀请链接不完整，请重新打开邮件中的链接。'
  } else if (session.user) {
    confirmSwitch.value = true
  } else {
    await accept()
  }
})

async function accept() {
  confirmSwitch.value = false
  try {
    const r = await call(api.POST('/api/v1/auth/accept-invite', { body: { token } }))
    setUser(r.user)
    await router.replace('/watches')
  } catch (e) {
    error.value = messageOf(e)
  }
}
</script>

<template>
  <section class="zg-auth">
    <h1 class="zg-auth__title">接受邀请</h1>
    <template v-if="error">
      <p class="notice notice-danger" role="alert">{{ error }}</p>
      <div class="zg-actions"><RouterLink to="/login" class="zg-shard zg-btn">邮箱登录</RouterLink></div>
    </template>
    <template v-else-if="confirmSwitch">
      <p>当前登录的邮箱是 {{ session.user?.email }}。接受邀请后，此浏览器将使用受邀账号。</p>
      <div class="zg-actions"><button class="zg-shard zg-btn" @click="accept">切换账号并接受邀请</button>
        <RouterLink to="/watches" class="zg-shard zg-btn zg-btn--secondary">保留当前账号</RouterLink></div>
    </template>
    <p v-else class="muted" role="status"><span class="spinner" aria-hidden="true" /> 正在完成注册…</p>
  </section>
</template>
