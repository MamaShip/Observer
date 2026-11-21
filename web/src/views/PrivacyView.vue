<script setup lang="ts">
import { serviceName, session } from '@/session'
</script>

<template>
    <div class="zg-doc">
      <nav class="zg-toc" aria-label="本页目录">
        <a href="#data">我们保存的信息</a>
        <a href="#cookie">Cookie</a>
        <a href="#backup">文章备份</a>
        <a href="#providers">第三方服务</a>
        <a href="#delete">删除账号</a>
        <a href="#contact">联系</a>
      </nav>
      <article class="zg-prose">
        <h1>隐私说明</h1>
        <p class="zg-prose__updated">更新于 2026 年 11 月</p>
        <p>{{ serviceName() }}只收集提供服务所必需的信息，不做统计分析，不投放广告，也不使用任何第三方跟踪。</p>

        <h2 id="data">我们保存的信息</h2>
        <ul>
          <li><strong>邮箱地址</strong>：用于登录和发送通知。</li>
          <li><strong>你提交的文章链接和观察记录</strong>：用于检查文章状态、在网页上展示给你。</li>
          <li><strong>获取验证码时的 IP 地址</strong>：用于防止滥用，随验证码一起保存，验证码过期一天后删除。</li>
          <li><strong>浏览器标识（User-Agent）</strong>：随登录状态保存，登录状态失效或退出登录后删除。</li>
          <li><strong>邮件发送记录</strong>（收件地址、标题、时间）：用于控制发送量和排查投递问题，保存 90 天。</li>
          <li><strong>每个邮箱当天获取验证码的次数</strong>：用于限制发送频率，保存 2 天。</li>
          <li v-if="session.site?.wechat">
            <strong>公众号绑定</strong>（如果你绑定了）：微信提供给本公众号的用户标识（OpenID，只在本公众号内有效，不是微信号）和绑定时间，用于把你在公众号里发送的链接加入你的账号。你在公众号里发送的消息不会保存。回复里的一次性链接最长 1 天有效（绑定链接 30 分钟，附带你绑定前发送的文章链接），过期后在每天的清理中删除。绑定关系在解除绑定、取消关注公众号或删除账号时立即删除。
          </li>
        </ul>

        <h2 id="cookie">Cookie</h2>
        <p>只使用一个保持登录状态的 Cookie，30 天内不使用会自动失效。没有其他 Cookie。</p>

        <h2 id="backup">文章备份</h2>
        <p>
          备份的是公开发布的文章内容，不包含个人信息。同一篇文章只保存一份；只有提交过这篇文章的用户能够查看它的备份，备份不会公开展示。
        </p>

        <h2 id="providers">第三方服务</h2>
        <ul>
          <li>服务器位于香港。</li>
          <li>文章备份（正文和图片）存放在 Wasabi 的新加坡机房。</li>
          <li>邮件通过 Resend（美国）发送，它会处理收件地址和邮件内容。</li>
          <li v-if="session.site?.wechat">使用公众号时，你发送的消息和我们的回复经过微信（腾讯）的服务器。</li>
        </ul>

        <h2 id="delete">删除账号</h2>
        <p>
          你可以随时在「设置」里删除账号。删除后，你的邮箱、登录状态、观察列表和公众号绑定会立即删除，邮件发送记录里的地址也会被抹去。
        </p>

        <h2 id="contact">联系</h2>
        <p>
          对隐私有任何疑问，请在
          <a href="https://github.com/MamaShip/Observer/issues" target="_blank" rel="noopener noreferrer">GitHub</a> 上提出。
        </p>
      </article>
    </div>
</template>
