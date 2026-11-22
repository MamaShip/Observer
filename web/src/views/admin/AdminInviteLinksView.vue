<script setup lang="ts">
import { api, call, messageOf, type InviteLink } from '@/api/client'
import ZgIcon from '@/components/ZgIcon.vue'
import { formatDate, formatDateTime } from '@/lib/format'
import { session } from '@/session'
import { computed, onMounted, ref } from 'vue'

// Public invitation links (plan.md, 6.1): anyone with one can sign up until
// it expires or its places run out.

const links = ref<InviteLink[]>([])
const loading = ref(true)
const error = ref('')
const notice = ref('')
const formOpen = ref(false)
const maxUses = ref<number | null>(20)
const validDays = ref(7)
const note = ref('')
const creating = ref(false)
const confirming = ref<string | null>(null) // the link whose revocation awaits a second click
const busy = ref(false)
const shown = ref<string | null>(null) // a link to copy by hand, when the clipboard is unavailable

const inviteMode = computed(() => session.site?.registration_mode === 'invite')
const durations = [1, 3, 7, 14, 30]
const statuses: Record<InviteLink['status'], { text: string; tone: string }> = {
  active: { text: '有效', tone: 'watching' },
  // Open, but it may send no more login codes; codes already sent still sign up.
  no_codes: { text: '验证码已发完', tone: 'expiring' },
  full: { text: '名额已满', tone: 'archived' },
  expired: { text: '已过期', tone: 'ended' },
  revoked: { text: '已撤销', tone: 'deleted' },
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    links.value = (await call(api.GET('/api/v1/admin/invite-links'))).items
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function create() {
  const n = maxUses.value
  if (n === null || !Number.isInteger(n) || n < 1 || n > 100) {
    error.value = '名额是 1～100 的整数。'
    return
  }
  creating.value = true
  error.value = ''
  notice.value = ''
  try {
    const l = await call(api.POST('/api/v1/admin/invite-links', {
      body: { max_uses: n, valid_hours: validDays.value * 24, note: note.value.trim() || undefined },
    }))
    links.value = [l, ...links.value]
    formOpen.value = false
    note.value = ''
    await copy(l, '链接已创建并复制，可以直接粘贴分享。', '链接已创建。')
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    creating.value = false
  }
}

async function copy(l: InviteLink, done = '已复制链接。', fallback = '') {
  shown.value = null
  try {
    await navigator.clipboard.writeText(l.url)
    notice.value = done
  } catch {
    // No clipboard (insecure context, denied): show it to copy by hand.
    shown.value = l.id
    notice.value = fallback
  }
}

async function revoke(l: InviteLink) {
  if (confirming.value !== l.id) {
    confirming.value = l.id
    return
  }
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await call(api.DELETE('/api/v1/admin/invite-links/{id}', { params: { path: { id: l.id } } }))
    notice.value = `已撤销${l.note ? `「${l.note}」` : '邀请链接'}，已注册的账户不受影响。`
    confirming.value = null
    await load()
  } catch (e) {
    error.value = messageOf(e)
  } finally {
    busy.value = false
  }
}

function selectAll(e: Event) {
  (e.target as HTMLInputElement).select()
}
</script>

<template>
  <header class="zg-pagehead"><div><h1 class="zg-pagehead__title">邀请链接</h1>
    <div class="zg-pagehead__meta">任何人都可以通过有效的链接注册，直到过期或名额用完</div></div>
    <button v-if="inviteMode" type="button" class="zg-shard zg-btn" :aria-expanded="formOpen" @click="formOpen = !formOpen"><ZgIcon name="user-plus" />新建链接</button>
  </header>
  <p v-if="session.site && !inviteMode" class="notice notice-info">现在开放注册，任何人都可以直接登录，不需要邀请链接。</p>
  <form v-if="formOpen" class="zg-shard zg-panel link-form" @submit.prevent="create">
    <label class="zg-field"><span class="zg-field__label">名额</span>
      <span class="zg-shard zg-input"><input v-model.number="maxUses" type="number" min="1" max="100" step="1" required></span>
      <span class="zg-field__hint">最多可以注册的新账号数（1～100）。已有账号通过链接登录不占名额。邀请链接每天最多发出全站邮件额度 30% 的验证码，人多时可能要分几天注册完。</span></label>
    <label class="zg-field"><span class="zg-field__label">有效期</span>
      <span class="zg-shard zg-select"><select v-model.number="validDays"><option v-for="d in durations" :key="d" :value="d">{{ d }} 天</option></select>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg></span></label>
    <label class="zg-field"><span class="zg-field__label">备注（可选）</span>
      <span class="zg-shard zg-input zg-input--text"><input v-model="note" maxlength="100" placeholder="例如：发在哪个群"></span>
      <span class="zg-field__hint">只有管理员能看到。</span></label>
    <div class="row"><button class="zg-shard zg-btn zg-btn--sm" type="submit" :disabled="creating">创建链接</button>
      <button type="button" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm" @click="formOpen = false">取消</button></div>
  </form>
  <p v-if="notice" class="notice notice-ok" role="status">{{ notice }}</p>
  <p v-if="error" class="notice notice-danger" role="alert">{{ error }} <button class="btn-link" @click="load">刷新列表</button></p>
  <p v-if="loading" class="muted" role="status">加载中…</p>
  <div class="zg-shard zg-tablewrap"><div class="zg-tablewrap__scroll"><table class="zg-table">
    <thead><tr><th>备注</th><th>状态</th><th class="is-num">已用 / 名额</th><th>到期</th><th>操作</th></tr></thead>
    <tbody>
      <template v-for="l in links" :key="l.id">
        <tr>
          <td>{{ l.note || '（无备注）' }}<span class="zg-table__sub">{{ formatDate(l.created_at) }} 创建{{ l.created_by ? ` · ${l.created_by}` : '' }}</span></td>
          <td><span class="zg-shard zg-tag" :class="`zg-tag--${statuses[l.status].tone}`"><i class="zg-tag__glyph" />{{ statuses[l.status].text }}</span></td>
          <td class="is-num">{{ l.used }} / {{ l.max_uses }}</td>
          <td class="is-mono">{{ formatDateTime(l.expires_at) }}</td>
          <td class="link-actions">
            <button v-if="l.status === 'active'" class="btn-link" @click="copy(l)">复制链接</button>
            <RouterLink v-if="l.accounts" class="btn-link" :to="{ path: '/admin/users', query: { invite_link: l.id } }">{{ l.accounts }} 个账户</RouterLink>
            <button v-if="l.status === 'active' || l.status === 'no_codes'" class="btn-link danger" :disabled="busy" @click="revoke(l)">{{ confirming === l.id ? '确认撤销' : '撤销' }}</button>
          </td>
        </tr>
        <tr v-if="shown === l.id"><td colspan="5">
          <label class="zg-shard zg-input zg-input--text"><input class="mono" :value="l.url" readonly aria-label="邀请链接" @focus="selectAll"></label>
          <span class="zg-field__hint">浏览器不允许自动复制，请手动复制上面的链接。</span>
        </td></tr>
      </template>
      <tr v-if="!links.length && !loading && !error"><td colspan="5">还没有邀请链接。</td></tr>
    </tbody>
  </table></div></div>
</template>

<style scoped>
.zg-table { min-width: 640px; }
.link-form { max-width: 560px; margin-bottom: var(--space-4); }
.link-actions { white-space: nowrap; }
.link-actions > * + * { margin-left: var(--space-3); }
.danger { color: var(--danger); }
</style>
