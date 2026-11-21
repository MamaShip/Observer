<script setup lang="ts">
import { api, call, messageOf, type Invitations, type Me } from '@/api/client'
import WechatQR from '@/components/WechatQR.vue'
import ZgIcon from '@/components/ZgIcon.vue'
import { formatDate, formatShort } from '@/lib/format'
import { serviceName, session, setUser } from '@/session'
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const quota = ref<Pick<Me, 'emails_this_month' | 'monthly_emails'> | null>(null)
const invites = ref<Invitations | null>(null)
const wechat = ref<Me['wechat'] | null>(null)
const wechatName = computed(() => session.site?.wechat?.name || serviceName())
const confirmUnbind = ref(false)
const unbinding = ref(false)
const wechatError = ref('')

async function unbind() {
  unbinding.value = true
  wechatError.value = ''
  try {
    await call(api.DELETE('/api/v1/me/wechat'))
    wechat.value = null
    confirmUnbind.value = false
  } catch (e) {
    wechatError.value = messageOf(e)
  } finally {
    unbinding.value = false
  }
}

onMounted(async () => {
  try {
    const me = await call(api.GET('/api/v1/me'))
    setUser(me)
    quota.value = me
    wechat.value = me.wechat ?? null
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
const invitesLeft = computed(() =>
  invites.value && invites.value.monthly_limit > 0 ? Math.max(invites.value.monthly_limit - invites.value.used_this_month, 0) : null,
)
async function invite() {
  if (!invites.value) return
  inviting.value = true
  inviteError.value = ''
  invited.value = ''
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

const revoking = ref('')
async function revoke(id: string) {
  revoking.value = id
  inviteError.value = ''
  try {
    await call(api.DELETE('/api/v1/invites/{id}', { params: { path: { id } } }))
    invited.value = ''
    invites.value = await call(api.GET('/api/v1/invites'))
  } catch (e) {
    inviteError.value = messageOf(e)
  } finally {
    revoking.value = ''
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
      <div class="zg-panel__head"><h2 class="zg-panel__title">通知邮件</h2><span class="zg-meta">每月 1 日 00:00 重置</span></div>
      <div v-if="quota" class="zg-meter" :style="{ '--value': quota.monthly_emails > 0 ? Math.min(100, 100 * quota.emails_this_month / quota.monthly_emails) + '%' : '0%' }">
        <div class="zg-meter__row"><span>本月已收到通知邮件</span><span class="zg-meter__value">{{ quota.emails_this_month }} / {{ quota.monthly_emails || '不限' }} 封</span></div>
        <div v-if="quota.monthly_emails > 0" class="zg-meter__track" role="meter" aria-label="本月通知邮件用量" :aria-valuenow="quota.emails_this_month"
          aria-valuemin="0" :aria-valuemax="quota.monthly_emails"><div class="zg-meter__fill" /></div>
      </div>
      <p class="muted small">文章被删除时会发邮件通知你，几篇文章的通知合并成一封。观察到期时可以在「我的观察」里查看状态。达到邮件上限后，新的变化会在下月初汇总发送。验证码邮件不计入用量。</p>
    </section>

    <section v-if="session.site?.wechat" class="zg-shard zg-panel">
      <div class="zg-panel__head"><h2 class="zg-panel__title">微信公众号</h2>
        <span class="zg-meta">{{ wechat ? `${formatDate(wechat.bound_at)} 绑定` : '未绑定' }}</span></div>
      <template v-if="wechat">
        <p class="muted small">在公众号「{{ wechatName }}」里发送文章链接，就会加入这个账号的观察列表；删除提醒仍然发到这个邮箱。</p>
        <div class="zg-setrow"><span class="zg-setrow__label">解除绑定<span class="zg-setrow__sub">已有的观察不受影响</span></span>
          <button v-if="!confirmUnbind" type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" @click="confirmUnbind = true">解除绑定</button>
          <span v-else class="row">
            <button type="button" class="zg-shard zg-btn zg-btn--danger zg-btn--sm" :disabled="unbinding" @click="unbind">确认解除</button>
            <button type="button" class="zg-shard zg-btn zg-btn--ghost zg-btn--sm" :disabled="unbinding" @click="confirmUnbind = false">取消</button>
          </span>
        </div>
      </template>
      <template v-else>
        <p class="muted small">关注公众号「{{ wechatName }}」，发送「绑定」，再按回复里的链接确认。之后在微信里把文章链接发给公众号，就能开始观察。</p>
        <WechatQR />
      </template>
      <p v-if="wechatError" class="notice notice-danger" role="alert">{{ wechatError }}</p>
    </section>

    <section v-if="invites?.can_invite" class="zg-shard zg-panel">
      <div class="zg-panel__head"><h2 class="zg-panel__title">邀请朋友</h2>
        <span class="zg-meta">{{ invitesLeft !== null ? `剩余 ${invitesLeft} / ${invites.monthly_limit} 个名额` : '管理员不限名额' }}</span></div>
      <p class="muted small">
        填写朋友的邮箱，我们会发送邀请邮件。对方点击邮件中的链接即可注册并登录。
      </p>
      <form class="zg-field invite-form" @submit.prevent="invite">
        <label for="invite-email" class="zg-field__label">朋友的邮箱</label>
        <div class="zg-field__row">
          <div class="zg-shard zg-input zg-input--text"><input id="invite-email" v-model="inviteEmail" type="email" placeholder="name@example.com" autocomplete="off" required></div>
          <button type="submit" class="zg-shard zg-btn" :disabled="inviting || !inviteEmail.trim() || invitesLeft === 0"><ZgIcon name="user-plus" />邀请</button>
        </div>
        <span class="zg-field__hint">
          <template v-if="invitesLeft !== null">本月还可以邀请 {{ invitesLeft }} 人，每月 1 日恢复。</template>
          <template v-else>本月已邀请 {{ invites.used_this_month }} 人。</template>
          对方登录前可以撤回，撤回后名额退还。</span>
      </form>
      <p v-if="inviteError" class="notice notice-danger" role="alert">{{ inviteError }}</p>
      <p v-if="invited" class="notice notice-ok" role="status">已邀请 <strong>{{ invited }}</strong>。首次注册的朋友可以从邮件中接受邀请。</p>
      <div v-if="invites.items.length">
        <h3 class="invite-head">邀请记录</h3>
        <div v-for="i in invites.items" :key="i.id" class="zg-setrow">
          <span class="zg-setrow__label invite-email">{{ i.email }}<span class="zg-setrow__sub">{{ formatShort(i.created_at) }} 邀请</span></span>
          <span class="invite-state">
            <span class="zg-shard zg-tag" :class="i.status === 'accepted' ? 'zg-tag--archived' : 'zg-tag--watching'"><i class="zg-tag__glyph" />{{ i.status === 'accepted' ? '已注册' : '待接受' }}</span>
            <button v-if="i.status === 'pending'" type="button" class="zg-shard zg-btn zg-btn--ghost zg-btn--sm" :disabled="!!revoking"
              :aria-label="'撤回对 ' + i.email + ' 的邀请'" @click="revoke(i.id)">撤回</button>
          </span>
        </div>
      </div>
    </section>

    <section class="zg-shard zg-panel">
      <h2 class="zg-panel__title">登录状态</h2>
      <div class="zg-setrow"><span class="zg-setrow__label">当前设备</span><button type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="loggingOut" @click="logout"><ZgIcon name="log-out" />退出登录</button></div>
    </section>
    <section class="zg-shard zg-panel zg-panel--danger">
      <h2 class="zg-panel__title">删除账号</h2>
      <p class="muted small">删除后，你的邮箱、登录记录和观察列表都会被清除，且无法恢复。文章备份会继续保留，但不再与你关联。</p>
      <div><button ref="deleteButton" class="zg-shard zg-btn zg-btn--danger zg-btn--sm" @click="openDelete">删除账号</button></div>
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
.invite-form { max-width: none; }
.invite-head { margin: 0; padding-bottom: var(--space-2); border-bottom: var(--border-hair) solid var(--line-100); font: 500 12px/18px var(--font-sans); color: var(--ink-300); }
.invite-head + .zg-setrow { padding-top: var(--space-3); }
.invite-email { font-family: var(--font-mono); font-size: 13px; overflow-wrap: anywhere; }
.invite-email .zg-setrow__sub { font-family: var(--font-sans); }
.invite-state { display: flex; align-items: center; gap: var(--space-2); }
</style>
