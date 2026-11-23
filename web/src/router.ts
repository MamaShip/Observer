import { createRouter, createWebHistory, type RouteLocationNormalized } from 'vue-router'
import { isTokenHash } from './lib/wechat'
import { ensureSession, serviceName, session } from './session'
import HomeView from './views/HomeView.vue'
import LoginView from './views/LoginView.vue'
import SettingsView from './views/SettingsView.vue'
import WatchDetailView from './views/WatchDetailView.vue'
import WatchListView from './views/WatchListView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    /** Only for logged-in users; others are sent to the login page. */
    auth?: boolean
    /** Only for visitors; logged-in users go to their list. */
    guest?: boolean
    /** Only for admins (role admin). */
    admin?: boolean
    /** A wider page (tables). */
    wide?: boolean
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    { path: '/invite', name: 'invite', component: () => import('./views/InviteView.vue'), meta: { title: '接受邀请' } },
    { path: '/login', name: 'login', component: LoginView, meta: { title: '登录', guest: true } },
    // A public invitation link (docs/design.md, 6.1): the login page, signing up through the link.
    { path: '/join', name: 'join', component: LoginView, meta: { title: '接受邀请' } },
    { path: '/watches', name: 'watches', component: WatchListView, meta: { title: '我的观察', auth: true } },
    { path: '/watches/:id', name: 'watch', component: WatchDetailView, meta: { title: '观察详情', auth: true } },
    { path: '/watches/:id/backup/:version', name: 'reader', component: () => import('./views/ReaderView.vue'), meta: { title: '文章备份', auth: true } },
    { path: '/settings', name: 'settings', component: SettingsView, meta: { title: '设置', auth: true } },
    {
      path: '/admin',
      name: 'admin',
      component: () => import('./views/admin/AdminOverviewView.vue'),
      meta: { title: '管理后台', auth: true, admin: true, wide: true },
    },
    {
      path: '/admin/users',
      name: 'admin-users',
      component: () => import('./views/admin/AdminUsersView.vue'),
      meta: { title: '账户 · 管理后台', auth: true, admin: true, wide: true },
    },
    {
      path: '/admin/invites',
      name: 'admin-invites',
      component: () => import('./views/admin/AdminInviteLinksView.vue'),
      meta: { title: '邀请链接 · 管理后台', auth: true, admin: true, wide: true },
    },
    {
      path: '/admin/articles',
      name: 'admin-articles',
      component: () => import('./views/admin/AdminArticlesView.vue'),
      meta: { title: '文章 · 管理后台', auth: true, admin: true, wide: true },
    },
    {
      path: '/admin/articles/:id',
      name: 'admin-article',
      component: () => import('./views/admin/AdminArticleView.vue'),
      meta: { title: '文章详情 · 管理后台', auth: true, admin: true, wide: true },
    },
    // Links from the Official Account's replies (docs/design.md, 6.7). They check
    // the login themselves, so the token never goes into a login redirect.
    { path: '/wx/bind', name: 'wechat-bind', component: () => import('./views/WechatBindView.vue'), meta: { title: '绑定公众号' } },
    { path: '/wx/go', name: 'wechat-go', component: () => import('./views/WechatGoView.vue'), meta: { title: '正在打开' } },
    { path: '/about', name: 'about', component: () => import('./views/AboutView.vue'), meta: { title: '关于' } },
    { path: '/privacy', name: 'privacy', component: () => import('./views/PrivacyView.vue'), meta: { title: '隐私说明' } },
    { path: '/terms', name: 'terms', component: () => import('./views/TermsView.vue'), meta: { title: '服务条款' } },
    { path: '/:rest(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { title: '页面不存在' } },
  ],
  scrollBehavior(to, _from, saved) {
    return saved ?? (to.hash && !isTokenHash(to.hash) ? { el: to.hash, top: 80 } : { top: 0 })
  },
})

/** safeNext keeps a post-login redirect inside this site. */
export function safeNext(v: unknown): string {
  return typeof v === 'string' && v.startsWith('/') && !v.startsWith('//') && !v.startsWith('/\\') ? v : '/watches'
}

export function loginRoute(to: RouteLocationNormalized | string) {
  const next = typeof to === 'string' ? to : to.fullPath
  return { name: 'login', query: next && next !== '/' ? { next } : {} }
}

router.beforeEach(async (to) => {
  await ensureSession()
  if (to.meta.auth && !session.user) return loginRoute(to)
  if (to.meta.admin && session.user?.role !== 'admin') return { name: 'not-found', params: { rest: to.path.slice(1).split('/') } }
  if (to.meta.guest && session.user) return safeNext(to.query.next)
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · ${serviceName()}` : serviceName()
})
