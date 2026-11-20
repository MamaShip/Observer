<script setup lang="ts">
import { serviceName } from '@/session'
</script>

<template>
    <div class="zg-doc">
      <nav class="zg-toc" aria-label="本页目录">
        <a href="#scope">用途</a>
        <a href="#service">服务性质</a>
        <a href="#account">账号</a>
        <a href="#complaints">内容投诉</a>
        <a href="#changes">条款变更</a>
      </nav>
      <article class="zg-prose">
        <h1>服务条款</h1>
        <p class="zg-prose__updated">更新于 2026 年 11 月</p>
        <p>使用{{ serviceName() }}即表示你同意以下条款。</p>

        <h2 id="scope">用途</h2>
        <ul>
          <li>本服务用于个人备份和阅读公开发布的微信公众号文章。</li>
          <li>请只提交你有权访问的公开文章，遵守法律法规，尊重原作者的著作权。备份仅供个人保存、阅读和研究，请勿用于转载牟利。</li>
          <li>不得利用本服务批量抓取内容、骚扰他人，或以任何方式干扰服务运行。</li>
        </ul>

        <h2 id="service">服务性质</h2>
        <ul>
          <li>本服务免费，按现状提供。我们会尽力保证备份完整、检查及时，但不承诺一定能备份成功或一定能发现删除。</li>
          <li>服务可能因微信的访问限制、维护或其他原因暂时中断，也可能调整功能或停止运营；停止前会尽量提前通知。</li>
          <li>为保证所有人都能正常使用，服务设有观察数量、提交频率和验证码次数等限制。</li>
        </ul>

        <h2 id="account">账号</h2>
        <ul>
          <li>账号以邮箱识别，请保管好你的邮箱。</li>
          <li>违反本条款的账号可能被停用。</li>
        </ul>

        <h2 id="complaints">内容投诉</h2>
        <p>
          如果你是文章的权利人，认为某份备份侵犯了你的权益，请在
          <a href="https://github.com/MamaShip/Observer/issues" target="_blank" rel="noopener noreferrer">GitHub</a> 上联系我们，核实后会删除相关备份。
        </p>

        <h2 id="changes">条款变更</h2>
        <p>条款可能会更新，更新后在本页公布。继续使用即视为接受更新后的条款。</p>
      </article>
    </div>
</template>
