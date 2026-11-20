<script setup lang="ts">
import { api, call, messageOf, type Me } from '@/api/client'
import { formatDate } from '@/lib/format'
import { session, setUser } from '@/session'
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const quota = ref<Pick<Me, 'emails_this_month' | 'monthly_emails'> | null>(null)

onMounted(async () => {
  try {
    const me = await call(api.GET('/api/v1/me'))
    setUser(me)
    quota.value = me
  } catch {
    // The page works without the count.
  }
})
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
.danger-zone {
  border-color: color-mix(in srgb, var(--danger) 40%, var(--border));
}
.danger-zone h2 {
  color: var(--danger);
}
</style>
