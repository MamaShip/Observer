<script setup lang="ts">
import { api, call, messageOf } from '@/api/client'
import { session, setUser } from '@/session'
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()
const error = ref('')
// Wait for a click: mail scanners that run scripts must not spend the
// one-time link (and receive the session).
const ready = ref(false)
let token = ''

onMounted(async () => {
  token = new URLSearchParams(route.hash.slice(1)).get('t') ?? ''
  if (route.hash) await router.replace({ path: '/invite' })
  if (!token) {
    error.value = '邀请链接不完整，请重新打开邮件中的链接。'
  } else {
    ready.value = true
  }
})

async function accept() {
  ready.value = false
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
    <template v-else-if="ready">
      <p v-if="session.user">当前登录的邮箱是 {{ session.user.email }}。接受邀请后，此浏览器将使用受邀账号。</p>
      <p v-else>点击下方按钮，完成注册并登录。</p>
      <div class="zg-actions"><button class="zg-shard zg-btn" @click="accept">{{ session.user ? '切换账号并接受邀请' : '接受邀请并注册' }}</button>
        <RouterLink v-if="session.user" to="/watches" class="zg-shard zg-btn zg-btn--secondary">保留当前账号</RouterLink></div>
    </template>
    <p v-else class="muted" role="status"><span class="spinner" aria-hidden="true" /> 正在完成注册…</p>
  </section>
</template>
