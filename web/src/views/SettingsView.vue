<script setup lang="ts">
import { api, call, messageOf, type Invitations, type Me } from '@/api/client'
import { formatDate } from '@/lib/format'
import { serviceName, session, setUser } from '@/session'
import { computed, onMounted, ref } from 'vue'
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
const deleteWord = ref('')
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

async function deleteAccount() {
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
  <h1>设置</h1>
  <template v-if="session.user">
    <section class="card stack">
      <h2>账号</h2>
      <p>
        <span class="muted">邮箱</span><br>
        <strong>{{ session.user.email }}</strong>
      </p>
      <p class="muted small">注册于 {{ formatDate(session.user.created_at) }}</p>
      <button type="button" class="btn" :disabled="loggingOut" @click="logout">退出登录</button>
    </section>

    <section class="card stack">
      <h2>邮件通知</h2>
      <p class="muted small">
        文章被删除时会发邮件通知你，几篇文章的通知合并成一封。观察期结束时不发邮件，随时可以在「我的观察」里查看状态。
      </p>
      <p v-if="quota" class="small quota">
        <template v-if="quota.monthly_emails > 0">
          本月已收到 <strong>{{ quota.emails_this_month }}</strong> / {{ quota.monthly_emails }} 封通知邮件（每月 1 日重置）。达到上限后，新的变化会在下月初汇总成一封发送，网站上随时可以查看。
        </template>
        <template v-else>本月已收到 {{ quota.emails_this_month }} 封通知邮件。</template>
      </p>
    </section>

    <section v-if="invites?.can_invite" class="card stack">
      <h2>邀请朋友</h2>
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
        <input v-model="inviteEmail" type="email" placeholder="对方的邮箱" aria-label="对方的邮箱" autocomplete="off" required>
        <button type="submit" class="btn btn-primary" :disabled="inviting || !inviteEmail.trim() || invitesLeft === 0">邀请</button>
      </form>
      <p v-if="inviteError" class="notice notice-danger" role="alert">{{ inviteError }}</p>
      <div v-if="invited" class="notice notice-ok stack" role="status">
        <p>已邀请 <strong>{{ invited }}</strong>。把下面这段话发给对方：</p>
        <p class="invite-text">{{ inviteText }}</p>
        <p><button type="button" class="btn" @click="copyInvite">{{ copied ? '已复制' : '复制' }}</button></p>
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

    <section class="card stack danger-zone">
      <h2>删除账号</h2>
      <p class="muted small">
        删除后，你的邮箱、登录记录和观察列表都会被清除，且无法恢复。文章备份是公共存档，不会随账号删除，但也不再与你关联。
      </p>
      <button v-if="!confirmDelete" type="button" class="btn btn-danger" @click="confirmDelete = true">删除账号…</button>
      <form v-else class="stack" @submit.prevent="deleteAccount">
        <label class="field">
          <span>请输入「删除」两个字确认</span>
          <input v-model="deleteWord" type="text" autocomplete="off">
        </label>
        <div class="row">
          <button type="submit" class="btn btn-danger solid" :disabled="deleteWord.trim() !== '删除' || deleting">永久删除账号</button>
          <button type="button" class="btn" @click="confirmDelete = false; deleteWord = ''">取消</button>
        </div>
        <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
      </form>
    </section>
  </template>
</template>

<style scoped>
.quota {
  margin: 0;
}
.invite-form {
  flex-wrap: nowrap;
}
.invite-form input {
  flex: 1;
  min-width: 0;
}
.invite-text {
  user-select: all;
  word-break: break-all;
}
.invite-list {
  margin: 8px 0 0;
  padding-left: 1.2em;
}
.danger-zone {
  border-color: color-mix(in srgb, var(--danger) 40%, var(--border));
}
.danger-zone h2 {
  color: var(--danger);
}
</style>
