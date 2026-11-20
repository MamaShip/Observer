<script setup lang="ts">
import { api, call, messageOf, type Invitations, type Me } from '@/api/client'
import { formatDate } from '@/lib/format'
import { serviceName, session, setUser } from '@/session'
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const quota = ref<Pick<Me, 'emails_this_month' | 'monthly_emails'> | null>(null)
const invites = ref<Invitations | null>(null)

onMounted(async () => {
  try {
    const me = await call(api.GET('/api/v1/me'))
    setUser(me)
    quota.value = me
  } catch {
    // The page works without the count.
  }
})
onMounted(async () => {
  try {
    invites.value = await call(api.GET('/api/v1/invites'))
  } catch {
    // Without the list the invitation card stays hidden.
  }
})

const inviteEmail = ref('')
const inviting = ref(false)
const inviteError = ref('')
const invited = ref('') // the address just invited
const copied = ref(false)
const invitesLeft = computed(() =>
  invites.value && invites.value.monthly_limit > 0 ? Math.max(invites.value.monthly_limit - invites.value.used_this_month, 0) : null,
)
const loginURL = `${location.origin}/login`
const inviteText = computed(
  () => `我邀请你使用${serviceName()}：打开 ${loginURL} ，用 ${invited.value} 获取验证码登录即可。`,
)

async function invite() {
  if (!invites.value) return
  inviting.value = true
  inviteError.value = ''
  invited.value = ''
  copied.value = false
  try {
    const r = await call(api.POST('/api/v1/invites', { body: { email: inviteEmail.value.trim() } }))
    invited.value = r.invitation.email
    invites.value.used_this_month = r.used_this_month
    invites.value.items = [r.invitation, ...invites.value.items.filter((i) => i.email !== r.invitation.email)]
    inviteEmail.value = ''
  } catch (e) {
    inviteError.value = messageOf(e)
  } finally {
    inviting.value = false
  }
}

async function copyInvite() {
  try {
    await navigator.clipboard.writeText(inviteText.value)
    copied.value = true
  } catch {
    // Some in-app browsers refuse; the text is on screen to copy by hand.
  }
}
const error = ref('')
const loggingOut = ref(false)
const confirmDelete = ref(false)
const confirmEmail = ref('')
const deleteButton = ref<HTMLButtonElement>()
const emailInput = ref<HTMLInputElement>()
const cancelButton = ref<HTMLButtonElement>()
const deleting = ref(false)

async function logout() {
  loggingOut.value = true
  try {
    await call(api.POST('/api/v1/auth/logout'))
  } catch {
    // The session is gone either way for this browser.
  }
  setUser(null)
  await router.replace('/')
}

const emailMatches = computed(() => !!session.user && confirmEmail.value.trim().toLowerCase() === session.user.email.toLowerCase())
async function openDelete() {
  error.value = ''
  confirmEmail.value = ''
  confirmDelete.value = true
  await nextTick()
  emailInput.value?.focus()
}
async function closeDelete() {
  if (deleting.value) return
  confirmDelete.value = false
  await nextTick()
  deleteButton.value?.focus()
}
function trapFocus(e: KeyboardEvent) {
  if (e.shiftKey && document.activeElement === emailInput.value) {
    e.preventDefault()
    cancelButton.value?.focus()
  } else if (!e.shiftKey && document.activeElement === cancelButton.value) {
    e.preventDefault()
    emailInput.value?.focus()
  }
}
async function deleteAccount() {
  if (!emailMatches.value || deleting.value) return
  deleting.value = true
  error.value = ''
  try {
    await call(api.DELETE('/api/v1/me'))
    setUser(null)
    await router.replace('/')
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="list-page">
    <header class="zg-pagehead"><h1 class="zg-pagehead__title">设置</h1></header>
    <div v-if="session.user" class="zg-stack">
    <section class="zg-shard zg-panel">
      <h2 class="zg-panel__title">账号信息</h2>
      <div>
        <div class="zg-setrow"><span class="zg-setrow__label">邮箱<span class="zg-setrow__sub">接收验证码和删除提醒</span></span><span class="zg-setrow__value">{{ session.user.email }}</span></div>
        <div class="zg-setrow"><span class="zg-setrow__label">注册时间</span><span class="zg-setrow__value">{{ formatDate(session.user.created_at) }}</span></div>
      </div>
    </section>
    <section class="zg-shard zg-panel">
      <div class="zg-panel__head"><h2 class="zg-panel__title">通知邮件</h2><span class="zg-meta">每月 1 日重置</span></div>
      <div v-if="quota" class="zg-meter">
        <div class="zg-meter__row"><span>本月已收到通知邮件</span><span class="zg-meter__value">{{ quota.emails_this_month }} / {{ quota.monthly_emails || '不限' }} 封</span></div>
        <progress v-if="quota.monthly_emails > 0" aria-label="本月通知邮件用量" :max="quota.monthly_emails" :value="quota.emails_this_month" />
      </div>
      <p class="muted small">文章被删除时会发邮件通知你，几篇文章的通知合并成一封。观察到期时可以在「我的观察」里查看状态。达到邮件上限后，新的变化会在下月初汇总发送。</p>
    </section>

    <section v-if="invites?.can_invite" class="zg-shard zg-panel">
      <h2 class="zg-panel__title">邀请朋友</h2>
      <p class="muted small">
        现在只有受邀的邮箱才能登录。填写对方的邮箱后，对方就可以用这个邮箱获取验证码登录。我们不会给对方发邮件，请你自己告诉对方。
      </p>
      <p class="small quota">
        <template v-if="invitesLeft !== null">
          本月还可以邀请 <strong>{{ invitesLeft }}</strong> 人（每月 {{ invites.monthly_limit }} 个名额，1 日重置）。
        </template>
        <template v-else>本月已邀请 {{ invites.used_this_month }} 人，管理员不限名额。</template>
      </p>
      <form class="row invite-form" @submit.prevent="invite">
        <div class="zg-shard zg-input zg-input--text"><input v-model="inviteEmail" type="email" placeholder="对方的邮箱" aria-label="对方的邮箱" autocomplete="off" required></div>
        <button type="submit" class="zg-shard zg-btn" :disabled="inviting || !inviteEmail.trim() || invitesLeft === 0">邀请</button>
      </form>
      <p v-if="inviteError" class="notice notice-danger" role="alert">{{ inviteError }}</p>
      <div v-if="invited" class="notice notice-ok stack" role="status">
        <p>已邀请 <strong>{{ invited }}</strong>。把下面这段话发给对方：</p>
        <p class="invite-text">{{ inviteText }}</p>
        <p><button type="button" class="zg-shard zg-btn zg-btn--secondary" @click="copyInvite">{{ copied ? '已复制' : '复制' }}</button></p>
      </div>
      <details v-if="invites.items.length" class="small">
        <summary>我邀请过的邮箱（{{ invites.items.length }}）</summary>
        <ul class="invite-list">
          <li v-for="i in invites.items" :key="i.email">
            {{ i.email }} <span class="muted">{{ formatDate(i.created_at) }}</span>
          </li>
        </ul>
      </details>
    </section>

    <section class="zg-shard zg-panel">
      <h2 class="zg-panel__title">登录状态</h2>
      <div class="zg-setrow"><span class="zg-setrow__label">退出当前设备</span><button type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="loggingOut" @click="logout">退出登录</button></div>
    </section>
    <section class="zg-shard zg-panel zg-panel--danger">
      <h2 class="zg-panel__title">删除账号</h2>
      <p class="muted small">删除后，你的邮箱、登录记录和观察列表都会被清除，且无法恢复。文章备份会继续保留，但不再与你关联。</p>
      <div><button ref="deleteButton" class="zg-shard zg-btn zg-btn--danger zg-btn--sm" @click="openDelete">删除账号…</button></div>
    </section>
    <Teleport to="body">
      <div v-if="confirmDelete" class="zg-scrim" @keydown.esc="closeDelete" @keydown.tab="trapFocus">
        <form class="zg-shard zg-dialog" role="dialog" aria-modal="true" aria-labelledby="delete-title" @submit.prevent="deleteAccount">
          <h2 id="delete-title" class="zg-dialog__title">永久删除账号？</h2>
          <p class="zg-dialog__body">你的账号和观察记录将被清除，无法恢复。请输入当前账号的邮箱确认。</p>
          <label class="zg-field"><span class="zg-field__label">{{ session.user.email }}</span>
            <span class="zg-shard zg-input zg-input--text"><input ref="emailInput" v-model="confirmEmail" type="email" autocomplete="off" aria-label="输入当前账号邮箱"></span>
          </label>
          <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
          <div class="zg-dialog__actions">
            <button class="zg-shard zg-btn zg-btn--danger" type="submit" :disabled="!emailMatches || deleting">永久删除账号</button>
            <button ref="cancelButton" class="zg-shard zg-btn zg-btn--secondary" type="button" @click="closeDelete">取消</button>
          </div>
        </form>
      </div>
    </Teleport>
    </div>
  </div>
</template>
<style scoped>
progress { width: 100%; height: 4px; appearance: none; background: var(--surface-200); color: var(--brand); border: none; }
progress::-webkit-progress-bar { background: var(--surface-200); }
progress::-webkit-progress-value { background: var(--brand); }
progress::-moz-progress-bar { background: var(--brand); }
.invite-form { flex-wrap: nowrap; }
.invite-text { user-select: all; }
.invite-list { list-style: none; margin: 0; padding: 0; }
.invite-list li { display: flex; justify-content: space-between; gap: 8px; padding: var(--space-2) 0; border-bottom: 1px solid var(--line-100); font-family: var(--font-mono); }
</style>
