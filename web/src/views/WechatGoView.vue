<script setup lang="ts">
// A link from an Official Account reply: /wx/go?to=<page>#t=<token>. The
// token signs in the account bound to that WeChat user, once; after that
// (or if it expired) the page opens if this browser is logged in anyway.
import { ApiError, api, call, messageOf } from '@/api/client'
import { dropToken, takeToken } from '@/lib/wechat'
import { loginRoute, safeNext } from '@/router'
import { session, setUser } from '@/session'
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()
const error = ref('')

onMounted(async () => {
  const to = safeNext(route.query.to)
  const token = takeToken('go', route.hash)
  if (token) {
    try {
      const r = await call(api.POST('/api/v1/wechat/login', { body: { token } }))
      dropToken('go')
      setUser(r.user)
      await router.replace(to)
      return
    } catch (e) {
      dropToken('go')
      if (!(e instanceof ApiError) || (e.status !== 410 && e.status !== 404)) {
        error.value = messageOf(e)
        return
      }
    }
  }
  await router.replace(session.user ? to : loginRoute(to))
})
</script>

<template>
  <section class="zg-auth">
    <template v-if="error">
      <h1 class="zg-auth__title">没能打开</h1>
      <p class="notice notice-danger" role="alert">{{ error }}</p>
      <div class="zg-actions"><RouterLink to="/login" class="zg-shard zg-btn">邮箱登录</RouterLink></div>
    </template>
    <p v-else class="muted" role="status"><span class="spinner" aria-hidden="true" /> 正在打开…</p>
  </section>
</template>
