<template>
  <section class="zg-empty">
    <div class="zg-empty__art" aria-hidden="true"><i /><i /><i /><i /></div>
    <span class="zg-empty__code">404</span>
    <h1 class="zg-empty__title">这个页面不存在</h1>
    <p class="zg-empty__body">链接可能写错了，或者这页已经被移走。如果你在找一份文章备份，请从「我的观察」进入。</p>
    <div class="zg-actions">
      <RouterLink to="/" class="zg-shard zg-btn">返回首页</RouterLink>
      <RouterLink to="/watches" class="zg-shard zg-btn zg-btn--ghost">我的观察</RouterLink>
    </div>
  </section>
</template>
<style scoped>
.zg-empty { padding: var(--space-8) 0; max-width: 480px; }
</style>
