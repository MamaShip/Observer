<template>
  <nav class="admin-nav" aria-label="管理后台">
    <RouterLink :to="{ name: 'admin' }" exact-active-class="active">概览</RouterLink>
    <RouterLink :to="{ name: 'admin-users' }" active-class="active">账户</RouterLink>
    <RouterLink :to="{ name: 'admin-articles' }" active-class="active">文章</RouterLink>
  </nav>
</template>

<style scoped>
.admin-nav {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 20px;
}
.admin-nav a {
  padding: 8px 14px;
  color: var(--muted);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
}
.admin-nav a:hover {
  text-decoration: none;
  color: var(--fg);
}
.admin-nav a.active {
  color: var(--fg);
  font-weight: 600;
  border-bottom-color: var(--accent);
}
</style>
