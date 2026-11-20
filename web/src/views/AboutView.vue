<script setup lang="ts">
import { serviceName, session } from '@/session'
import { computed } from 'vue'

const days = computed(() => session.site?.watch_days ?? 60)
const maxOpen = computed(() => session.site?.max_open_watches ?? 50)
const monthlyEmails = computed(() => session.site?.monthly_emails ?? 20)
</script>

<template>
  <article class="card prose">
    <h1>关于{{ serviceName() }}</h1>
    <p>
      微信公众号文章随时可能被删除：作者自删、违规删除、被投诉、账号被封……删除之后，原文就再也看不到了。{{ serviceName() }}帮你在文章还在的时候保存一份，并在它被删除时告诉你。
    </p>

    <h2>它是怎么工作的</h2>
    <ul>
      <li>提交文章链接后，服务立即下载正文和图片，生成一份排版好的备份（视频、音频不保存）。</li>
      <li>接下来 {{ days }} 天里定期检查文章是否还能访问：刚提交的文章每小时左右一次，之后逐渐放宽到每天几次。</li>
      <li>发现文章被删除时，会再复核一次，确认后给你发邮件，并附上微信显示的删除原因。</li>
      <li>备份可以在线阅读、下载为单个 HTML 文件（不联网也能打开），或用浏览器打印成 PDF。</li>
      <li>同一篇文章被多人提交时只保存一份，每位提交者都能查看。</li>
    </ul>

    <h2>使用限制</h2>
    <ul>
      <li>每人最多同时观察 {{ maxOpen }} 篇文章。</li>
      <li v-if="monthlyEmails > 0">每个账号每月最多收到 {{ monthlyEmails }} 封通知邮件；超出后，新的变化会在下月初汇总成一封发送，网站上随时可以查看。</li>
      <li>只能备份公开的公众号文章；需要付费或关注后才能阅读的内容无法备份。</li>
      <li>如果微信暂时限制访问，检查会推迟进行，期间可能无法及时发现删除。</li>
    </ul>

    <h2>背景</h2>
    <p>
      本项目受<a href="https://github.com/Terminus2049/Terminus2049.github.io" target="_blank" rel="noopener noreferrer">端点星计划</a>启发，只为个人备份自己关注的文章，不做全量存档。全量备份工作有香港大学的
      <a href="https://wechatscope.jmsc.hku.hk/" target="_blank" rel="noopener noreferrer">WeChatSCOPE</a> 项目在做。
    </p>
    <p>
      {{ serviceName() }}是开源软件（GPL-3.0），源代码在
      <a href="https://github.com/MamaShip/Observer" target="_blank" rel="noopener noreferrer">GitHub</a>。问题和建议欢迎在那里提出。
    </p>
  </article>
</template>
