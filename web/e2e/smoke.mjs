// Manual acceptance run of the whole web app in a real browser (plan.md §15):
// log in, submit a real article, wait for its backup, read it, download it,
// print it to PDF, visit every page — on a phone-sized screen, then again as
// WeChat's in-app browser. Fails on console errors, CSP violations, 5xx
// responses and images that do not load.
//
// It needs Chrome or Chromium and a running service, and logs in one of two
// ways:
//
// - With a code from the service's log (development: no SMTP_HOST,
//   REGISTRATION_MODE=open, BASE_URL=$BASE):
//     BASE=http://localhost:8080 LOG=/path/to/service.log OUT=/tmp/shots \
//     ARTICLE=https://mp.weixin.qq.com/s/... npm run smoke
//
// - With a session token from `observer admin session <email>` (any
//   deployment, production included):
//     BASE=https://ob.portkey.click SESSION=<token> ARTICLE=... npm run smoke
//
// INSECURE_TLS=1 accepts self-signed certificates (the local production
// stack, deploy/compose.local.yaml). CHROME is the browser binary.
import { mkdirSync, readFileSync } from 'node:fs'
import { chromium } from 'playwright-core'

const env = (k, d) => process.env[k] ?? d ?? (console.error(`set ${k}`), process.exit(2))
const base = env('BASE', 'http://localhost:8080')
const session = process.env.SESSION
const logFile = session ? '' : env('LOG')
const out = env('OUT', 'e2e-shots')
const article = env('ARTICLE')
const chrome = env('CHROME', '/usr/bin/google-chrome')
mkdirSync(out, { recursive: true })

const email = `smoke-${Date.now()}@example.com`
const problems = []
const iPhone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'
const weChat = `${iPhone.replace(' Version/18.0 Mobile/15E148 Safari/604.1', ' Mobile/15E148')} MicroMessenger/8.0.50(0x1800322d) NetType/WIFI Language/zh_CN`

function codeFromLog() {
  const log = readFileSync(logFile, 'utf8')
  return /验证码：(\d{6})/.exec(log.slice(log.lastIndexOf(email)))?.[1]
}

function watch(page) {
  page.on('console', (m) => {
    if (m.type() === 'error') problems.push(`console: ${m.text()}`)
  })
  page.on('pageerror', (e) => problems.push(`page error: ${e.message}`))
  page.on('response', (r) => r.status() >= 500 && problems.push(`HTTP ${r.status()} ${r.url()}`))
}

const step = (s) => console.log(`- ${s}`)
const browser = await chromium.launch({ executablePath: chrome })
const phone = { viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true, locale: 'zh-CN' }
const insecure = { ignoreHTTPSErrors: process.env.INSECURE_TLS === '1' }
const ctx = await browser.newContext({ ...phone, ...insecure, userAgent: iPhone })
const page = await ctx.newPage()
watch(page)
const shot = (name) => page.screenshot({ path: `${out}/${name}.png`, fullPage: true })

try {
  await page.goto(base + '/')
  await shot('01-home')
  await page.goto(base + '/watches')
  await page.waitForURL((u) => u.pathname === '/login')
  step('logged-out visitors are sent to the login page')

  if (session) {
    await ctx.addCookies([{ name: '__Host-session', value: session, url: base, secure: true, httpOnly: true, sameSite: 'Lax' }])
    await page.goto(base + '/watches')
    await page.waitForURL((u) => u.pathname === '/watches')
    step('logged in with the given session')
  } else {
    await page.fill('input[name=email]', email)
    await page.click('button[type=submit]')
    await page.waitForSelector('input[name=code]')
    await shot('02-login-code')
    let code
    for (let i = 0; i < 40 && !code; i++) {
      await page.waitForTimeout(250)
      code = codeFromLog()
    }
    if (!code) throw new Error(`no login code for ${email} in ${logFile}`)
    await page.fill('input[name=code]', code) // submits itself at 6 digits
    await page.waitForURL((u) => u.pathname === '/watches')
    step('logged in with the emailed code')
  }
  await shot('03-list-empty')

  await page.fill('textarea[name=url]', `看看这篇 ${article} 来自微信`)
  await page.click('form.submit button[type=submit]')
  await page.waitForURL((u) => u.pathname.startsWith('/watches/'))
  const detailURL = page.url()
  await shot('04-detail-new')
  const t0 = Date.now()
  await page.waitForSelector('a:has-text("在新窗口阅读")', { timeout: 300_000 })
  await page.click('text=在页面内预览') // collapsed on phones
  await page.waitForSelector('iframe.preview')
  await page.waitForTimeout(3000)
  step(`backup readable after ${Math.round((Date.now() - t0) / 1000)} s`)
  await shot('05-detail')

  const frame = page.frames().find((f) => f !== page.mainFrame())
  const images = await frame.evaluate(() => [...document.images].map((i) => i.complete && i.naturalWidth > 0))
  step(`snapshot images loaded: ${images.filter(Boolean).length}/${images.length}`)
  if (images.some((ok) => !ok)) problems.push('some snapshot images did not load')

  await page.click('text=打印 / 保存为 PDF')
  await shot('06-print-help')
  const viewURL = await page.getAttribute('a:has-text("在新窗口阅读")', 'href')
  const [download] = await Promise.all([page.waitForEvent('download'), page.click('text=下载 HTML')])
  step(`download: ${download.suggestedFilename()}`)
  const snap = await ctx.newPage()
  await snap.goto(viewURL)
  await snap.pdf({ path: `${out}/snapshot.pdf`, format: 'A4' })
  await snap.close()
  step('printed the backup to snapshot.pdf')

  await page.goto(base + '/watches')
  await page.waitForSelector('.item')
  await shot('07-list')
  await page.goto(base + '/settings')
  await shot('08-settings')
  for (const p of ['about', 'privacy', 'terms', 'no-such-page']) {
    await page.goto(`${base}/${p}`)
    await page.waitForSelector('main h1')
    await shot(`09-${p}`)
  }

  // The same detail page in WeChat's in-app browser, which cannot download
  // or print: it must say so.
  const cookies = await ctx.cookies()
  const wctx = await browser.newContext({ ...phone, ...insecure, userAgent: weChat })
  await wctx.addCookies(cookies)
  const wpage = await wctx.newPage()
  watch(wpage)
  await wpage.goto(detailURL)
  await wpage.waitForSelector('a:has-text("在新窗口阅读")')
  if (!(await wpage.isVisible('text=微信内置浏览器不能下载文件或打印'))) problems.push('no WeChat hint on the detail page')
  await wpage.screenshot({ path: `${out}/10-wechat-detail.png`, fullPage: true })
  step('WeChat in-app browser hint shown')

  await page.goto(base + '/settings')
  await page.click('text=退出登录')
  await page.waitForURL((u) => u.pathname === '/')
  step('logged out')
} catch (e) {
  problems.push(`failed: ${e.message}`)
  await shot('99-failure').catch(() => {})
} finally {
  await browser.close()
}

console.log(`screenshots in ${out}`)
if (problems.length) {
  console.log('PROBLEMS:\n' + problems.map((p) => `  ${p}`).join('\n'))
  process.exit(1)
}
console.log('ok: no console errors, CSP violations or 5xx responses')
