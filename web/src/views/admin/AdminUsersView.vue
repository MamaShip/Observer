<script setup lang="ts">
import { api, call, messageOf, type AdminUser } from '@/api/client'
import { formatDate, formatRelative } from '@/lib/format'
import { session } from '@/session'
import { useAdminPages } from '@/lib/adminPages'
import { accountSource, linkLabel } from '@/lib/admin'
import ResultPager from '@/components/admin/ResultPager.vue'
import ZgIcon from '@/components/ZgIcon.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const search = ref('')
const route = useRoute()
const router = useRouter()
const invitedBy = ref<AdminUser | null>(null) // show only the accounts this user's invitations created
// Or only those a public invitation link created (from the links page).
const inviteLink = ref(typeof route.query.invite_link === 'string' ? route.query.invite_link : '')
const filter = ref('all')
const appliedSearch = ref('')
const inviteOpen = ref(false)
const notice = ref('')
const inviteEmail = ref('')
const inviting = ref(false)
const open = ref<string | null>(null) // the row whose actions are shown
const limitInput = ref<number | null>(null)
const busy = ref(false)

const { items, loading, error, page, hasNext, go, reset, replace, matchCount } = useAdminPages<AdminUser>(
  cursor => call(api.GET('/api/v1/admin/users', { params: { query: {
    search: appliedSearch.value || undefined, invited_by: invitedBy.value?.id, invite_link: inviteLink.value || undefined, cursor, limit: 50,
  } } })),
  u => filter.value === 'all' || u.status === filter.value || (filter.value === 'admin' && u.role === 'admin'),
)
const selected = computed(() => items.value.find(u => u.id === open.value))
function load() {
  appliedSearch.value = search.value.trim()
  open.value = null
  return reset()
}
watch(filter, load, { immediate: true })

async function invite() {
  inviting.value = true
  error.value = ''
  notice.value = ''
  try {
    await call(api.POST('/api/v1/admin/users', { body: { email: inviteEmail.value.trim() } }))
    notice.value = '邀请已处理。首次注册的用户可以从邮件中接受邀请。'
    inviteOpen.value = false
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
  setLink('')
  search.value = ''
  void load()
}

function showLink(id: string) {
  invitedBy.value = null
  setLink(id)
  search.value = ''
  void load()
}

// setLink keeps the link filter in the address, so the links page can link here.
function setLink(id: string) {
  inviteLink.value = id
  if ((route.query.invite_link ?? '') !== id) void router.replace({ query: id ? { invite_link: id } : {} })
}

const linkFilterNote = computed(() => items.value.find(u => u.invite_link_id === inviteLink.value)?.invite_link_note)

function showInviter(u: AdminUser) {
  if (!u.invited_by) return
  invitedBy.value = null
  setLink('')
  search.value = u.invited_by
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
    replace(x => x.id === u.id, updated)
    if (open.value === u.id) limitInput.value = updated.monthly_emails
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
  <header class="zg-pagehead"><div><h1 class="zg-pagehead__title">账户管理</h1>
    <div v-if="session.site" class="zg-pagehead__meta">{{ session.site.registration_mode === 'invite' ? '邀请制已开启' : '开放注册' }}</div></div>
    <button type="button" class="zg-shard zg-btn" :aria-expanded="inviteOpen" @click="inviteOpen = !inviteOpen"><ZgIcon name="user-plus" />邀请用户</button>
  </header>
  <form v-if="inviteOpen" class="zg-toolbar" @submit.prevent="invite">
    <label class="zg-shard zg-input zg-input--text"><input v-model="inviteEmail" type="email" placeholder="邀请邮箱" aria-label="邀请邮箱" required></label>
    <button class="zg-shard zg-btn zg-btn--sm" type="submit" :disabled="inviting || !inviteEmail.trim()">确认邀请</button>
  </form>
  <p v-if="notice" class="notice notice-ok" role="status">{{ notice }}</p>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="load">刷新列表</button></p>
  <p v-if="invitedBy" class="notice notice-info">只看 {{ invitedBy.email }} 邀请注册的账户。 <button class="btn-link" @click="showInvitees(null)">显示全部</button></p>
  <p v-else-if="inviteLink" class="notice notice-info">只看通过公开邀请{{ linkFilterNote ? `「${linkFilterNote}」` : '链接' }}注册的账户。 <button class="btn-link" @click="showInvitees(null)">显示全部</button></p>
  <div class="zg-detail">
    <section>
      <form class="zg-toolbar" role="search" @submit.prevent="load">
        <label class="zg-shard zg-input zg-input--search"><input v-model="search" type="search" placeholder="搜索邮箱" aria-label="搜索邮箱"></label>
        <button class="zg-shard zg-btn zg-btn--secondary zg-btn--sm">搜索</button>
        <label class="zg-shard zg-select zg-select--sm"><select v-model="filter" aria-label="账号状态"><option value="all">全部状态</option><option value="active">启用</option><option value="disabled">停用</option><option value="admin">管理员</option></select>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
        </label>
        <span v-if="matchCount !== undefined" class="zg-toolbar__count">匹配 {{ matchCount }} 个</span>
      </form>
      <p v-if="loading" class="muted" role="status">加载中…</p>
      <div class="zg-shard zg-tablewrap"><div class="zg-tablewrap__scroll"><table class="zg-table">
        <thead><tr><th class="pin">邮箱</th><th>来源</th><th class="fit">状态</th><th class="is-num fit">观察中</th><th class="is-num fit">本月通知</th><th class="fit">注册</th></tr></thead>
        <tbody><tr v-for="u in items" :key="u.id" :class="{ chosen: open === u.id }" @click="toggle(u)">
          <td class="pin"><button class="btn-link mono clip" :title="u.email" :aria-expanded="open === u.id" @click.stop="toggle(u)">{{ u.email }}</button><span v-if="u.role === 'admin'" class="zg-table__sub">管理员</span></td>
          <td><span class="clip source" :title="accountSource(u)">{{ accountSource(u) }}</span></td>
          <td class="fit"><span class="zg-shard zg-tag" :class="u.status === 'active' ? 'zg-tag--archived' : 'zg-tag--ended'"><i class="zg-tag__glyph" />{{ u.status === 'active' ? '启用' : '停用' }}</span></td>
          <td class="is-num fit">{{ u.open_watches }}</td><td class="is-num fit">{{ u.emails_this_month }} / {{ u.monthly_emails || '不限' }}{{ u.custom_email_limit ? '*' : '' }}</td><td class="is-mono fit">{{ formatDate(u.created_at) }}</td>
        </tr><tr v-if="!items.length && !loading && !error"><td colspan="6">没有符合条件的账户。</td></tr></tbody>
      </table></div></div>
      <ResultPager :page="page" :has-next="hasNext" :loading="loading" @go="go" />
    </section>
    <aside class="zg-shard zg-panel">
      <h2 class="zg-panel__title">{{ selected?.email || '账户详情' }}</h2>
      <p v-if="!selected" class="muted small">选择列表中的邮箱，查看邀请关系、用量和权限。</p>
      <template v-else>
        <dl class="zg-facts">
          <dt>来源</dt><dd><button v-if="selected.invite_link_id" class="btn-link" @click="showLink(selected.invite_link_id)">{{ linkLabel(selected) }}</button><button v-else-if="selected.invited_by" class="btn-link" @click="showInviter(selected)">{{ selected.invited_by }}</button><template v-else>{{ selected.invited ? '已注销的账户' : '—' }}</template></dd>
          <dt>邀请注册</dt><dd><button v-if="selected.invitees" class="btn-link" @click="showInvitees(selected)">{{ selected.invitees }} 人</button><template v-else>0 人</template></dd>
          <dt>本月邀请</dt><dd>{{ selected.invites_this_month }} 人</dd><dt>累计观察</dt><dd>{{ selected.total_watches }} 篇</dd>
          <dt>本月提交</dt><dd>{{ selected.watches_this_month }} 篇</dd><dt>最近登录</dt><dd>{{ selected.last_login_at ? formatRelative(selected.last_login_at) : '从未' }}</dd>
          <template v-if="session.site?.wechat"><dt>公众号</dt><dd>{{ selected.wechat_bound_at ? formatDate(selected.wechat_bound_at) + ' 绑定' : '未绑定' }}</dd></template>
        </dl>
        <div>
          <label class="zg-setrow zg-switch">启用账户
            <input type="checkbox" :checked="selected.status === 'active'" :disabled="busy || isSelf(selected)" @click.prevent="patch(selected, { status: selected.status === 'active' ? 'disabled' : 'active' }, '账户状态已更新')"><span class="zg-switch__track" />
          </label>
          <label class="zg-setrow zg-switch">管理员权限
            <input type="checkbox" :checked="selected.role === 'admin'" :disabled="busy || isSelf(selected)" @click.prevent="patch(selected, { role: selected.role === 'admin' ? 'user' : 'admin' }, '账户权限已更新')"><span class="zg-switch__track" />
          </label>
          <span v-if="isSelf(selected)" class="zg-field__hint">这是你自己的账户。</span>
        </div>
        <form class="zg-field" @submit.prevent="saveLimit(selected)">
          <label for="monthly-limit" class="zg-field__label">每月通知邮件上限</label>
          <div class="zg-shard zg-input"><input id="monthly-limit" v-model.number="limitInput" type="number" min="0" step="1"></div>
          <span class="zg-field__hint">{{ selected.custom_email_limit ? '该账户使用单独设置的上限。' : '该账户使用默认上限。' }}0 表示不限。</span>
          <div class="row"><button class="zg-shard zg-btn zg-btn--sm" :disabled="busy">保存</button>
            <button v-if="selected.custom_email_limit" type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" :disabled="busy" @click="patch(selected, { default_email_limit: true }, '恢复默认上限')">恢复默认</button>
          </div>
        </form>
      </template>
    </aside>
  </div>
</template>
<style scoped>
.zg-table tbody tr { cursor: pointer; }
.chosen { background: var(--status-watching-soft); }
/* Narrow columns shrink to their content; 来源 takes what is left. */
.zg-table th, .zg-table td { padding-inline: var(--space-3); }
.fit { width: 1%; white-space: nowrap; }
.clip { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; }
.pin .clip { max-width: 12em; }
.source { max-width: 11em; color: var(--ink-200); }
/* When the rows scroll sideways the email stays in view. */
.pin { position: sticky; left: 0; z-index: 1; background: var(--surface-100); }
tbody tr:hover > .pin { background: var(--surface-200); }
tr.chosen > .pin { background: var(--status-watching-soft); }
@media (max-width: 767px) {
  .pin { box-shadow: inset -1px 0 0 var(--line-100); }
  .pin .clip, .source { max-width: 10em; }
}
@media (min-width: 768px) { .zg-detail { grid-template-columns: minmax(0, 1fr) 300px; } }
</style>
