import { createApp } from 'vue'
import { api } from './api/client'
import App from './App.vue'
import { loginRoute, router } from './router'
import { setUser } from './session'
import './styles/main.css'

// A session that ends while the app is open (expired, logged out elsewhere)
// sends the user to the login page from wherever they were.
api.use({
  onResponse({ request, response }) {
    if (response.status !== 401) return
    const path = new URL(request.url, location.origin).pathname
    if (path.startsWith('/api/v1/auth/') || (path === '/api/v1/me' && request.method === 'GET')) return
    setUser(null)
    const current = router.currentRoute.value
    if (current.meta.auth) void router.replace(loginRoute(current))
  },
})

const theme = window.matchMedia('(prefers-color-scheme: dark)')
function syncTheme() {
  document.documentElement.dataset.theme = theme.matches ? 'dark' : 'light'
}
syncTheme()
theme.addEventListener('change', syncTheme)

createApp(App).use(router).mount('#app')
