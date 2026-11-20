<script setup lang="ts">
import { api, call, messageOf, type AdminUser } from '@/api/client'
import AdminNav from '@/components/admin/AdminNav.vue'
import { formatDate, formatRelative } from '@/lib/format'
import { session } from '@/session'
import { onMounted, ref } from 'vue'

const items = ref<AdminUser[]>([])
const next = ref<string | undefined>()
const search = ref('')
const invitedBy = ref<AdminUser | null>(null) // show only the accounts this user's invitations created
const loading = ref(false)
const error = ref('')
const notice = ref('')
const inviteEmail = ref('')
const inviting = ref(false)
const open = ref<string | null>(null) // the row whose actions are shown
const limitInput = ref<number | null>(null)
const busy = ref(false)

async function load(more = false) {
  loading.value = true
  error.value = ''
  try {
    const page = await call(
      api.GET('/api/v1/admin/users', {
        params: {
          query: {
            search: search.value.trim() || undefined,
            invited_by: invitedBy.value?.id,
            cursor: more ? next.value : undefined,
            limit: 50,
          },
        },
      }),
    )
    items.value = more ? [...items.value, ...page.items] : page.items
    next.value = page.next_cursor
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => load())

async function invite() {
  inviting.value = true
  error.value = ''
  notice.value = ''
  try {
    const req = api.POST('/api/v1/admin/users', { body: { email: inviteEmail.value.trim() } })
    const u = await call(req) // throws with the server's message on failure
    const created = (await req).response.status === 201
    notice.value = created ? `已邀请 ${u.email}，对方现在可以用验证码登录。` : `${u.email} 已经有账户。`
    inviteEmail.value = ''
    await load()
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    inviting.value = false
  }
}

function showInvitees(u: AdminUser | null) {
  invitedBy.value = u
  search.value = ''
  open.value = null
  void load()
}

function showInviter(u: AdminUser) {
  if (!u.invited_by) return
  invitedBy.value = null
  search.value = u.invited_by
  open.value = null
  void load()
}

function toggle(u: AdminUser) {
  open.value = open.value === u.id ? null : u.id
  limitInput.value = u.monthly_emails
}

type Patch = { status?: 'active' | 'disabled'; role?: 'user' | 'admin'; monthly_emails?: number; default_email_limit?: boolean }

async function patch(u: AdminUser, body: Patch, done: string) {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    const updated = await call(api.PATCH('/api/v1/admin/users/{id}', { params: { path: { id: u.id } }, body }))
    items.value = items.value.map((x) => (x.id === u.id ? updated : x))
    limitInput.value = updated.monthly_emails
    notice.value = `${u.email}：${done}`
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    busy.value = false
  }
}

function saveLimit(u: AdminUser) {
  const n = limitInput.value
  if (n === null || !Number.isInteger(n) || n < 0) {
    error.value = '上限是 0 或正整数（0 表示不限）。'
    return
  }
  void patch(u, { monthly_emails: n }, n === 0 ? '每月通知邮件不限' : `每月通知邮件上限改为 ${n}`)
}

const isSelf = (u: AdminUser) => u.id === session.user?.id
</script>

<template>
  <AdminNav />

  <div class="toolbar">
    <form class="row" role="search" @submit.prevent="load()">
      <input v-model="search" type="text" placeholder="按邮箱搜索" aria-label="按邮箱搜索">
      <button type="submit" class="btn">搜索</button>
    </form>
    <form class="row" @submit.prevent="invite">
      <input v-model="inviteEmail" type="email" placeholder="邀请邮箱" aria-label="邀请邮箱" required>
      <button type="submit" class="btn btn-primary" :disabled="inviting || !inviteEmail.trim()">邀请</button>
    </form>
  </div>

  <p v-if="invitedBy" class="notice notice-info filter">
    只看 <strong>{{ invitedBy.email }}</strong> 邀请注册的账户。
    <button type="button" class="btn-link" @click="showInvitees(null)">显示全部</button>
  </p>
  <p v-if="notice" class="notice notice-ok" role="status">{{ notice }}</p>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }}</p>

  <div class="table-wrap">
    <table class="small">
      <thead>
        <tr>
          <th>账户</th>
          <th class="num">观察中 / 累计</th>
          <th class="num">本月提交</th>
          <th class="num">本月通知</th>
          <th>邀请人</th>
          <th class="num" title="邀请注册的账户数（本月发出的邀请数）">邀请</th>
          <th>最近登录</th>
          <th>注册</th>
          <th />
        </tr>
      </thead>
      <tbody>
        <template v-for="u in items" :key="u.id">
          <tr :class="{ disabled: u.status === 'disabled' }">
            <td>
              <span class="email">{{ u.email }}</span>
              <span v-if="u.role === 'admin'" class="tag">管理员</span>
              <span v-if="u.status === 'disabled'" class="tag danger">已停用</span>
            </td>
            <td class="num">{{ u.open_watches }} / {{ u.total_watches }}</td>
            <td class="num">{{ u.watches_this_month }}</td>
            <td class="num" :title="u.custom_email_limit ? '为该账户单独设置的上限' : '默认上限'">
              {{ u.emails_this_month }} / {{ u.monthly_emails || '不限' }}<span v-if="u.custom_email_limit">*</span>
            </td>
            <td>
              <button v-if="u.invited_by" type="button" class="btn-link" :title="`查看 ${u.invited_by}`" @click="showInviter(u)">
                {{ u.invited_by }}
              </button>
              <span v-else-if="u.invited" class="muted">已注销的账户</span>
              <span v-else class="muted">—</span>
            </td>
            <td class="num" :title="`本月发出 ${u.invites_this_month} 个邀请`">
              <button v-if="u.invitees" type="button" class="btn-link" @click="showInvitees(u)">{{ u.invitees }}</button>
              <template v-else>0</template>
              <span v-if="u.invites_this_month" class="muted">（{{ u.invites_this_month }}）</span>
            </td>
            <td>{{ u.last_login_at ? formatRelative(u.last_login_at) : '从未' }}</td>
            <td>{{ formatDate(u.created_at) }}</td>
            <td><button type="button" class="btn-link" :aria-expanded="open === u.id" @click="toggle(u)">管理</button></td>
          </tr>
          <tr v-if="open === u.id" class="actions-row">
            <td colspan="9">
              <div class="actions">
                <form class="row" @submit.prevent="saveLimit(u)">
                  <label class="row">每月通知邮件上限
                    <input v-model.number="limitInput" type="number" min="0" step="1" class="limit">
                  </label>
                  <button type="submit" class="btn" :disabled="busy">保存</button>
                  <button v-if="u.custom_email_limit" type="button" class="btn" :disabled="busy"
                    @click="patch(u, { default_email_limit: true }, '恢复默认上限')">恢复默认</button>
                </form>
                <div class="row">
                  <template v-if="!isSelf(u)">
                    <button v-if="u.status === 'active'" type="button" class="btn btn-danger" :disabled="busy"
                      @click="patch(u, { status: 'disabled' }, '已停用，并退出了所有设备')">停用账户</button>
                    <button v-else type="button" class="btn" :disabled="busy" @click="patch(u, { status: 'active' }, '已启用')">启用账户</button>
                    <button v-if="u.role === 'admin'" type="button" class="btn" :disabled="busy"
                      @click="patch(u, { role: 'user' }, '已取消管理员')">取消管理员</button>
                    <button v-else type="button" class="btn" :disabled="busy" @click="patch(u, { role: 'admin' }, '已设为管理员')">设为管理员</button>
                  </template>
                  <span v-else class="muted">这是你自己的账户。</span>
                </div>
              </div>
            </td>
          </tr>
        </template>
        <tr v-if="!items.length && !loading">
          <td colspan="9" class="muted center">没有账户。</td>
        </tr>
      </tbody>
    </table>
  </div>
  <p class="muted small">
    * 为该账户单独设置的上限。「邀请」是该账户邀请注册的账户数，括号里是本月发出的邀请数（包括邀请已有账户的邮箱）。「—」表示由命令行创建，或在记录邀请关系之前注册。本月数字按北京时间自然月统计。
  </p>
  <div v-if="next" class="center">
    <button type="button" class="btn" :disabled="loading" @click="load(true)">加载更多</button>
  </div>
</template>

<style scoped>
.filter {
  margin-bottom: 12px;
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 12px;
}
.toolbar input {
  width: 240px;
  max-width: 100%;
}
.toolbar form {
  flex-wrap: nowrap;
}
.table-wrap {
  overflow-x: auto;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
table {
  border-collapse: collapse;
  width: 100%;
}
th,
td {
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  text-align: left;
  white-space: nowrap;
}
th {
  color: var(--muted);
  font-weight: 600;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
tr.disabled td {
  color: var(--muted);
}
.email {
  font-weight: 600;
}
.tag {
  display: inline-block;
  margin-left: 6px;
  padding: 0 6px;
  border-radius: 999px;
  font-size: 0.75rem;
  background: var(--accent-soft);
  color: var(--accent);
}
.tag.danger {
  background: var(--danger-soft);
  color: var(--danger);
}
.actions-row td {
  background: var(--bg);
  white-space: normal;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 24px;
  justify-content: space-between;
}
.limit {
  width: 90px;
  padding: 6px 8px;
}
</style>
