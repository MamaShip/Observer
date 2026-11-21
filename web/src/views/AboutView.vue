<script setup lang="ts">
import { sourceURL } from '@/lib/version'
import { serviceName, session } from '@/session'
import { computed } from 'vue'

const days = computed(() => session.site?.watch_days ?? 60)
const maxOpen = computed(() => session.site?.max_open_watches ?? 50)
const monthlyEmails = computed(() => session.site?.monthly_emails ?? 20)
const version = computed(() => session.site?.version)
const versionURL = computed(() => sourceURL(version.value))
</script>

<template>
    <div class="zg-doc">
      <nav class="zg-toc" aria-label="本页目录">
        <a href="#how">它是怎么工作的</a>
        <a href="#limits">使用限制</a>
        <a href="#deploy">创建自己的独立观察者</a>
      </nav>
      <article class="zg-prose">
        <h1>关于{{ serviceName() }}</h1>
        <p>
          微信公众号文章随时可能被删除……删除之后，原文就再也看不到了。{{ serviceName() }}帮你在文章还在的时候保存一份，并在它被删除时告诉你。
        </p>

        <h2 id="how">它是怎么工作的</h2>
        <ul>
          <li>提交文章链接后，服务立即下载正文和图片，生成一份备份（视频、音频不保存）。</li>
          <li>在 {{ days }} 天的观察期内，服务会定期检查文章是否还能访问。</li>
          <li>发现文章被删除时，会再复核一次，确认后给你发邮件，并附上微信显示的删除原因。</li>
          <li>备份可以在线阅读、下载为单个 HTML 文件（不联网也能打开），或用浏览器打印成 PDF。</li>
        </ul>

        <h2 id="limits">使用限制</h2>
        <ul>
          <li>每人最多同时观察 {{ maxOpen }} 篇文章。</li>
          <li v-if="monthlyEmails > 0">每个账号每月最多收到 {{ monthlyEmails }} 封通知邮件；超出后，新的变化会在下月初汇总成一封发送，网站上随时可以查看。</li>
          <li>只能备份公开的公众号文章；需要付费或关注后才能阅读的内容无法备份。</li>
          <li>如果微信暂时限制访问，检查会推迟进行，期间可能无法及时完成备份或发现删除。</li>
        </ul>

        <h2 id="deploy">创建自己的独立观察者</h2>
        <p>
          欢迎你用这个开源项目搭建自己的观察者，把关心的文章保存在自己的服务器和存储空间里。你可以自己用，也可以邀请朋友加入。
        </p>
        <p>
          项目采用 GPL-3.0 许可证。部署指南和源代码都在
          <a href="https://github.com/MamaShip/Observer" target="_blank" rel="noopener noreferrer">GitHub 项目主页</a>。
        </p>
        <p v-if="version" class="muted small">
          当前版本：<a v-if="versionURL" :href="versionURL" target="_blank" rel="noopener noreferrer">{{ version }}</a><template
            v-else>{{ version }}</template>
        </p>
      </article>
    </div>
</template>
