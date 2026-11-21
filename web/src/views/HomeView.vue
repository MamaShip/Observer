<script setup lang="ts">
import ZgIcon from '@/components/ZgIcon.vue'
import { session } from '@/session'

const steps = [
  ['保存备份', '提交链接后，我们保存正文和图片。你可以在线阅读当时的版本、下载 HTML，或打印成 PDF。'],
  ['定期检查', '观察期内，我们会定期回访原文。每次检查的时间和结果都记在观察记录里，偶尔打不开也会再检查。'],
  ['删除提醒', '确认原文被删除后，我们发邮件告诉你，写明发现时间和微信页面显示的原因，并附上备份链接。'],
]
</script>

<template>
    <section class="zg-hero">
      <div>
        <p class="zg-hero__kicker">WECHAT ARTICLE OBSERVER</p>
        <h1 class="zg-hero__title">它还在吗</h1>
        <p class="zg-hero__lede">提交一篇公众号文章，我们立即保存当前版本，之后定期回来看它。一旦确认原文被删除，就发邮件告诉你，备份随时可以打开。</p>
        <div class="zg-hero__cta">
          <RouterLink :to="session.user ? '/watches' : '/login'" class="zg-shard zg-btn">
            <ZgIcon v-if="!session.user" name="mail" />
            {{ session.user ? '进入我的观察' : '邮箱登录' }}
          </RouterLink>
          <RouterLink to="/about" class="zg-shard zg-btn zg-btn--ghost">了解如何工作</RouterLink>
        </div>
        <p v-if="!session.user" class="zg-footnote">
          {{ session.site?.registration_mode === 'invite' ? '目前采用邀请制，受邀后可用邮箱验证码登录。' : '不需要密码，用邮箱验证码登录。' }}</p>
      </div>
      <aside class="hero-example" aria-label="观察和删除提醒示例">
        <span class="example-label" aria-hidden="true">EXAMPLE · 示例</span>
        <article class="zg-shard zg-watch example-card">
          <div class="zg-watch__top">
            <span class="zg-shard zg-tag zg-tag--watching"><i class="zg-tag__glyph" />观察中</span>
            <span class="zg-meta">观察示例</span>
          </div>
          <h2 class="zg-watch__title">一座老城的排水账本：三次内涝之后</h2>
          <div class="zg-watch__source">来自公众号「河岸观察」</div>
          <div class="zg-watch__foot">
            <span class="zg-meta">最近检查 14:32</span><span class="zg-shard zg-tag zg-tag--archived"><i class="zg-tag__glyph" />已备份</span></div>
        </article>
        <section class="zg-shard zg-notice example-card">
          <span class="zg-notice__kicker">DELETED · 提醒示例</span>
          <h2 class="zg-notice__title">这篇文章已被删除</h2>
          <p class="zg-notice__body">「县城青年的第三份工作」在最近一次检查中确认已删除。你提交时的版本还在，随时可以查看。</p>
          <div class="zg-notice__actions">
            <RouterLink to="/about#how" class="zg-shard zg-btn zg-btn--secondary zg-btn--sm">了解备份</RouterLink>
          </div>
        </section>
      </aside>
    </section>
    <section class="how">
      <h2 class="zg-section-title">三件事</h2>
      <div class="zg-steps">
        <div v-for="([title, body], i) in steps" :key="title" class="zg-shard zg-step" :class="{ 'is-warm': i === 2 }">
          <span class="zg-step__n">0{{ i + 1 }}</span>
          <h3 class="zg-step__title">{{ title }}</h3>
          <p class="zg-step__body">{{ body }}</p>
        </div>
      </div>
    </section>
    <section class="scope">
      <div>
        <h2 class="zg-section-title">怎么用</h2>
        <p>在微信里打开文章，点右上角「复制链接」，粘贴到「我的观察」即可。</p>
      </div>
      <div class="zg-shard zg-panel">
        <h3 class="zg-panel__title">支持</h3>
        <dl class="zg-facts">
          <dt>链接</dt>
          <dd class="is-mono">mp.weixin.qq.com/s…</dd>
          <dt>备份</dt>
          <dd>正文、图片、标题、公众号名、发布时间</dd>
          <dt>格式</dt>
          <dd>在线预览、离线 HTML、打印为 PDF</dd>
        </dl>
      </div>
      <div class="zg-shard zg-panel">
        <h3 class="zg-panel__title">不包含</h3>
        <dl class="zg-facts">
          <dt>媒体</dt>
          <dd>视频、音频、小程序卡片</dd>
          <dt>互动</dt>
          <dd>留言区、点赞与在看数</dd>
          <dt>外链</dt>
          <dd>文章里跳转到的其他页面</dd>
        </dl>
      </div>
    </section>
    <section class="zg-shard zg-panel start">
      <div>
        <h2 class="zg-section-title">从一篇文章开始</h2>
        <p>粘贴公众号文章链接，保存你关心的内容。</p>
      </div>
      <RouterLink :to="session.user ? '/watches' : '/login'" class="zg-shard zg-btn zg-btn--secondary">{{ session.user ? '进入我的观察' : '邮箱登录' }}
      </RouterLink>
    </section>
</template>
<style scoped>
/* The two cards beside the hero are specimens, not live content: they sit on
   a dotted stage under an "example" label, tilted, lifted and gently floating. */
.hero-example {
  --example-shadow: rgba(15, 26, 46, 0.14);
  position: relative; width: 100%; max-width: 420px; justify-self: end; display: grid; gap: var(--space-5);
  padding: var(--space-7) var(--space-5) var(--space-6);
  background-image: radial-gradient(var(--line-100) 1.2px, transparent 1.4px);
  background-size: 14px 14px;
  background-position: center;
}
:global(:root[data-theme="dark"]) .hero-example { --example-shadow: rgba(0, 0, 0, 0.55); }
.example-label {
  position: absolute; top: var(--space-3); left: var(--space-5);
  font: 500 11px/16px var(--font-mono); letter-spacing: .08em; color: var(--ink-300);
  padding: 2px var(--space-2); background: var(--surface-000); border: var(--border-hair) dashed var(--line-200);
}
.example-card {
  --tilt: -1.5deg;
  /* drop-shadow follows the cut corner; a box-shadow would square it off. */
  filter: drop-shadow(0 14px 22px var(--example-shadow)) drop-shadow(0 2px 4px var(--example-shadow));
  transform: rotate(var(--tilt));
  animation: example-float 6s ease-in-out infinite;
}
.hero-example .zg-notice { --tilt: 1.2deg; margin-left: var(--space-6); box-shadow: none; animation-delay: -3s; }
@keyframes example-float {
  0%, 100% { transform: translateY(0) rotate(var(--tilt)); }
  50% { transform: translateY(-6px) rotate(var(--tilt)); }
}
@media (prefers-reduced-motion: reduce) { .example-card { animation: none; } }

/* 三件事 and 怎么用 are separate sections: a rule and space between them, and
   flat tinted panels below so they do not read as a fourth and fifth step. */
.how { padding-top: var(--space-4); }
.scope {
  display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr)); gap: var(--space-5);
  margin-top: var(--space-4); padding: var(--space-7) 0; border-top: var(--border-hair) solid var(--line-100);
}
.scope .zg-panel { --_fill: var(--surface-200); --_edge: var(--surface-200); }
.scope p, .start p { margin: 0; color: var(--ink-200); }
.start { --_c: var(--chamfer-l); padding: var(--space-6); flex-flow: row wrap; align-items: center; justify-content: space-between; }
.start h2 { margin-bottom: var(--space-2); }
@media (max-width: 767px) {
  .hero-example { justify-self: stretch; max-width: none; padding: var(--space-7) var(--space-4) var(--space-5); }
  .hero-example .zg-notice { margin-left: var(--space-4); }
}
</style>
