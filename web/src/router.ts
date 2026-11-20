import { createRouter, createWebHistory, type RouteLocationNormalized } from 'vue-router'
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
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    { path: '/login', name: 'login', component: LoginView, meta: { title: '登录', guest: true } },
    { path: '/watches', name: 'watches', component: WatchListView, meta: { title: '我的观察', auth: true } },
    { path: '/watches/:id', name: 'watch', component: WatchDetailView, meta: { title: '观察详情', auth: true } },
    { path: '/settings', name: 'settings', component: SettingsView, meta: { title: '设置', auth: true } },
    { path: '/about', name: 'about', component: () => import('./views/AboutView.vue'), meta: { title: '关于' } },
    { path: '/privacy', name: 'privacy', component: () => import('./views/PrivacyView.vue'), meta: { title: '隐私说明' } },
    { path: '/terms', name: 'terms', component: () => import('./views/TermsView.vue'), meta: { title: '服务条款' } },
    { path: '/:rest(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { title: '页面不存在' } },
  ],
  scrollBehavior(_to, _from, saved) {
    return saved ?? { top: 0 }
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
  if (to.meta.guest && session.user) return safeNext(to.query.next)
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · ${serviceName()}` : serviceName()
})
