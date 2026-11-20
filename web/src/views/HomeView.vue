<script setup lang="ts">
import { serviceName, session } from '@/session'
import { computed } from 'vue'

const days = computed(() => session.site?.watch_days ?? 60)
</script>

<template>
  <section class="hero">
    <h1>公众号文章被删之前，先存一份</h1>
    <p class="lead">
      把微信公众号文章链接交给{{ serviceName() }}：它会立即备份全文和图片，并在接下来 {{ days }} 天里定期检查文章是否还在。文章被删除时，你会收到邮件，备份随时可以在线阅读、下载或打印成 PDF。
    </p>
    <div class="row">
      <RouterLink v-if="session.user" to="/watches" class="btn btn-primary">进入我的观察</RouterLink>
      <RouterLink v-else to="/login" class="btn btn-primary">用邮箱登录，开始使用</RouterLink>
      <RouterLink to="/about" class="btn">了解更多</RouterLink>
    </div>
    <p v-if="session.site?.registration_mode === 'invite' && !session.user" class="muted small invite">
      目前处于邀请测试阶段，只有受邀的邮箱可以登录。
    </p>
  </section>

  <ol class="steps">
    <li class="card">
      <h2>1. 提交链接</h2>
      <p>在微信里打开文章，点右上角「···」→「复制链接」，粘贴到这里。</p>
    </li>
    <li class="card">
      <h2>2. 立即备份</h2>
      <p>几分钟内保存好正文和图片。即使文章随后被删，你也能看到原貌。</p>
    </li>
    <li class="card">
      <h2>3. 删除提醒</h2>
      <p>观察期内定期检查。文章被删除时发邮件告诉你，并说明微信给出的删除原因。</p>
    </li>
  </ol>

  <section class="card note">
    <h2>说明</h2>
    <ul>
      <li>备份只供你个人阅读和保存，不会公开展示。</li>
      <li>只能备份公开发布的公众号文章；视频、音频等多媒体内容不会保存。</li>
      <li>本服务开源，详见页面底部的「源代码」。</li>
    </ul>
  </section>
</template>

<style scoped>
.hero {
  padding: 20px 0 8px;
}
.hero h1 {
  font-size: 1.75rem;
}
.lead {
  font-size: 1.05rem;
  color: var(--muted);
  margin-bottom: 20px;
}
.invite {
  margin-top: 12px;
}
.steps {
  list-style: none;
  padding: 0;
  margin: 28px 0 16px;
  display: grid;
  gap: 12px;
}
/* The grid gap spaces the cards; the global .card + .card margin would
   push the 2nd and 3rd down and make them shorter than the 1st. */
.steps > .card {
  margin: 0;
}
@media (min-width: 640px) {
  .steps {
    grid-template-columns: repeat(3, 1fr);
  }
}
.steps p {
  margin: 0;
  color: var(--muted);
  font-size: 0.95rem;
}
.note ul {
  margin: 0;
  padding-left: 1.2em;
  color: var(--muted);
}
</style>
