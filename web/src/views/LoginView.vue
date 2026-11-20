<script setup lang="ts">
import { ApiError, api, call, messageOf } from '@/api/client'
import * as storage from '@/lib/storage'
import { safeNext } from '@/router'
import { serviceName, session, setUser } from '@/session'
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
    info.value = `验证码已发送到 ${saved.email}。`
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
  const digits = (e.target as HTMLInputElement).value.replace(/\D/g, '').slice(0, 6)
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
    codeInput.value?.focus()
  } finally {
    verifying.value = false
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
  <div class="login card">
    <h1>登录{{ serviceName() }}</h1>

    <form v-if="step === 'email'" class="stack" novalidate @submit.prevent="requestCode">
      <p class="muted">输入邮箱，我们会发送一个 6 位数字验证码。第一次使用会自动创建账号。</p>
      <p v-if="session.site?.registration_mode === 'invite'" class="notice notice-info">
        目前处于邀请测试阶段，只有受邀的邮箱能收到验证码。已经在用的朋友可以在「设置」里邀请你。
      </p>
      <label class="field">
        <span>邮箱</span>
        <input v-model="email" type="email" name="email" autocomplete="email" inputmode="email" placeholder="you@example.com" required autofocus>
      </label>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
      <button class="btn btn-primary btn-block" type="submit" :disabled="sending">
        <span v-if="sending" class="spinner" aria-hidden="true" />
        获取验证码
      </button>
      <p class="muted small">
        每个邮箱每天最多获取 {{ perDay }} 次验证码。
        <button v-if="emailLooksValid" type="button" class="btn-link small" @click="toCodeStep">已经收到验证码？</button>
      </p>
    </form>

    <form v-else class="stack" novalidate @submit.prevent="verify">
      <p v-if="info" class="notice notice-ok">{{ info }}</p>
      <label class="field">
        <span>验证码</span>
        <input
          ref="codeInput"
          class="code"
          type="text"
          name="code"
          inputmode="numeric"
          autocomplete="one-time-code"
          pattern="[0-9]*"
          maxlength="6"
          placeholder="6 位数字"
          :value="code"
          :disabled="verifying"
          @input="onCodeInput"
        >
      </label>
      <p class="muted small">
        发送到 <strong>{{ email.trim() }}</strong>，{{ ttlMinutes }} 分钟内有效。没收到的话，看看垃圾邮件箱。
      </p>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
      <button class="btn btn-primary btn-block" type="submit" :disabled="verifying || code.length !== 6">
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
  </div>
</template>

<style scoped>
.login {
  max-width: 420px;
  margin: 12px auto 0;
  padding: 24px 20px;
}
.code {
  font-family: var(--mono);
  font-size: 1.5rem !important;
  letter-spacing: 0.4em;
  text-align: center;
}
.code::placeholder {
  letter-spacing: normal;
  font-family: var(--font);
  font-size: 1rem;
}
.between {
  justify-content: space-between;
}
.btn-link:disabled {
  color: var(--muted);
  cursor: default;
  text-decoration: none;
}
</style>
