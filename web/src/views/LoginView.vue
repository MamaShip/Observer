<script setup lang="ts">
import { ApiError, api, call, messageOf } from '@/api/client'
import * as storage from '@/lib/storage'
import { safeNext } from '@/router'
import { session, setUser } from '@/session'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

// Login state survives a reload: on phones the page is often reloaded
// while the user switches to the mail app to read the code.
interface Saved {
  email: string
  sentAt: number
  resendAt: number
  remaining: number | null
}
const storageKey = 'login'

const route = useRoute()
const router = useRouter()

const step = ref<'email' | 'code'>('email')
const email = ref('')
const code = ref('')
const sending = ref(false)
const verifying = ref(false)
const error = ref('')
const info = ref('')
const remaining = ref<number | null>(null)
const resendAt = ref(0)
const now = ref(Date.now())
const codeInput = ref<HTMLInputElement>()

const ttlMinutes = computed(() => session.site?.code_ttl_minutes ?? 10)
const perDay = computed(() => session.site?.codes_per_day ?? 2)
const wait = computed(() => Math.max(0, Math.ceil((resendAt.value - now.value) / 1000)))
const emailLooksValid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value.trim()))

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => (now.value = Date.now()), 1000)
  const saved = storage.load<Saved>(storageKey)
  if (saved && Date.now() - saved.sentAt < ttlMinutes.value * 60_000) {
    email.value = saved.email
    resendAt.value = saved.resendAt
    remaining.value = saved.remaining
    step.value = 'code'
    info.value = '如果该邮箱可以登录，几分钟内会收到验证码。'
  }
})
onBeforeUnmount(() => clearInterval(timer))

function persist() {
  storage.save(storageKey, {
    email: email.value.trim(),
    sentAt: Date.now(),
    resendAt: resendAt.value,
    remaining: remaining.value,
  } satisfies Saved)
}

async function toCodeStep() {
  step.value = 'code'
  code.value = ''
  await nextTick()
  codeInput.value?.focus()
}

async function requestCode() {
  error.value = ''
  if (!emailLooksValid.value) {
    error.value = '请输入正确的邮箱地址。'
    return
  }
  sending.value = true
  try {
    const r = await call(api.POST('/api/v1/auth/code', { body: { email: email.value.trim() } }))
    remaining.value = r.remaining
    now.value = Date.now()
    resendAt.value = now.value + r.resend_after * 1000
    info.value = r.message
    persist()
    await toCodeStep()
  } catch (e) {
    error.value = messageOf(e)
    if (e instanceof ApiError && e.status === 429 && e.retryAfter) {
      // A code was sent moments ago (another tab, or a double tap).
      now.value = Date.now()
      resendAt.value = now.value + e.retryAfter * 1000
      if (step.value === 'email') {
        persist()
        await toCodeStep()
      }
    }
  } finally {
    sending.value = false
  }
}

function onCodeInput(e: Event) {
  const input = e.target as HTMLInputElement
  const digits = input.value.replace(/\D/g, '').slice(0, 6)
  input.value = digits
  code.value = digits
  if (digits.length === 6 && !verifying.value) void verify()
}

async function verify() {
  error.value = ''
  if (!/^\d{6}$/.test(code.value)) {
    error.value = '请输入邮件里的 6 位数字验证码。'
    return
  }
  verifying.value = true
  try {
    const r = await call(api.POST('/api/v1/auth/verify', { body: { email: email.value.trim(), code: code.value } }))
    storage.remove(storageKey)
    setUser(r.user)
    await router.replace(safeNext(route.query.next))
  } catch (e) {
    error.value = messageOf(e)
    code.value = ''
  } finally {
    verifying.value = false
    if (error.value) {
      await nextTick()
      codeInput.value?.focus()
    }
  }
}

function changeEmail() {
  storage.remove(storageKey)
  step.value = 'email'
  error.value = ''
  info.value = ''
  code.value = ''
  remaining.value = null
}
</script>

<template>
  <div class="login-layout">
  <section class="zg-auth">
    <h1 class="zg-auth__title">邮箱登录</h1>

    <form v-if="step === 'email'" class="zg-stack" novalidate @submit.prevent="requestCode">
      <p class="zg-auth__lede">输入邮箱，我们会发送一个 6 位数字验证码。第一次使用会自动创建账号。</p>

      <label class="zg-field">
        <span class="zg-field__label">邮箱</span>
        <span class="zg-shard zg-input zg-input--text"><input v-model="email" type="email" name="email" autocomplete="email" inputmode="email" placeholder="you@example.com" required autofocus></span>
      </label>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
      <button class="zg-shard zg-btn zg-btn--block" type="submit" :disabled="sending">
        <span v-if="sending" class="spinner" aria-hidden="true" />
        获取验证码
      </button>
      <p class="muted small">
        每个邮箱每天最多获取 {{ perDay }} 次验证码。
        <button v-if="emailLooksValid" type="button" class="btn-link" @click="toCodeStep">已经收到验证码？</button>
      </p>
    </form>

    <form v-else class="zg-stack" novalidate @submit.prevent="verify">
      <p v-if="info" class="notice notice-ok">{{ info }}</p>
      <label class="zg-field">
        <span class="zg-field__label">验证码</span>
        <span class="zg-code" :class="{ 'is-invalid': error }">
          <span v-for="n in 6" :key="n" aria-hidden="true" class="zg-shard zg-code__cell" :class="{ current: n === Math.min(code.length + 1, 6) }">{{ code[n - 1] }}</span>
        <input
          ref="codeInput"
          class="code"
          type="text"
          name="code"
          inputmode="numeric"
          autocomplete="one-time-code"
          pattern="[0-9]*"
          :aria-invalid="!!error"
          placeholder="6 位数字"
          :value="code"
          :disabled="verifying"
          @input="onCodeInput"
        ></span>
      </label>
      <p class="muted small">
        发送到 <strong>{{ email.trim() }}</strong>，{{ ttlMinutes }} 分钟内有效。没收到的话，看看垃圾邮件箱。
      </p>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
      <button class="zg-shard zg-btn zg-btn--block" type="submit" :disabled="verifying || code.length !== 6">
        <span v-if="verifying" class="spinner" aria-hidden="true" />
        登录
      </button>
      <div class="row between small">
        <button type="button" class="btn-link" @click="changeEmail">换一个邮箱</button>
        <span v-if="remaining === 0" class="muted">今天的验证码次数已用完</span>
        <button v-else type="button" class="btn-link" :disabled="sending || wait > 0" @click="requestCode">
          {{ wait > 0 ? `重新获取（${wait} 秒）` : '重新获取验证码' }}
        </button>
      </div>
      <p v-if="remaining !== null && remaining > 0" class="muted small">今天这个邮箱还可以获取 {{ remaining }} 次验证码。</p>
    </form>
    <p class="zg-auth__fine">登录即表示你同意<RouterLink to="/terms">服务条款</RouterLink>和<RouterLink to="/privacy">隐私说明</RouterLink>。</p>
  </section>
  <aside class="next-steps zg-stack" aria-label="登录后的使用步骤">
    <span class="zg-hero__kicker">WHAT HAPPENS NEXT</span>
    <ol>
      <li v-for="(step, i) in ['进入「我的观察」，粘贴公众号文章链接。', '我们保存当前版本，并开始定期检查。', '确认原文被删除后，提醒会发到这个邮箱。']" :key="step" class="zg-shard zg-step">
        <span class="zg-step__n">0{{ i + 1 }}</span>{{ step }}
      </li>
    </ol>
      <p v-if="session.site?.registration_mode === 'invite'" class="notice notice-info">
        目前处于邀请测试阶段，只有受邀的邮箱能收到验证码。已经在用的朋友可以在「设置」里邀请你。
      </p>
  </aside>
  </div>
</template>

<style scoped>
.login-layout { display: grid; gap: var(--space-8); grid-template-columns: 1fr 1fr; align-items: start; }
.next-steps { max-width: 420px; padding: var(--space-8) 0; }
.next-steps ol { list-style: none; padding: 0; margin: 0; display: grid; gap: var(--space-3); }
.next-steps li { padding: var(--space-4); font: 400 14px/22px var(--font-sans); color: var(--ink-200); }
.next-steps > * { margin: 0; }
.between { justify-content: space-between; }
.zg-code { position: relative; }
.zg-code__cell { min-width: 0; flex-shrink: 1; }
.zg-code input { position: absolute; inset: 0; width: 100%; opacity: 0; cursor: text; }
.zg-code:focus-within .current { outline: 2px solid var(--focus-ring); outline-offset: 2px; }
@media (max-width: 767px) {
  .login-layout { grid-template-columns: minmax(0, 1fr); gap: var(--space-5); }
  .zg-auth { padding: var(--space-6) 0 0; }
  .next-steps { padding: var(--space-4) 0; }
}
</style>
