<script setup lang="ts">
import { api, call, messageOf } from '@/api/client'
import { formatDate } from '@/lib/format'
import { session, setUser } from '@/session'
import { ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const saving = ref(false)
const saved = ref(false)
const error = ref('')
const loggingOut = ref(false)
const confirmDelete = ref(false)
const deleteWord = ref('')
const deleting = ref(false)

async function setNotifyOnExpire(e: Event) {
  const checked = (e.target as HTMLInputElement).checked
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    setUser(await call(api.PATCH('/api/v1/me', { body: { notify_on_expire: checked } })))
    saved.value = true
  } catch (err) {
    error.value = messageOf(err)
    ;(e.target as HTMLInputElement).checked = !checked
  } finally {
    saving.value = false
  }
}

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
      <p class="muted small">文章被删除时总会发邮件通知你。几篇文章的通知会合并成一封。</p>
      <label class="toggle">
        <input type="checkbox" :checked="session.user.notify_on_expire" :disabled="saving" @change="setNotifyOnExpire">
        <span>观察期结束、文章没有被删除时，也发邮件告诉我</span>
      </label>
      <p v-if="saved" class="small ok" role="status">已保存</p>
      <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>
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
      </form>
    </section>
  </template>
</template>

<style scoped>
.toggle {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  cursor: pointer;
}
.toggle input {
  width: 20px;
  height: 20px;
  margin-top: 3px;
  flex: none;
  accent-color: var(--accent);
}
.ok {
  color: var(--ok);
  margin: 0;
}
.danger-zone {
  border-color: color-mix(in srgb, var(--danger) 40%, var(--border));
}
.danger-zone h2 {
  color: var(--danger);
}
</style>
