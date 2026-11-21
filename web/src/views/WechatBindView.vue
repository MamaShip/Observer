<script setup lang="ts">
// A bind link from an Official Account reply: /wx/bind#t=<token>. Whoever is
// logged in here takes the WeChat user that received the link; if they sent
// an article before binding, it is watched now.
import { ApiError, api, call, messageOf } from '@/api/client'
import { dropToken, takeToken } from '@/lib/wechat'
import { loginRoute } from '@/router'
import { ensureSession, session, setUser } from '@/session'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

type State = 'loading' | 'missing' | 'confirm' | 'done' | 'gone'
const state = ref<State>('loading')
const token = ref<string | null>(null)
const busy = ref(false)
const error = ref('')
const watchID = ref<string | null>(null)
const submitError = ref('')
const siteName = computed(() => session.site?.service_name || '折光观察者')
const accountName = computed(() => session.site?.wechat?.name || siteName.value)

onMounted(async () => {
  token.value = takeToken('bind', route.hash)
  if (route.hash) await router.replace({ path: route.path, query: route.query }) // out of the address bar
  if (!token.value) {
    state.value = 'missing'
    return
  }
  await ensureSession()
  if (!session.user) {
    await router.replace(loginRoute('/wx/bind'))
    return
  }
  state.value = 'confirm'
})

async function bind() {
  if (!token.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const r = await call(api.POST('/api/v1/wechat/bind', { body: { token: token.value } }))
    dropToken('bind')
    watchID.value = r.watch_id ?? null
    submitError.value = r.submit_error ?? ''
    state.value = 'done'
  } catch (e) {
    if (e instanceof ApiError && e.status === 410) {
      dropToken('bind')
      state.value = 'gone'
    } else {
      error.value = messageOf(e)
    }
  } finally {
    busy.value = false
  }
}

async function switchAccount() {
  busy.value = true
  try {
    await call(api.POST('/api/v1/auth/logout'))
  } catch {
    // Logged out here either way.
  }
  setUser(null)
  await router.replace(loginRoute('/wx/bind')) // the token waits in storage
}
</script>

<template>
  <section class="zg-auth">
    <p v-if="state === 'loading'" class="muted" role="status"><span class="spinner" aria-hidden="true" /> 正在打开…</p>
    <p v-else-if="siteName !== accountName" class="muted small wx-origin">「{{ siteName }}」是公众号「{{ accountName }}」配套的文章备份网站</p>

    <template v-if="state === 'confirm' && session.user">
      <h1 class="zg-auth__title">绑定公众号</h1>
      <p class="zg-auth__lede">把你的微信绑定到这个账号：</p>
      <p class="zg-shard zg-panel bind-email">{{ session.user.email }}</p>
      <p class="muted small">
        绑定后，你在公众号「{{ accountName }}」里发送的文章链接会加入这个账号的观察列表；文章被删除时，提醒发到这个邮箱。之后可以在设置里解除绑定。
      </p>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
      <button class="zg-shard zg-btn zg-btn--block" type="button" :disabled="busy" @click="bind">
        <span v-if="busy" class="spinner" aria-hidden="true" />
        绑定
      </button>
      <p class="muted small">不是这个邮箱？<button type="button" class="btn-link" :disabled="busy" @click="switchAccount">换一个账号</button></p>
    </template>

    <template v-else-if="state === 'done'">
      <h1 class="zg-auth__title">已绑定</h1>
      <p class="zg-auth__lede">回到公众号「{{ accountName }}」，把文章链接发过去，就会开始观察。</p>
      <p v-if="watchID" class="notice notice-ok">刚才发送的文章已经开始备份。</p>
      <p v-if="submitError" class="notice notice-danger" role="alert">刚才发送的文章没能开始观察：{{ submitError }}</p>
      <div class="zg-actions">
        <RouterLink v-if="watchID" :to="`/watches/${watchID}`" class="zg-shard zg-btn">查看这篇文章</RouterLink>
        <RouterLink to="/watches" class="zg-shard zg-btn" :class="{ 'zg-btn--ghost': watchID }">我的观察</RouterLink>
      </div>
    </template>

    <template v-else-if="state === 'missing' || state === 'gone'">
      <h1 class="zg-auth__title">链接已失效</h1>
      <p class="zg-auth__lede">
        {{ state === 'gone' ? '绑定链接只能使用一次，并且 30 分钟内有效。' : '这个链接不完整。' }}请回到公众号「{{ accountName }}」，发送「绑定」获取新的链接。
      </p>
      <div class="zg-actions"><RouterLink to="/watches" class="zg-shard zg-btn zg-btn--ghost">我的观察</RouterLink></div>
    </template>
  </section>
</template>

<style scoped>
.wx-origin { margin: 0; }
.bind-email { margin: 0; font: 500 15px/22px var(--font-mono); overflow-wrap: anywhere; }
</style>
