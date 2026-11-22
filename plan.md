# Observer v2 重构方案

> 状态：草案 v1（2026-11-19）
> 范围：第一阶段（核心服务 + Web 端）
> 旧实现存档：git tag `legacy-python`（Flask + MySQL + docx 版本，需要参考旧逻辑时 `git show legacy-python:src/...`）

---

## 1. 背景与目标

### 1.1 背景

Observer（折光观察者）是一个微信公众号文章备份与删除监测服务：用户提交文章链接，系统立即备份，并在观察期内定期检查文章是否仍可访问，文章失效时通知用户。

旧实现（2020 年，2022 年停服）存在以下根本问题，决定整体重写而非修补：

- 运行时与依赖老化：Python 3.5–3.8、依赖未锁定，按现有依赖安装已无法启动（Flask 2.3 删除 `escape`）。
- 可靠性：工作线程遇到未捕获异常会静默退出，观察/发信停止且无告警。
- 正确性：删除检测依赖 2020 年的页面结构；被反爬拦截时会被误判为"文章存在"。
- 通知渠道失效：Gmail 已停用账号密码 SMTP 登录。
- 无测试、无部署自动化、配置与个人信息硬编码在代码中。

### 1.2 目标（第一阶段）

1. **独立核心服务**：业务逻辑与入口解耦，对外提供统一 JSON API；Web 是第一个入口，未来接入公众号（订阅号被动回复）与微信小程序时不改核心。
2. **HTML 备份**：每篇文章保存清洗后的 HTML 快照与图片；用户可在线查看、下载单文件 HTML，或通过浏览器打印为 PDF（阅读版自带打印样式）。
3. **账号体系**：邮箱登录，用户在 Web 上管理观察列表、查看备份。
4. **邮件通知**：文章被删除时发邮件，邮件内附查看链接（不再附带附件）。
5. **检测可信**：明确区分"存在 / 已删除 / 被拦截 / 网络错误"，宁可报告"无法判定"也不误报漏报。
6. **低成本、低维护**：单台小规格 VPS 运行；除已有资源外新增现金成本为零；单二进制部署，依赖锁定，自动化测试与部署。

### 1.3 非目标（第一阶段不做）

- ~~公众号入口~~（2026-11-21 实现了个人订阅号的被动回复入口，见 §6.7）；小程序入口、微信消息推送（无企业主体，见 §2）。
- 被审查文章的全量备份（定位仍是个人关注文章的备份）。
- ~~管理后台 Web 界面~~（2026-11-20 提前实现了简易版，见 §10.1）。
- 多机部署 / 高可用。

---

## 2. 约束

| 类别 | 约束 | 对设计的影响 |
|---|---|---|
| 主体 | 无企业主体 | 无法使用服务号模板消息/订阅通知、开放平台 UnionID、微信扫码登录 → 通知只用邮件，登录用邮箱 |
| 部署地 | 香港 VPS | 无需 ICP 备案；需实测香港数据中心 IP 抓取微信文章是否频繁触发验证 |
| VPS | 2 核 / **1 GB 内存** / 30 GB 磁盘 / 500 GB 月流量 | 服务器只运行后端服务，**不运行浏览器**；Postgres 调小；本地缓存设上限 |
| 已有资源 | Wasabi 对象存储套餐、域名 | 备份文件全部存 Wasabi；注意 90 天最短存储期、出站 ≤ 存储量的公平使用条款（账号下其他应用已有约 1 TB 存储，本服务出站不受限，见 §8.1） |
| 成本 | 其余尽量零成本 | 邮件用 Resend 免费档（3000 封/月，**100 封/日**）；CI/镜像仓库用 GitHub 免费额度 |

---

## 3. 总体架构

```
                      ┌──────────────────── VPS（Docker Compose）─────────────────────┐
                      │                                                              │
  浏览器 ──HTTPS──▶ Caddy ──┬── <domain>        ──▶ app: API + 内嵌 Web 静态文件        │
                      │     └── snap.<domain>   ──▶ app: 快照服务（隔离域，无登录 Cookie）│
                      │                              │                               │
                      │                      ┌───────┴────────┐                      │
                      │                      │ app（单 Go 进程）│                      │
                      │                      │  ├ HTTP 层 (Huma)│                      │
                      │                      │  ├ River workers │                      │
                      │                      │  └ 周期任务      │                      │
                      │                      └───┬─────────┬───┘                      │
                      │                          │         │ 本地磁盘缓存（上限 5 GB）  │
                      │                   Postgres 18      │                          │
                      │              （业务数据 + 任务队列） │                          │
                      └──────────────────────────────────── │ ─────────────────────────┘
                                                            ▼
                     mp.weixin.qq.com / mmbiz.qpic.cn    Wasabi（快照、图片、PDF、DB 备份）
                                                         Resend SMTP（邮件）
                                                         Healthchecks.io（心跳告警）
```

### 3.1 进程模型

- **一个 Go 二进制 `observer`**，子命令：
  - `observer serve`：HTTP 服务 + 任务 worker + 周期任务（第一阶段同进程运行）。
  - `observer migrate`：执行数据库迁移（`serve` 启动时也会自动执行）。
  - `observer probe <url>`：抓取诊断工具，打印检测结论，可选 `--save-fixture` 保存为测试样本。服务器上和开发者本机都可以运行。
  - `observer admin ...`：管理命令（统计、用户禁用、手动触发检查、重放邮件等）。
  - `observer healthcheck`：请求本机 `/api/v1/healthz`，供 Docker `HEALTHCHECK` 使用（distroless 镜像里没有 curl）。
  - `observer netcheck`：测量到微信、Wasabi、SMTP 的网络路径（M0 工具）。
- 未来如需拆分，可通过参数让 API 与 worker 分别运行，代码无需改动。

### 3.2 模块划分

| 包 | 职责 |
|---|---|
| `internal/wechat` | URL 识别与规范化、页面抓取、解析、删除检测（**核心难点，测试覆盖要求最高**） |
| `internal/snapshot` | HTML 清洗、图片下载与去重、生成阅读版 HTML（含打印样式）、生成单文件 HTML |
| `internal/blob` | Wasabi（S3）读写 + 本地磁盘 LRU 缓存 |
| `internal/store` | Postgres 访问（sqlc 生成）、迁移文件 |
| `internal/jobs` | River 任务定义：抓取、检查、快照、PDF、邮件、周期任务 |
| `internal/watch` | 业务核心：提交、观察、状态机、调度策略、通知决策（不依赖 HTTP 与具体存储） |
| `internal/auth` | 邮件验证码、会话、鉴权中间件、限流 |
| `internal/mail` | Mailer 接口、SMTP 实现、邮件模板 |
| `internal/api` | HTTP handler（Huma），生成 OpenAPI 文档 |
| `internal/snapserver` | `snap.<domain>` 快照与资源服务 |
| `internal/config` | 环境变量配置 |
| `web/` | Vue 3 前端 |

依赖方向：`api / jobs → watch → (wechat, snapshot, store, blob, mail 的接口)`。`watch` 只依赖接口，便于单元测试，也为未来的公众号/小程序入口复用。

---

## 4. 技术栈

| 层 | 选择 | 理由 / 备选 |
|---|---|---|
| 后端语言 | **Go**（当前稳定版） | 兼容性承诺强、单静态二进制、内存占用低、并发原生 |
| HTTP / API | 标准库 `net/http` + **Huma v2** | 代码即契约，自动输出 OpenAPI 3.1，供前端与未来小程序生成类型 |
| 数据库 | **Postgres 18** | 自带 `uuidv7()`；同时承载任务队列 |
| DB 访问 | **pgx v5 + sqlc** | 手写 SQL，生成类型安全代码，不用 ORM |
| 迁移 | **goose** | SQL 文件迁移，启动时自动执行（advisory lock 防并发） |
| 任务队列 / 调度 | **River** | Postgres 原生：重试、唯一任务、周期任务、事务内入队、Snooze；不需要 Redis |
| 抓取 | `net/http`（自动 gzip）+ **goquery** | 微信文章结构固定，无需通用 readability |
| PDF | **浏览器打印**（阅读版内置 `@media print` 样式） | 服务器零开销；如以后需要服务端直出 PDF，用纯 Go PDF 库按快照结构排版（嵌入中文字体子集），不引入浏览器 |
| 浏览器调试 | 开发者本机的 Chromium | 只用于分析拦截类型、对比页面，不部署到服务器 |
| HTML 清洗 | **bluemonday** | 白名单清洗 |
| 对象存储 | **minio-go**（S3 兼容） | 对接 Wasabi，API 比 aws-sdk 轻 |
| 邮件 | **wneessen/go-mail**（SMTP）→ Resend SMTP | 走标准 SMTP，换服务商只改配置（SES / 其他） |
| 配置 | `caarlos0/env` | 环境变量 → 结构体 |
| 日志 | `log/slog`（JSON → stdout） | Docker 负责日志轮转 |
| 限流 | `golang.org/x/time/rate` | 单实例内存令牌桶足够 |
| 前端 | **Vue 3 + Vite + TypeScript** + vue-router | 构建产物 `go:embed` 进二进制；未来小程序可用 uni-app（Vue 语法）复用经验 |
| 前端 API 类型 | `openapi-typescript` + `openapi-fetch` | 由后端 OpenAPI 自动生成，避免手写类型 |
| 前端样式 | 原生 CSS（不引入大型组件库） | 页面少，减少依赖与升级负担 |
| 反向代理 / TLS | **Caddy 2** | 自动申请/续期 Let's Encrypt 证书 |
| 编排 | **Docker Compose** | 3 个容器：caddy / app / postgres |
| CI/CD | GitHub Actions + **GHCR** | lint、测试、构建镜像、tag 触发部署 |
| 代码质量 | golangci-lint、`go test -race`、`vue-tsc` | |
| 监控 | `/healthz` + **Healthchecks.io** 心跳（免费） | 任务停摆、金丝雀失败时邮件告警 |

> Node.js 只在构建前端时使用（本地与 CI），不进入运行时。

---

## 5. 领域模型

### 5.1 核心概念

- **Article（文章）**：全局唯一的一篇微信文章。多个用户提交同一篇文章时只抓取、只检查一次。
- **Snapshot（快照）**：某一时刻文章内容的备份。第一阶段每篇文章在首次成功抓取时生成一份；未来可在检测到内容变化时追加。
- **Asset（资源）**：快照引用的图片，按 sha256 内容寻址去重。
- **Watch（观察）**：某用户对某文章的观察关系，有自己的观察期与结束原因。
- **CheckRun（检查记录）**：每次检查的结果，用于排障与统计。

### 5.2 文章标识与去重

微信文章链接有多种形态，例如短链 `https://mp.weixin.qq.com/s/<token>`、长链 `https://mp.weixin.qq.com/s?__biz=..&mid=..&idx=..&sn=..&<追踪参数>`。

1. **提交时预规范化**：仅接受 `mp.weixin.qq.com/s` 开头的链接；短链去掉 query 与 fragment；长链的别名 key 只保留 `__biz, mid, idx, sn`（按固定顺序），**但请求地址要保留 `chksm`**。实测长链缺少 `chksm` 时，微信一定会返回验证页。临时链接（带 `timestamp` 和 `signature`）会过期，直接拒绝。结果写入 `url_aliases` 查重。
2. **抓取后确定规范 ID**：从页面脚本变量 `biz, mid, idx, sn` 得到 `canonical_key = biz:mid:idx`；若已存在同 key 的文章，则把新别名并入已有文章。之后的检查一律使用页面中的 `msg_link`（即短链）作为请求地址。
3. 实现见 `internal/wechat`（`link.go`、`analyze.go`），M0 已用真实页面验证（附录 B）。

### 5.3 文章状态机

```
            首次抓取成功                     确认删除（两次检查一致）
 pending ───────────────▶ alive ────────────────────────────▶ deleted（终态）
    │                       │
    │ 首次抓取即判定删除/无效   │ 无活跃观察
    ▼                       ▼
 invalid（终态）          dormant（停止检查；有新观察时回到 alive 并恢复调度）
```

- **检查结果**有五种：`alive` / `deleted(原因)` / `invalid`（参数错误、链接过期）/ `blocked`（验证码、环境异常、被限流）/ `error`（网络、5xx、解析失败、无法识别的页面）。
- 只有 `alive`、`deleted`、`invalid` 会改变文章状态；`blocked` 和 `error` 只累加 `consecutive_failures` 并退避重试，连续超过阈值（默认 6 次）触发告警。
- **删除需确认**：首次判定为 `deleted` 后，10 分钟后再检查一次，两次一致才进入 `deleted`。确认时在事务内锁住文章行、核对当前状态和首次判定时间：间隔不足 10 分钟的检查（如管理员手动触发）不算确认，只把下次检查定在首次判定后 10 分钟。文章从 `dormant` 恢复时清掉之前的首次判定，因为中间可能恢复过。
- 删除原因原样记录页面提示文字，并映射到分类：`author_deleted`（作者删除）、`violation`（违规）、`complaint`（被投诉）、`account_blocked`（账号封禁/迁移）、`unknown`。

### 5.4 观察状态机

```
 resolving ──▶ active ──┬──▶ ended(deleted)        文章被删 → 立即邮件通知
    │                   ├──▶ ended(expired)        观察期满 → 按用户设置决定是否通知
    │                   └──▶ ended(user_stopped)   用户主动停止
    └──▶ failed(invalid)   提交的链接无法解析或首次即不可访问
```

- 默认观察期 **60 天**（配置项 `WATCH_DAYS`）。
- 同一用户对同一文章只允许一个 `active` 观察。
- 观察结束后，用户仍可查看该文章的快照（快照归属文章，访问权限来自"曾经观察过"）。

### 5.5 检查调度策略

文章的 `next_check_at` 根据其首次入库后的时长决定：

| 文章入库时长 | 检查间隔 |
|---|---|
| 不满 24 小时 | 3 小时 |
| 满 24 小时、不满 7 天 | 8 小时 |
| 满 7 天 | 24 小时 |

- 每次在间隔上加 ±20% 随机抖动，避免集中请求。
- 全局抓取限速：同一时刻最多 1 个抓取请求，两次请求最少间隔 3 秒（可配置）。
- 遇到 `blocked`：指数退避（5 分钟起，上限 2 小时）；短时间内多篇文章都被拦截时触发**全局熔断**，暂停所有抓取 30 分钟。
- 文章没有活跃观察时进入 `dormant`，停止检查。
- 一个周期任务（每分钟）领取 `next_check_at <= now()` 的文章并入队检查任务；River 的唯一任务机制保证同一文章不会重复排队。

**容量估算**按串行 3 秒一次计算，理论上限为每小时 1200 次。若文章都在首日，每篇每 3 小时检查一次，对应约 3600 篇同时观察的文章。

**流量估算**（按 M0 实测数据修正，见附录 B）：正文页解码后约 4 MB，gzip 后约 880 KB。定期检查不下载整页，有两种读取方式：

| 检查方式 | 读到哪里为止 | 实测每次流量 | 60 天约 79 次（8 + 18 + 53） |
|---|---|---|---|
| `check`：确认正文节点存在 | `#js_content` 之后 64 KB | ~210 KB | ~17 MB |
| `head`：只读页面头部（实验性） | Open Graph 标签 | ~26 KB | ~2 MB |
| 已删除、验证页 | 页面本身很小 | ~16 KB | — |

另外，首次快照（整页加图片下载，再上传 Wasabi）约 10 MB/篇。

- 全部使用 `check` 约 27 MB/篇。500 GB 月流量扣除同机代理约 20 GB、为用户查看预留的约 200 GB 后，大约能支撑每月 1 万篇新文章。
- 若用 `head` 日常检查，每天额外用 `check` 复核一次，约 25 MB/篇。
- **第一阶段先全部用 `check`**。M0 中删除页和验证页都没有 Open Graph 标签，但样本还不全；M2 补齐投诉、封号等删除样本后，再决定是否切换到 `head`。

### 5.6 通知策略

- **文章被删除**：周期任务每 5 分钟扫描一次"已结束但未通知"的观察，按用户合并成一封摘要邮件（同一用户这段时间内的多篇删除只发一封）。用扫描而不是每次删除单独入队，任务失败或并发时都不会漏发。
- **观察期满**：不发邮件（2026-11-20 确定：只在文章被删除时发邮件）。观察到期时直接标记为已通知；网站上照常显示"已到期"，备份保留。原先的"到期也发邮件"开关已删除（迁移 00005 删除 `users.notify_on_expire`）。
- 邮件只包含标题、状态、删除原因和**查看链接**，不带附件。
- **每日发送上限保护**：Resend 免费档每日 100 封。通知和邀请邮件只用到额度的 90%，剩下 10% 留给验证码邮件。发送遇到限额（本地计数或服务商返回）时，摘要和邀请任务顺延到次日 UTC 零点之后。当日发送量达到 80% 时给管理员发告警。
- **每个账号每月最多 20 封通知邮件**（2026-11-20 加入，`MONTHLY_EMAIL_LIMIT`，按北京时间自然月计）：只计删除通知邮件，验证码、邀请与管理员告警不计。达到上限后摘要任务顺延到下月 1 日 00:05，届时把这期间的全部变化合并成一封；第 20 封邮件里会说明已达上限。每个账号可单独设置上限（`users.monthly_email_limit`，空值用默认值，0 表示不限），为以后付费提高上限预留：`observer admin email-limit <email> <n|default>`。设置页显示"本月已收到 X / 20 封"。
- **验证码邮件同步发送，不进任务队列**：任务参数以明文存在 `river_job` 表里，验证码不能明文落库。额度用完时，登录接口直接返回 503"今天的邮件发送额度已用完"。
- **管理员告警**（`ADMIN_EMAILS`）：金丝雀失败、熔断打开、单篇文章连续 6 次检查无结论、邮件用量达到 80%。同类告警 6 小时内只发一次，并且都会写入错误日志。

---

## 6. 关键流程

### 6.1 登录（邮箱验证码）

1. 用户在登录页输入邮箱 → `POST /api/v1/auth/code`，服务端发送 6 位数字验证码（标题里也带验证码，手机通知栏可直接看到）。
2. 验证码只存哈希，10 分钟有效，一次性使用；每个验证码最多尝试 5 次，只有最近一次发送的验证码有效。
3. 用户在**同一页面**输入验证码 → `POST /api/v1/auth/verify {email, code}`。
4. 验证成功：首次登录则创建用户（开放模式，或邀请制下通过公共邀请链接，见下文）；签发会话 token（256 位随机数，哈希存库，60 天滑动过期：每次使用时、最多每小时一次把服务端过期时间延后到 60 天后，并重新下发同样有效期的 Cookie，否则浏览器会在登录 60 天后丢掉 Cookie），写入 `__Host-session` Cookie（`Secure; HttpOnly; SameSite=Lax`）。2026-11-21 由 30 天改为 60 天，并补上 Cookie 续期。
5. 发送限制：
   - **每个邮箱每天最多 2 封**（按北京时间自然日），两次之间至少隔 60 秒。用完后接口返回 429"这个邮箱今天已获取过 2 次验证码，请明天再试"。
   - 同一 IP 每小时最多 10 次。
   - 计数对所有邮箱一视同仁（无论是否有账号、是否被邀请、是否停用），响应也相同，不泄露邮箱是否注册。发信失败时退还这次计数。
   - 接口返回当天剩余次数和可重发的等待秒数，供前端显示倒计时。

日常登录时由验证码验证邮箱所有权，解决了旧系统"可绑定他人邮箱"的问题。

**邀请**（邀请制下，2026-11-20 实现）：只有已有账户的邮箱能收到验证码，所以"邀请"就是为这个邮箱建好账户。
- 用户在设置页填写对方邮箱即可邀请，**每人每月 5 个**（北京时间自然月，`MONTHLY_INVITE_LIMIT`，0 表示只有管理员能邀请）；管理员不限，也可以在后台账户页或用 `observer admin invite` 邀请。
- 设置页、管理后台和命令行都会自动为尚未首次登录的有效账户发送邀请邮件，提供纯文本和 HTML 两种格式。收件人点击邮件中的链接，在打开的页面上再点「接受邀请并注册」，浏览器即为该邮箱完成首次登录，进入「我的观察」。打开链接本身不兑换令牌，以免会执行脚本的邮件安全扫描器用掉链接并拿到会话（2026-11-21）。已登录其他账号的浏览器会先提示确认切换。
- 邀请和 River 发信任务在同一事务内写入。任务参数只有邀请 ID，投递时生成 256 位随机令牌，数据库只存哈希。链接自投递起 7 天内有效，令牌放在主站链接的片段中，由前端取出后 POST 兑换会话。已撤回、已首次登录（含验证码登录）、已停用或已过期的链接统一返回 410。链接过期后，仍受邀的邮箱可在登录页用验证码登录。
- 同一邀请在链接有效期内只发一次；链接过期且未使用时，重复邀请会补发一封新链接（只看邀请本身的状态，回答与对方是否有账户无关）。撤回后重新邀请会生成新链接，同一邮箱一小时内最多收到一封邀请邮件。重复提交升级前创建的邀请可补发首封邮件。
- 每次邀请都计入名额，包括邀请已有账户的邮箱，回答也完全相同，所以邀请不会泄露某个邮箱是否注册过；只有重复邀请自己邀请过的邮箱不计数。已有的账户不会被改动（停用的仍是停用）。不能邀请自己。
- 邀请关系记在 `invites` 表（迁移 00006）：每次邀请一行（邀请人、邮箱、时间）；只有当这次邀请新建了账户时才记 `invitee_id`，这就是"由谁邀请"。删除账户时，带有该邮箱的邀请记录一并删除；该用户发出的邀请中，建了账户的保留（邀请人显示为"已注销的账户"），其余删除。
- 开放注册模式下不需要邀请，设置页不显示邀请卡片，接口返回 409。
- 邀请记录显示"待接受 / 已注册"：只有这次邀请新建的账户登录过才算已注册，邀请已有账户的邮箱永远显示待接受，所以状态同样不泄露邮箱是否注册过。
- 待接受的邀请可以撤回（2026-11-21）：删除这条邀请。已排队过邀请邮件的撤回后**不退还名额**（2026-11-21 修正：否则"邀请—撤回"循环可以不限量地向任意地址发信，挤占删除通知的日额度），`revoked_invites` 表（迁移 00012）记下邀请人和原邀请时间，不存邮箱；升级前创建、从未发过邮件的邀请撤回后仍退还名额。是否退还只看邀请是否排过队，与对方是否有账户无关。这次邀请建的账户若从未登录就一并删除；如果还有别人邀请过同一邮箱，账户转给其中最早的一条邀请，对方仍能登录。已注册的不能撤回（409）。撤回时先锁邀请人、再锁被邀请人的账户行；登录时也锁账户行，两者不会交错。

**公共邀请链接**（邀请制下，2026-11-22 实现）：管理员可以开一个公开链接，在一段时间内提供一定数量的注册名额，适合发到群里或公众号文章里。
- 只有管理员能创建（后台「邀请链接」页，或 `observer admin invite-link create -n <名额> -valid 72h -note …`）。创建时设定名额（1～1000）和有效期（1 小时～30 天，后台提供 1 / 3 / 7 / 14 / 30 天），备注只给管理员看。开放注册模式下不能创建（409）。
- 链接形如 `/join#t=<令牌>`。令牌是链接 ID 加上 HMAC（密钥由 `SIGNING_KEY` 派生，不与快照签名共用），数据库不存秘密，后台随时可以再次复制。轮换 `SIGNING_KEY` 后所有邀请链接一起失效。
- 打开链接仍要用邮箱验证码证明邮箱归属，链接本身不能直接建账户。`/join` 是带邀请的登录页：先 `POST /auth/invite-link` 查询链接状态，有效时只显示到期时间，不显示剩余名额；请求验证码和登录时都带上 `invite_token`。令牌存在 sessionStorage 里，切到邮件 App 再回来刷新也不丢。已登录的浏览器会提示先退出登录。
- 带有效链接时，没有账户的邮箱也能收到验证码；已有账户收到的是普通登录码，回答完全相同，不泄露邮箱是否注册过。停用的账户照旧静默不发。
- **名额只在真正新建账户时扣除**：登录事务里先锁链接行，确认未撤销、未过期、仍有名额，再建账户并加一。已有账户通过链接验证只是普通登录，不检查链接，也不占名额。同时抢最后一个名额的，只有一个能成功。已用名额是计数器，注销账户不退还。
- **每个链接的发码上限**为 `max(3 × 名额, 10)`，防止链接流传出去后被用来向任意地址发信、耗光每日邮件额度。带链接的请求不论邮箱是否有账户都计数；单个邮箱的发码限制没通过或发信失败时退还。
- 过期、名额用完、已撤销、令牌无效统一返回 410，页面显示"邀请已失效"，并给出邮箱登录入口。撤销立即生效，已注册的账户不受影响；链接记录保留，不删除。
- 来源分开记录：私人邀请记在 `invites`，公共邀请记在 `users.invite_link_id`（迁移 00013 的 `invite_links` 表）。后台账户详情的「邀请人」显示"公开邀请「备注」"（没有备注时显示创建日期），点击后只看这个链接注册的账户（`/admin/users?invite_link=<id>`）。
- 开放注册模式下 `/join` 直接转到登录页，接口忽略令牌。

**日常登录采用验证码的原因**（2026-11-20 决定）：
- 用户多在手机浏览器或微信内置浏览器里使用本站，而邮件常在 QQ 邮箱等 App 里看；点链接会在邮件 App 的内置浏览器里登录，回到原页面仍是未登录。验证码在原页面输入，登录就发生在原浏览器里。
- 每天只有 2 次机会，魔法链接"登录到了错误的浏览器"会白白浪费一次。
- 不受邮件安全扫描器预取链接的影响；国内用户也更熟悉验证码。
- 代价：6 位验证码靠尝试次数保证安全（每天最多猜 10 次，命中概率约十万分之一），可以接受。

### 6.2 提交文章

1. `POST /api/v1/watches {url}` → 预规范化、校验域名。
2. 命中 `url_aliases` 且文章状态为 `alive`：直接创建 `active` 观察，返回 201。
3. 否则创建 `resolving` 观察，在同一事务内入队高优先级抓取任务，返回 202；前端轮询观察详情。
4. 抓取完成后：先把原始页面存入对象存储；再在一个事务内确定规范 ID → 合并或创建文章 → 观察转为 `active`（或 `failed`）→ 入队快照任务。快照任务异步读取原始页面、下载图片并生成阅读版，所以观察可能比快照早几秒到几十秒生效，前端需显示"备份中"。
   - 同一用户用两种链接提交了同一篇文章时，合并后只保留一个观察，多出来的 `resolving` 观察会被删除。M3 的 API 在轮询到已删除的观察时，应返回该用户对同一文章的现有观察。
5. 配额：每用户最多 50 个活跃观察，每日最多提交 30 次（可配置），管理员不限。提交在同一用户的行锁下依次完成查重、配额校验、创建和入队，并发请求不会一起越过上限；提交已在观察的文章直接返回原观察，不受配额限制。重新观察（§6.5）同样在用户锁下检查同时观察上限。

### 6.3 抓取与快照生成

1. 用 HTTP 客户端抓取页面（移动端 UA，每次任务内 UA 固定，自动 gzip，10 秒超时，**只允许请求白名单域名**，重定向后再次校验）。
2. 检测结果为 `blocked` 时按 §5.5 退避重试，不在服务器上换用浏览器。
3. 解析：标题、公众号名、作者、发布时间、正文节点 `#js_content`。
4. 下载图片：`data-src` 优先，带 `Referer: https://mp.weixin.qq.com/`，串行下载，单图 20 秒超时、上限 20 MB；计算 sha256，Wasabi 中已存在则跳过上传。
5. 清洗正文：移除脚本、事件属性、追踪像素；视频/音频 iframe 替换为占位卡片（附原链接）；图片地址改写为 `snap.<domain>/a/<sha256>`。
6. 生成阅读版 HTML（内置简洁样式模板，含标题、公众号、发布时间、原文链接、备份时间），上传到 Wasabi。
7. 同时上传原始 HTML（gzip），以便日后解析逻辑改进时重新处理。
8. 部分图片下载失败时快照仍然生效，记录 `missing_assets`，后续检查时补抓缺失图片。
9. **抓取记录 `captures`**（2026-11-20，迁移 00007）：原始页面存入对象存储后，在入队快照任务的同一事务里登记一行（原始页面的键、快照 ID、文章链接、抓取时间、状态 `pending`）。快照生成成功时与快照同一事务标为 `done`；页面没有正文区域（`#js_content`，图片、视频消息）时立即标为 `failed / no_content`；其他错误在最后一次重试失败后标为 `failed / error`。
   - 起因：检测器允许"文章模板、有标题但没有 `#js_content`"的页面判为在线（`article_template_no_content_node`），快照生成却要求正文节点，观察生效后详情页一直显示"正在备份"。重试用尽（如对象存储长时间不可用）也是同样结果，而原始页面的位置只存在于任务参数里，无法补救。
   - 观察详情返回 `backup_status`（`none` / `pending` / `ready` / `failed`）和 `backup_error`，前端据此显示"正在备份"或失败原因，不再按"观察中且没有快照"推断。
   - 管理员在文章详情的"抓取记录"里看到每次抓取的结果，失败的可以**从已保存的原始页面重建**（`POST /admin/captures/{id}/rebuild`），不访问微信，文章已删除也可以；重建时记下 `captures.rebuilt_at`（迁移 00008），历次备份据此标明来源是"抓取"还是"原始内容重建"。重建任务带上重建时间参与去重，以免被当成上一次（river 可能还没标记完成的）同一快照任务而丢弃；"重新备份"（重新抓取）仍只用于在线文章。有失败抓取的文章在"有问题"中标为"备份失败"。
   - 图片、视频消息的阅读版尚未支持，需要先采集样本；支持后可对这些失败的抓取逐一重建。

### 6.4 查看与下载

- **在线查看**：前端调用 `POST /api/v1/snapshots/{id}/view-url` 获取签名链接（HMAC，1 小时有效），以 `<iframe sandbox>` 嵌入 `snap.<domain>/s/<id>?sig=...`。
- **下载单文件 HTML**：快照服务在请求时把图片内联为 data URI，流式输出，不缓存成品（`snapshot.SingleFile`：先查齐所有图片的类型，缺图时在发送前就报错；之后逐张读取、边读边 Base64 编码写出，内存占用与图片大小无关；中途出错则断开连接，浏览器显示下载失败而不是保存半个文件）。同时最多 2 个下载，其余最多排队 15 秒，仍满则返回 503。命令行 `admin export` 用同一实现。2026-11-20 之前的实现会把全部图片的编码结果和整份文件放在内存里，已修正。
- **保存为 PDF**：阅读版 HTML 内置打印样式（分页、图片适配纸宽、隐藏无关元素、页脚标注原文链接与备份时间）。用户点击"打印 / 保存为 PDF"后，前端在新标签页打开快照，并提示使用浏览器的打印功能（iOS、安卓需从系统浏览器打开，微信内置浏览器不支持打印）。服务器不生成 PDF。
- 资源 `/a/<sha256>` 内容不可变，设置长期缓存头。读取顺序为 本地磁盘缓存 → Wasabi，回源后写入缓存。计划改为在线查看时图片直连 Wasabi（§8.1，待实施），`/a/` 保留作回退。

### 6.5 观察到期与收尾

周期任务（每 10 分钟）将超过 `expires_at` 的观察标记为 `ended(expired)`（不发邮件，见 §5.6）；文章不再有活跃观察时转为 `dormant`。

**续期**（2026-11-20 实现）：`POST /watches/{id}/renew` 把观察的结束时间设为"从现在起 60 天"（`WATCH_DAYS`）。
- 观察中的：直接延长（只会往后推）。观察详情页在延长至少能多出一天时显示"延长观察"。
- 已到期或已停止、且文章仍在线（`alive` / `dormant`）的：重新变为观察中，清掉结束原因与通知标记；文章若已 `dormant`，恢复为 `alive` 并立即检查一次。详情页显示"重新观察"。这会占用同时观察的名额（超出返回 429），但不计入每日提交次数。管理员不受名额限制。
- 不能续期：文章已删除或失效、提交失败、正在解析；或者用户之后又提交了同一篇文章，已有另一条观察在进行（返回 409）。
- 是否可以续期由 `watch.Renewable` 判断，API 在观察上返回 `can_renew`，前端据此显示按钮。
- 不发到期提醒邮件（节省邮件额度）；列表中最后 7 天显示"还剩 N 天到期"，详情页显示剩余天数。不限续期次数。

### 6.6 金丝雀检测

周期任务（每 6 小时）检查一组配置好的金丝雀链接：一篇长期存在的文章和一篇已确认删除的文章。结果与预期不符时立即告警。这样微信页面结构变化会在第一时间暴露，不会等到用户反馈。

### 6.7 公众号入口（订阅号被动回复，2026-11-21）

用户在个人订阅号的对话里发送文章链接即可开始观察。公众平台开启"服务器配置"后，把用户消息 POST 到 `/wechat/callback`，我们在 5 秒内返回一条文本回复。只用被动回复，不调用任何需要 access_token 的接口（客服消息、模板消息都不用），因此不需要 AppSecret、IP 白名单或企业主体；也因此**不能主动推送**。绑定邮箱后，删除通知会发邮件，也会在用户下次给公众号发消息时随回复送达。实现在 `internal/mp`，绑定的业务规则在 `internal/auth/wechat.go`。

**协议**：明文模式（全程 HTTPS，签名已能防伪造，不需要 AES）。校验 `signature = sha1(sort(token, timestamp, nonce))`（常量时间比较）且时间戳与服务器相差不超过 10 分钟；配置了 `WECHAT_ACCOUNT_ID` 时核对 `ToUserName`；请求体上限 64 KB。整个处理限时 4 秒，超时或出错回复"系统繁忙，请稍后再试"；没有回复时返回 `success`。微信 5 秒内没收到回复会重试 3 次，同一 OpenID 的相同 MsgId（事件按 OpenID、CreateTime 和事件类型）同时到达时共用一次处理，成功的回复在内存缓存 1 分钟，供重试原样取回。每个 OpenID 30 条消息的令牌桶，每 20 秒补 1 条，超出回复"发送太频繁"。日志只记 OpenID 哈希的前 8 位。

**消息与回复**（文本里用 `<a href>` 链接，单条不超过 2048 字节）：
- 关注：欢迎语与用法；未绑定时附绑定链接。取消关注事件不受消息频率限制，删除绑定和该 OpenID 的全部链接，不回复。停用账号仍可解绑，其他指令只回复“这个账号已停用”。
- 文章链接（文本中提取，与前端 `extractLink` 规则相同；或"链接"类型消息）：未绑定时暂存链接并回复绑定链接，绑定后自动开始观察；已绑定时调用 `watch.SubmitLink`，按结果回复"正在备份""已开始观察《标题》，到某日结束""已经在观察中""已经被删除（页面显示：…）""链接打不开"、配额提示或"账号已停用"，并附观察页链接。
- 整条消息为「我的」/「列表」：绑定的邮箱（打码）、观察中数量、最近 5 条观察（状态用设计规范的词），按字节预算截断。「绑定」「解绑」各司其职；其他内容和图片、语音等回复用法说明。菜单点击等其他事件不回复。
- **公众号与网站的关系**（2026-11-21 补充）：公众号名（`WECHAT_NAME`）与网站名（`SERVICE_NAME`）不同时，回复写"本号配套的「网站名」网站"，绑定页与从绑定链接进入的登录页也写明"「网站名」是公众号「公众号名」配套的文章备份网站"，避免用户以为被引到了别的平台。带链接的回复末尾都附一句：链接打开的是本号配套的网站（主机名），微信可能提示"该网站未完成ICP备案"，可以放心继续访问。
- **邀请制下**：未受邀的邮箱收不到验证码（为不泄露邮箱是否注册，静默不发），所以给未绑定用户的回复写明"网站目前仅限受邀用户使用。已有账号或收到过邀请邮件的，请先绑定邮箱"。这说的是整体规则，不涉及任何具体邮箱。登录页在邀请制下不再写"第一次使用会自动创建账号"。
- **发链接后等首次抓取**：新文章的观察处于 resolving 时，回复最多再等 3 秒（且至少在 4 秒处理时限前 1 秒停下），每 250 ms 查一次：抓取完成就直接回复"已开始观察《标题》"或打不开的原因；若这只是用户已在观察的文章的另一个地址（resolving 记录被合并删除），回复"已经在观察中"；仍未完成就回复"正在备份"，进度链接打开「我的观察」，避免合并后临时观察 ID 失效。首次抓取已被作者删除或违规下架的文章，回复微信页面给出的原因。生产上首次抓取通常 2–4 秒。
- **删除提醒随下一条回复送达**（迁移 00011，`watches.wechat_told_at`）：公众号不能主动推送，所以已绑定用户下次发任何消息（「解绑」除外，停用账号除外）时，回复开头列出绑定之后、最近 30 天内在观察期间确认删除的文章（最多 3 条），各附备份链接。提交一篇早已删除的文章不会产生新的公众号提醒。命令回复和链接都准备好后才标记已告知，并发回复只包含各自成功标记的提醒。邮件照常发送，两者互不影响。
- 网站首页「怎么用」与设置页未绑定时显示公众号二维码（`web/public/wechat-qr.jpg`，只在开启公众号入口时显示；换图需要重新发布）。

**链接与令牌**（`wechat_tokens`，256 位随机数，只存哈希，一次性）：
- 绑定链接 `/wx/bind#t=<令牌>`，30 分钟有效，记 OpenID 和暂存的文章链接。网页上未登录先登录（登录页 `next=/wx/bind`），确认页写明当前邮箱和绑定的后果，可"换一个账号"。绑定在一个事务里完成：消耗令牌 → 锁用户行 → 用咨询锁锁 OpenID → 删掉该 OpenID 或该用户原有的绑定及旧的免登录链接 → 写入新绑定。暂存的文章随后用第二个事务提交，失败时绑定保留，页面显示原因。
- 免登录链接 `/wx/go?to=<站内路径>#t=<令牌>`，24 小时有效，只能登录这个 OpenID **当前**绑定的账号（兑换时在事务里核对绑定）。同一条回复里的链接共用一个令牌：第一次点击登录，之后令牌已用，页面发现已登录就直接打开 `to`，否则去登录。
- 令牌放在 `#` 之后：浏览器不把它发给服务器，不进服务器与 Caddy 日志、不进 Referer、不会被预取消耗。页面读到后立即从地址栏去掉，暂存在 sessionStorage，再 POST 兑换。代价（用户已接受）：把回复转发给别人，对方能在有效期内用它登录一次，或把发送者的微信绑到自己的账号；确认页明示邮箱，公众号里发「我的」会显示绑定的邮箱。

**绑定关系**（`wechat_bindings`）：一个 OpenID 对一个账号，一个账号对一个 OpenID。解除绑定可在设置页或公众号里发「解绑」；取消关注、删除账号时一并删除。

**你需要在平台上做的事**：在微信开发者平台（公众平台已不再提供"服务器配置"）「我的业务 → 公众号 → 基础信息 → 域名与消息推送配置」设置（是公众号本身，不是关联的小程序）：URL `https://<域名>/wechat/callback`，Token 与 `WECHAT_TOKEN` 相同，消息加解密方式选明文模式，数据格式选 XML。启用后，公众平台后台的"自动回复"和"自定义菜单"失效，所有回复由本服务给出；自定义菜单接口对未认证个人号是否开放，上线后在"接口权限"中确认，不在本次范围。建议先用"接口测试号"指向生产环境验证。

**风险**：域名没有 ICP 备案。2026-11-21 上线后真机验证：微信内置浏览器会先显示"该网站未完成ICP备案，无法确认其安全性"，可以继续访问，绑定与免登录链接都能用；回复里因此附了说明。若将来被完全拦截，后备方案是"网页端绑定码"（设置页显示一个码，用户发给公众号完成绑定），尚未实现。

---

## 7. 数据库设计（草案）

```sql
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  email            citext NOT NULL UNIQUE,
  role             text NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_login_at    timestamptz
);

CREATE TABLE login_codes (            -- 邮件验证码
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  email       citext NOT NULL,
  code_hash   bytea NOT NULL,
  attempts    int NOT NULL DEFAULT 0,   -- 最多 5 次
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  request_ip  inet,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE login_requests (         -- 每个邮箱每天（北京时间）的发送计数
  email    citext NOT NULL,
  day      date NOT NULL,
  sent     int NOT NULL,
  last_at  timestamptz NOT NULL,
  PRIMARY KEY (email, day)
);

CREATE TABLE sessions (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash   bytea NOT NULL UNIQUE,
  user_agent   text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL
);

CREATE TABLE articles (
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  canonical_key        text UNIQUE,            -- biz:mid:idx，解析前为 NULL
  canonical_url        text,
  status               text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','alive','deleted','invalid','dormant')),
  title                text,
  account_name         text,
  author               text,
  published_at         timestamptz,
  deleted_reason_code  text,
  deleted_reason_text  text,
  deleted_at           timestamptz,
  first_seen_at        timestamptz NOT NULL DEFAULT now(),
  last_checked_at      timestamptz,
  last_alive_at        timestamptz,
  next_check_at        timestamptz,
  consecutive_failures int NOT NULL DEFAULT 0
);
CREATE INDEX articles_next_check ON articles (next_check_at)
  WHERE status IN ('pending','alive');

CREATE TABLE url_aliases (
  url        text PRIMARY KEY,                 -- 预规范化后的 URL
  article_id uuid NOT NULL REFERENCES articles(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE watches (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  article_id    uuid REFERENCES articles(id),  -- resolving 阶段可能为 NULL
  submitted_url text NOT NULL,
  status        text NOT NULL CHECK (status IN ('resolving','active','ended','failed')),
  end_reason    text CHECK (end_reason IN ('deleted','expired','user_stopped','invalid')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  ended_at      timestamptz,
  notified_at   timestamptz
);
CREATE UNIQUE INDEX watches_one_active ON watches (user_id, article_id)
  WHERE status = 'active';
CREATE INDEX watches_user ON watches (user_id, created_at DESC);
CREATE INDEX watches_article_active ON watches (article_id) WHERE status = 'active';

CREATE TABLE snapshots (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  article_id       uuid NOT NULL REFERENCES articles(id),
  captured_at      timestamptz NOT NULL DEFAULT now(),
  content_hash     bytea NOT NULL,             -- 正文纯文本 sha256，用于未来的变更检测
  title            text,
  html_key         text NOT NULL,              -- Wasabi 对象 key
  raw_key          text NOT NULL,
  size_bytes       bigint NOT NULL,
  asset_count      int NOT NULL,
  missing_assets   int NOT NULL DEFAULT 0
);

CREATE TABLE assets (
  sha256       bytea PRIMARY KEY,
  content_type text NOT NULL,
  size_bytes   bigint NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE snapshot_assets (
  snapshot_id uuid NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  sha256      bytea NOT NULL REFERENCES assets(sha256),
  source_url  text NOT NULL,
  PRIMARY KEY (snapshot_id, sha256)
);

CREATE TABLE check_runs (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  article_id  uuid NOT NULL REFERENCES articles(id),
  checked_at  timestamptz NOT NULL DEFAULT now(),
  mode        text NOT NULL CHECK (mode IN ('full','check','head')),
  result      text NOT NULL CHECK (result IN ('alive','deleted','invalid','blocked','error')),
  reason      text,                            -- 删除原因分类
  rule        text,                            -- 命中的判定规则，便于排障
  http_status int,
  wire_bytes  int,                             -- 实际传输字节数，用于核对流量预算
  duration_ms int,
  detail      text
);
CREATE INDEX check_runs_article ON check_runs (article_id, checked_at DESC);

CREATE TABLE email_log (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
  kind        text NOT NULL CHECK (kind IN ('login','invite','deleted','expired','alert')),
  to_email    citext NOT NULL,
  subject     text NOT NULL,
  status      text NOT NULL CHECK (status IN ('queued','sent','failed')),
  provider_id text,
  last_error  text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  sent_at     timestamptz
);
```

说明：
- 实际迁移文件：`internal/store/migrations/00001_init.sql`（以它为准；上线前可以直接修改，上线后只能追加新迁移）。
- River 的任务表由 River 自带的迁移创建，包装为 goose 的 Go 迁移（版本 2），固定在 River 迁移版本 8。升级 River 后如果它带了新迁移，测试 `TestRiverSchemaPinned` 会失败，提醒追加一个 goose 迁移。River 的部分迁移不能放进同一个事务，所以这一步不在 goose 事务中执行。
- `check_runs` 保留 180 天（周期任务清理），引起状态变化的记录永久保留。
- 过期的 `login_codes`、两天前的 `login_requests` 与过期 `sessions` 每天清理。
- 删除账号时级联删除会话与观察；文章与快照属于公共备份，不随用户删除。
- 公众号入口（§6.7，迁移 00009；00011 给 `watches` 加 `wechat_told_at`，记删除提醒已在回复里告知）：`wechat_bindings(openid PK, user_id UNIQUE, created_at)`、`wechat_tokens(token_hash PK, purpose bind|login, openid, user_id, pending_url, expires_at, used_at)`。删除账号时绑定与该 OpenID 的全部链接一并删除；过期链接每天清理。

---

## 8. 对象存储设计（Wasabi）

```
assets/<sha256 前 2 位>/<sha256>          图片（不可变，内容寻址）
snapshots/<snapshot_id>/page.html        阅读版 HTML（图片引用为 /a/<sha256>）
snapshots/<snapshot_id>/raw.html.gz      原始页面
backups/postgres/<YYYY-MM-DD>.sql.gz                 数据库备份
```

- Bucket 私有，只由 app 访问，用户不直连 Wasabi（当初的理由：大陆访问速度不可控，并且可以把 Wasabi 出站量控制在存储量以内）。**2026-11-20 已决定图片改为直连，见 §8.1，尚未实施**；阅读版、原始页面、数据库备份仍然私有。
- **90 天最短存储期**：除数据库备份外，所有对象都是长期保留，不受影响；数据库备份默认保留 120 天。**不要把 Wasabi 当临时存储用**，临时文件只放本地磁盘。
- 本地磁盘 LRU 缓存上限 5 GB（配置项），存放热点图片与快照 HTML。只在读取（快照域服务）时用到，所以与快照域一起在 M3 实现。
- 开发与测试用本地目录（`BLOB_DIR`）代替 S3，代码路径相同；S3 实现另有一个连真实 bucket 的测试（`TestS3`，需设置 `TEST_S3_*`），M2 已在 VPS 上对 Wasabi 跑通。
- Region 选 **新加坡 `ap-southeast-1`**（`s3.ap-southeast-1.wasabisys.com`）：M0 从香港 VPS 实测连接耗时 34 ms，是所有区域中最低的（东京 55 ms，Wasabi 没有香港区域）。Bucket 的区域在创建时确定，Wasabi 套餐按账号计费，新建 bucket 不增加费用。
- 已创建 bucket `observer-backup-001`（2026-11-19）：私有、未开启版本控制与 Object Lock；程序使用专用子用户 `observer-app` 的密钥，策略只允许访问该 bucket（`netcheck -s3` 已验证列出全部 bucket 会被拒绝）。
- 从 VPS 实测：8 MB 对象上传 13–15 MB/s、下载 4–5 MB/s；64 KB 对象（接近文章图片大小）上传中位数 48–69 ms、下载 76–101 ms。一篇 55 张图的文章串行上传约 3–4 秒。

### 8.1 图片直连 Wasabi（2026-11-20 决定，待实施）

**起因**：文章浏览的流量几乎全是图片（一篇几 MB，阅读版 HTML 只有几十 KB）。图片现在全部经 VPS 转发：VPS 到大陆的线路带宽有限，并发时互相拥塞；据实际使用，大陆直连 Wasabi 取图明显快于经 VPS。当初"大陆访问 Wasabi 速度不可控"只是推测，M0 没有对比过大陆直连 Wasabi 与经 VPS 的速度。

**出站配额不是问题**：同一 Wasabi 账号下其他应用已有接近 1 TB 的存储量，公平使用条款按账号的存储量比较，本服务的图片出站远低于此。（§2 中"出站 ≤ 存储量"的约束因此不再限制本设计。）

**方案**：
1. **Bucket 策略**：只给 `assets/*` 开放匿名读（`s3:GetObject`），`snapshots/`、`backups/` 保持私有；不开放列举。图片按内容哈希命名、地址固定，浏览器缓存可以长期生效。用户已接受代价：知道哈希的人都能取到该图片（这些本来就是公众号的公开图片）。下架时删除对应对象即可，与现在相同。
2. **配置 `ASSET_BASE_URL`**（如 `https://s3.ap-southeast-1.wasabisys.com/observer-backup-001/assets/`）：设置后，快照服务在返回阅读版（`GET /s/{id}`）时把 `/a/<sha256>` 改写为 `ASSET_BASE_URL` 下对应的 `<sha256 前 2 位>/<sha256>`；不设置时保持现状。已存的 `page.html` 不改，回滚只需删掉这个配置。Wasabi 不支持自定义域名加 HTTPS，所以只能用 Wasabi 自己的域名。
3. **CSP**：快照页的 `img-src` 加入 `ASSET_BASE_URL` 的源。快照页已有 `<meta name="referrer" content="no-referrer">`，带签名的快照链接不会通过 Referer 泄露给 Wasabi；实施时补一个响应头 `Referrer-Policy: no-referrer` 双重保险。
4. **缓存头**：新上传的图片在 `PutObject` 时带 `Cache-Control: public, max-age=31536000, immutable`（`Content-Type` 已经在上传时设置）。已有图片没有这个头，浏览器会按启发式缓存；需要的话用一次性的原地复制补上元数据（对象都很新，覆盖会产生少量 90 天最短存储期的费用，可以忽略）。
5. **不变的部分**：`/a/<sha256>` 路由保留（作为回退，以及 `ASSET_BASE_URL` 未设置时使用）；单文件下载与 `admin export` 仍在服务器端从 Wasabi 读取并内联；阅读版 HTML 仍经快照域加签名提供。本地磁盘缓存此后主要缓存阅读版和下载时用到的图片，可以视情况调小 `CACHE_MAX_BYTES`。
6. **局限**：快照页禁止脚本，浏览器里没法"直连失败再走服务器"，只能整体开关。VPS 仍是单点：阅读版、签名链接、API 都在它上面，直连只减轻它的带宽压力。

**实施前后验证**：上线前在大陆网络（至少两家运营商）对同一张图比较 `snap.<domain>/a/<sha>` 与 Wasabi 直连的首字节时间和下载速度；上线后浏览器检查在线查看的图片来自 Wasabi、无 CSP 报错，下载与打印 PDF 正常；确认匿名请求 `snapshots/`、`backups/` 下的对象和列举 bucket 都被拒绝。

---

## 9. API 草案（`/api/v1`，JSON）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/code` | `{email, invite_token?}` → 202 `{remaining, resend_after}`；超限 429；邀请链接已失效 410 |
| POST | `/auth/verify` | `{email, code, invite_token?}` → 设置会话 Cookie；通过邀请链接注册时名额已满或链接已失效 410 |
| POST | `/auth/invite-link` | `{token}` → `{expires_at}`；公共邀请链接已失效 410，开放注册 409（§6.1） |
| POST | `/auth/accept-invite` | 用邮件中的 `{token}` 完成首次注册，设置会话 Cookie。链接失效返回 410 |
| GET | `/auth/session` | 当前登录用户（同 `/me`）；未登录时返回 200 且无用户，前端启动时用它，避免访客页面出现 401 报错 |
| POST | `/auth/logout` | 注销当前会话 |
| GET | `/admin/overview` | 管理后台概览（§10.1） |
| GET / POST | `/admin/users` | 账户列表与用量（`invited_by=<用户 id>` 只看该用户邀请注册的账户，`invite_link=<链接 id>` 只看该公共邀请链接注册的账户）/ 自动邮件邀请，统一返回 201 及邀请记录 |
| PATCH | `/admin/users/{id}` | 停用 / 启用、角色、每月通知上限 |
| GET / POST | `/admin/invite-links` | 公共邀请链接列表（状态 `active` / `full` / `expired` / `revoked`、已用 / 名额、现存账户数、可复制的 `url`）/ `{max_uses, valid_hours, note?}` 创建 → 201；开放注册时 409 |
| DELETE | `/admin/invite-links/{id}` | 撤销链接 → 204，已注册的账户保留 |
| GET | `/admin/articles`、`/admin/articles/{id}` | 文章列表（含完整性标记；`status`、`issues`、`issue=<问题类型>` 筛选，第一页带 `matched` 与 `total`）/ 详情（备份带来源 `source`：`fetch` 抓取 / `rebuild` 从原始页面重建） |
| POST | `/admin/articles/{id}/resnapshot` | 重新抓取并生成新备份 |
| POST | `/admin/captures/{id}/rebuild` | 从已保存的原始页面重新生成失败的备份（不访问微信） |
| GET | `/me` | 当前用户信息与设置，含本月通知邮件数与上限（`emails_this_month`、`monthly_emails`） |
| DELETE | `/me` | 删除账号（同时从验证码记录、发信记录和邀请记录中抹去邮箱） |
| GET / POST | `/invites` | 我发出的邀请（`status`：`pending` / `accepted`）与本月名额（`can_invite`、`used_this_month`、`monthly_limit`）/ `{email}` 邀请 → 201；名额用完 429，开放注册时 409（§6.1） |
| DELETE | `/invites/{id}` | 撤回待接受的邀请 → 204（已发过邀请邮件的不退还名额）；已注册 409（§6.1） |
| POST | `/watches` | `{url}` → 201（已备份）/ 202（解析中） |
| GET | `/watches` | 列表，支持 `status` 过滤，游标分页 |
| GET | `/watches/{id}` | 详情（含文章状态、最近检查记录、快照列表、备份状态 `backup_status` / `backup_error`） |
| DELETE | `/watches/{id}` | 停止观察 |
| POST | `/watches/{id}/renew` | 续期：观察中的延长，已到期 / 已停止的重新观察（§6.5）；超出同时观察上限 429，不能续期 409 |
| POST | `/wechat/login` | `{token}`：用公众号回复里的免登录链接登录（设置会话 Cookie）；已用、过期或绑定已变 410 |
| POST | `/wechat/bind` | `{token}`：把绑定链接对应的微信绑到当前账号 → `{watch_id?, submit_error?}`（绑定前发来的文章）；失效 410 |
| DELETE | `/me/wechat` | 解除公众号绑定 |
| POST | `/snapshots/{id}/view-url` | 返回签名的在线查看链接 |
| POST | `/snapshots/{id}/download-url` | 返回单文件 HTML 的签名下载链接 |
| GET | `/site` | 公开的服务设置（服务名、注册模式、观察天数、验证码次数、运行的版本号等），前端登录前使用 |
| GET | `/openapi.json`、`/openapi.yaml` | API 契约（`observer openapi` 也可直接输出，前端类型由它生成） |
| GET | `/healthz` | 健康检查（DB 可用、worker 心跳） |

**鉴权**：Web 使用会话 Cookie；未来小程序在 `Authorization: Bearer` 中携带同一种会话 token，共用 `sessions` 表。

**CSRF**：会话 Cookie 使用 `SameSite=Lax`；所有写操作要求 JSON Content-Type 并校验 `Origin`。

**错误格式**：RFC 9457 Problem Details（Huma 默认支持）。

`GET /me` 在已绑定公众号时带 `wechat.bound_at`；`GET /site` 在开启公众号入口时带 `wechat.name`；没有开启时 `/wechat/*` 接口返回 404。公众平台的回调 `GET / POST /wechat/callback` 返回 XML，不走 Huma（§6.7）。

快照域 `snap.<domain>`：`GET /s/{snapshot_id}?sig=...`、`GET /s/{snapshot_id}/single.html?sig=...`、`GET /a/{sha256}`。

主域上 `/api/` 以外的路径都由内嵌的前端提供（`internal/webui`）：带内容哈希的 `/assets/*` 长期缓存，其余路径返回 `index.html`（`no-cache`，附严格 CSP）。

---

## 10. Web 前端

| 路径 | 页面 | 内容 |
|---|---|---|
| `/` | 首页 | 服务说明、三步用法、邀请制提示 |
| `/invite` | 接受邀请 | 从邮件打开，点击「接受邀请并注册」后完成注册并进入「我的观察」，链接失效时提供邮箱登录入口 |
| `/join` | 接受邀请（公共邀请链接） | 带邀请的邮箱验证码登录页：显示到期时间，新邮箱可以注册，已有账号直接登录；链接失效时显示"邀请已失效"和邮箱登录入口（§6.1） |
| `/login` | 登录 | 输入邮箱 → 获取验证码（60 秒重发倒计时、当天剩余次数）→ 同一页面输入验证码，输满 6 位自动提交；步骤存在 `sessionStorage`，切到邮件 App 后页面被重新加载也能继续 |
| `/watches` | 我的观察 | 从分享文字提取链接：一次粘贴（含键盘剪贴板面板插入）里有文章链接时，输入框只保留链接；输入框随内容增高（最多 4 行），手机上按钮排在输入框下方，提示行显示"已识别文章链接"，避免只看到链接末尾而误以为粘贴失败（2026-11-22）。成功后在列表显示「查看」入口；支持按提交时间或到期时间排序，以及全部 / 观察中 / 即将到期（沿用 7 天）/ 已删除 / 已结束筛选（打不开、没能开始观察的链接归入已结束，标"无法访问"）。状态标签只用设计规范的词：观察中、即将到期、已结束、已删除、无法访问，以及备份中；怎样结束的（到期、停止）写在卡片右上角。默认按游标加载，跨页筛选和排序后再分页显示；读完全部页才展示总数。最近检查和备份状态在详情页显示；有解析中的条目时每 5 秒刷新 |
| `/watches/:id` | 观察详情 | 文章信息；已删除时顶部通知写明确认删除的时间和页面显示的原因（不另作推断）；观察期限条（删除时在删除位置标暖色刻线）；检查记录把检查、备份版本和开始观察排成一条时间线，连续"在线"的中间部分折叠为"另有 N 次检查，均在线"；备份在 iframe 中预览（手机上默认收起），可独立打开阅读页、在线预览、下载离线 HTML、打印为 PDF（按 iOS / 安卓 / 桌面给出操作步骤）；微信内置浏览器中提示改用系统浏览器；备份生成前自动轮询（前 1 分钟每 3 秒，之后每 10 秒，最多 10 分钟）；观察到期日与剩余天数，延长观察 / 重新开始观察（§6.5）；停止观察（二次确认） |
| `/watches/:id/backup/:version` | 独立阅读 | 版本地址使用快照 ID；可切换版本、下载当前版本或打开对应的快照打印。正文继续由隔离的快照域通过 sandbox iframe 提供 |
| `/wx/bind`、`/wx/go` | 公众号链接 | 公众号回复里的绑定链接与免登录链接（§6.7）：令牌在 `#` 之后，读出后立即去掉；绑定前确认当前邮箱，可换账号；绑定后显示绑定前发送的文章是否已开始观察 |
| `/settings` | 设置 | 邮箱、退出登录、本月通知邮件数与上限、开启公众号入口时的「微信公众号」（未绑定说明如何绑定，已绑定显示日期、可解除）、邀请朋友（邀请制下：本月剩余名额、邮件邀请表单、邀请记录及其"待接受 / 已注册"状态，待接受的可撤回）、删除账号（在确认弹窗输入当前账号邮箱） |
| `/about`、`/privacy`、`/terms` | 关于 / 隐私说明 / 服务条款 | 静态页（按需加载）；关于页末尾显示当前版本，正式版本链接到 GitHub 上对应标签的源代码 |

- 技术：Vue 3 + vue-router + TypeScript，原生 CSS（浅色 / 深色跟随系统），不引入组件库；API 类型由 `observer openapi` → `openapi-typescript` 生成，请求用 `openapi-fetch`。品牌标识、得意黑标题字体和 JetBrains Mono 随前端提供，桌面双栏在手机上改为单栏，主导航可展开。生产包脚本约 60 KB、样式约 9 KB（gzip）。
- 界面遵循折光观察者设计系统：时间一律 24 小时制，当天 `HH:mm`、跨天 `MM-DD HH:mm`、完整日期 `YYYY-MM-DD`；图标为内联 Lucide（`ZgIcon`）。
- 得意黑只用于我们自己的标题，完整字体（约 1.1 MB，`web/fonts/`，不打包）按界面文字裁成子集（约 80 KB）。改了界面文字后运行 `make web-fonts`（需要 fonttools 与 brotli）；`tests/font.test.ts` 在有字符没进子集时失败。来自别处的标题（自定义服务名）缺字时逐字回退到系统字体。
- 构建：`make web` 用 Vite 构建到 `internal/webui/dist/app`，`go build` 时通过 `go:embed` 嵌入；没构建前端的二进制在页面路径返回 503 提示。构建目标兼容 Chrome 87 / Safari 14（较旧的微信内置浏览器）。
- 主域 CSP：`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-src <快照域>; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`，不需要任何 `unsafe-*`。
- 会话过期时，任何 API 请求返回 401 都会把用户带回登录页，登录后回到原来的页面（只允许站内路径）。
- 移动端优先：大部分用户会在微信内置浏览器或手机浏览器中使用。

### 10.1 管理后台（2026-11-20 提前实现）

`/admin`，只对 `role = admin` 的账户开放（前端路由与 `/api/v1/admin/*` 都会检查；非管理员看到"页面不存在"，接口返回 403）。设置角色：`observer admin role <email> admin`，或在后台的账户页。代码按需加载，普通用户不会下载。

| 页面 | 内容 |
|---|---|
| 概览 `/admin` | 运行状态（数据库、后台任务心跳、版本）；**本月用量**（北京时间自然月）：邮件（验证码 / 邀请 / 通知 / 告警 / 失败）、今日邮件对服务商日额度（UTC 0 点重置）、检查次数与拦截率、微信页面抓取流量、新保存图片量、新增账户 / 观察 / 文章 / 备份、发现删除；**今天**的同类数字；**累计**：账户、观察、文章各状态、备份、对象存储（图片 + 阅读页）；**近 30 天**每日趋势（新观察、新账户、新备份、发现删除、检查、被拦截、邮件）小图 + 数据表 |
| 账户 `/admin/users` | 搜索、邀请、状态筛选与每页 20 条的翻页；详情面板显示每个账户的角色、状态、观察中 / 累计、本月提交、本月通知 / 上限（单独设置的标 *）、邀请人（点击查看；来自公共邀请链接的显示链接备注，点击筛选出该链接注册的账户）、邀请注册的账户数（点击筛选出这些账户）与本月发出的邀请数、最近登录、注册时间、公众号绑定日期（开启公众号入口时）；停用 / 启用（停用同时退出所有设备）、设为 / 取消管理员、调整每月通知上限或恢复默认。不能停用自己或取消自己的管理员 |
| 邀请链接 `/admin/invites` | 新建公共邀请链接（名额、有效期、备注），创建后自动复制；列表显示备注、创建时间与创建人、状态（有效 / 名额已满 / 已过期 / 已撤销）、已用 / 名额、到期时间；可复制链接（浏览器不允许时显示出来手动复制）、点两次撤销、查看该链接注册的账户 |
| 文章 `/admin/articles` | 标签：全部 / 观察中 / 已删除 / 已结束 / 备份失败 / 图片缺失 / 检查异常（文章状态用设计规范的词：在线检查中为观察中、休眠为已结束、无效为无法访问、解析中为备份中）；概览"需要处理"链接到全部问题，筛选在服务端完成；工具栏显示"匹配数 / 总数"；按标题 / 公众号搜索并翻页；每篇显示观察人数、备份数、最新备份的图片数与缺失数、正文字数、问题标签（备份失败、缺少备份、检查异常为红色，图片缺失、正文过短为赭黄） |
| 文章详情 | 文章信息与问题说明；全部备份（来源：抓取 / 原始内容重建；图片、缺失、正文字数、页面大小），可预览、新窗口打开、下载；**重新备份**（文章在线时：按全局限速重新抓取完整页面，生成新备份，旧的保留）；**抓取记录**（每次抓取的结果与失败原因，失败的可从原始页面重建，§6.3）；观察者；最近 50 次检查（方式、结果、规则、HTTP 状态、流量、耗时） |

- "有问题"的判定（`api.articleIssues` 与 `AdminListArticles` 查询保持一致）：最近一次抓取没能生成备份（备份失败）；抓取成功却没有备份；最新备份有图片没存下来；正文不到 100 字（纯图片文章也会命中）；连续 3 次以上检查无结论。
- 正文字数从迁移 00004 起记录（`snapshots.text_runes`），之前的备份显示"—"。
- 管理员可以查看任意文章的备份（`view-url` / `download-url` 不再要求观察过该文章）。
- 概览先显示服务状态和常用指标，新增观察与新增备份按天绘制在同一张折线图上，删除检测使用暖色柱状图。「需要处理」链接到问题文章，完整用量、各指标的单系列小图和数据表可展开。

## 11. 抓取与检测（最大技术风险）

### 11.1 M0 实测要回答的问题

> 已于 2026-11-19 完成，结论见附录 B.3。

在写业务代码之前，用 `observer probe` 原型（纯 HTTP，不依赖浏览器）在**香港 VPS 上**实测，并在开发者本机对比：

1. 纯 HTTP 抓取文章页面的成功率；数据中心 IP 是否频繁触发"环境异常 / 去验证"页面；触发的频率阈值大约是多少。
2. 如果被拦截，判断拦截类型：用本机 Chromium 和本机 `probe` 分别访问同一链接做对比，区分是按 IP / 频率拦截，还是按浏览器特征 / JS 检测拦截。结论决定 §11.3 走哪条预案。
3. 各类页面的特征（保存为测试样本）：正常、作者删除、违规删除、被投诉、账号封禁/迁移、验证页、短链跳转、长链。
4. 如何从页面中可靠提取 `biz/mid/idx/sn`、标题、公众号名、发布时间。
5. 图片防盗链行为：需要哪些请求头；`wx_fmt` 与 webp 的处理方式。
6. Wasabi 各候选 region 从 VPS 访问的延迟与吞吐。

### 11.2 检测实现原则

- 判定规则按"强信号优先"：先识别验证/拦截页（`blocked`），再识别删除提示页（`deleted`），最后要求正文节点存在且非空才算 `alive`。**三者都不满足时归为 `error`，绝不默认判为 `alive`**。
- 规则集中在 `internal/wechat/analyze.go`，每条规则都有对应的样本测试。
- 样本文件放在 `internal/wechat/testdata/`，用 `observer probe -save-fixture` 采集并脱敏（去掉 token、追踪参数），再精简后入库。
- 金丝雀检测（§6.6）兜底发现规则失效。

### 11.3 被拦截严重时的预案（按成本递增）

1. 降低频率、加大间隔与抖动、调整请求头。
2. **本地抓取节点**：在家用宽带的机器上运行 `observer agent`，通过鉴权 API 从服务器领取被拦截的抓取任务并回传结果，借用住宅 IP。服务器仍然只跑后端；节点离线时任务继续排队，不影响其他功能。
3. 用户侧补充：提供书签脚本或浏览器扩展，由用户的浏览器抓取页面后上传（后续阶段）。
4. 付费代理（最后手段，需要重新评估成本）。

> 如果拦截是基于浏览器特征而不是 IP，第 2 条的本地节点可以带上 Chromium。浏览器只在本地节点运行，不上服务器。

---

## 12. 安全与合规

- **快照隔离**：快照是第三方内容，只在 `snap.<domain>` 提供，该域不设置任何登录 Cookie。响应头：
  `Content-Security-Policy: sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; frame-ancestors https://<domain>`。
  此外，入库前已经过 bluemonday 清洗。
  图片改为直连 Wasabi 后（§8.1，待实施），`img-src` 还要加入 `ASSET_BASE_URL` 的源。
  `allow-same-origin` 是必需的：没有它，快照页会变成匿名来源（opaque origin），它对 `/a/` 图片的请求就成了跨源请求，会被图片的 `Cross-Origin-Resource-Policy: same-origin` 拦截（M4 浏览器验收时发现，此前在线查看一直不显示图片）。它只在**禁止脚本**的前提下安全，绝不能再加 `allow-scripts`；测试会检查这一点。前端 iframe 的 `sandbox` 属性同样如此设置。
- **SSRF 防护**：抓取只允许白名单域名，页面只允许 `mp.weixin.qq.com`，资源只允许 `*.qpic.cn`、`*.qlogo.cn`、`res.wx.qq.com`（M0 样本中出现的全部资源域名）。拨号时拒绝内网、回环、链路本地、CGNAT 等非公网地址，重定向后再次校验（已在 `internal/wechat/fetch.go` 实现）。
- **签名链接**：HMAC-SHA256，密钥来自配置，带过期时间；签名绑定快照 ID。
- **会话安全**：token 只存哈希；Cookie 使用 `__Host-` 前缀；用户可以注销全部会话（`admin` 命令也支持）。
- **滥用防护**：登录、提交接口按 IP 与用户限流；用户配额；注册模式配置 `REGISTRATION_MODE=open|invite|closed`（默认 `invite`，即邀请制软启动，见 §16）；后续如有邮件轰炸，可加自托管工作量证明验证（如 ALTCHA），不依赖第三方验证码服务。
- **密钥管理**：所有密钥放在服务器 `.env`（权限 600），不进仓库；代码中不再硬编码任何邮箱、openid 或默认口令。
- **隐私**：只收集提供服务所需的信息，隐私说明页（`/privacy`）逐项写明用途与保存期限，必须与实现一致：
  - 验证码记录（含请求 IP）在过期一天后删除；每个邮箱的每日计数保存 2 天；会话（含 User-Agent）过期或退出后删除；发信记录保存 90 天（`auth.Cleanup` 每天清理）。
  - 删除账号：用户、会话、观察列表级联删除；该邮箱的验证码记录删除；发信记录中的地址改为 `deleted`（保留行用于当天额度计数）；每日计数照常 2 天后过期，避免删号重注册绕过限制。集成测试检查删除后任何表里都不再有该邮箱。
  - 服务访问日志不记录 IP 和查询参数。Caddy 不开访问日志，并且给所有日志加了过滤器：Caddy 自己的警告和错误（例如客户端中途断开时）会附带整个请求，过滤器删除其中的 IP、端口、请求头、TLS 信息和快照链接的签名参数。上线当天的冒烟测试就触发过这类警告，暴露了只关访问日志还不够。
- **公众号入口**（§6.7）：只保存 OpenID（只在本公众号内有效）与绑定时间，不保存消息内容；回复里的一次性链接最长 1 天有效，过期后每天清理；解除绑定、取消关注、删除账号时立即删除绑定。微信回调的查询参数（`signature`、`timestamp`、`nonce`、`openid`、`echostr`、`msg_signature`）也由 Caddy 日志过滤器删除，应用日志只记 OpenID 哈希前缀。隐私说明页在开启公众号入口时显示对应条目。
- **版权与下架**：快照默认仅对观察过该文章的用户可见；提供联系邮箱处理下架请求。
- **HTTP 安全头**：主域设置 HSTS、`X-Content-Type-Options`、严格 CSP（Vue 构建产物不需要 `unsafe-eval`）。

---

## 13. 部署与运维

### 13.1 Compose 服务与内存预算（1 GB 内存）

| 服务 | 镜像 | 常态内存 | 容器上限 |
|---|---|---|---|
| caddy | `caddy:2-alpine` | ~30 MB | 64 MB |
| postgres | `postgres:18-alpine` | 150–250 MB | 320 MB |
| app | `alpine:3.24` + `observer` 二进制 + `postgresql18-client`（备份用，见 §13.4），镜像约 40 MB，非 root 运行 | 40–100 MB | 256 MB |
| 系统 + Docker | — | ~150–200 MB | — |

- 常态总占用约 400–600 MB，1 GB 内存有余量。仍建议配置 1 GB swap，防止偶发峰值导致 OOM。
- **M0 实测提醒**：VPS 实际可见内存 913 MB，部署前已用 494 MB、可用约 420 MB，且没有 swap。
- **x-ui / xray 已停止**（2026-11-20）：原先约占 110 MB，现已腾出。其余为系统服务，其中 journald 约 55 MB、fail2ban 约 39 MB、multipathd 约 27 MB、udisks2 约 10 MB、ModemManager 约 9 MB。2026-11-20 实测已用 392 MB、可用约 520 MB（含缓存）。
  - 停用 VPS 上用不到的 multipathd、udisks2、ModemManager，并限制 journald 日志大小，可省约 50–100 MB。
  - 配置 **2 GB swap**（`vm.swappiness=10`）。
  - 加上 dockerd/containerd（约 70 MB）后，上表各容器合计 220–380 MB，物理内存仍然偏紧。M5 上线后按实测调整：必要时把 Postgres 的 `shared_buffers` 降到 64–96 MB，容器上限降到 256 MB。

### 13.1.1 端口规划

2026-11-20：用户已停掉 x-ui（xray 随之停止），并确定**这台服务器优先保障 Observer**，部署时不再为 x-ui / xray 让路（它们的文件保留，只是不运行）。

| 端口 | 占用者 | 说明 |
|---|---|---|
| 22/tcp | sshd | 不变 |
| 80/tcp | **caddy** | ACME HTTP-01 与跳转 HTTPS |
| 443/tcp | **caddy** | 主站与 `snap.` 子域 |
| 443/udp | caddy（可选） | HTTP/3 |

app 与 Postgres 只在 Compose 内部网络通信，不映射到宿主机端口。
- app 设置 `GOMEMLIMIT=200MiB`；图片下载按流处理，不在内存中整体缓存大文件；单文件 HTML 也以流式输出。
- Postgres 参数（`deploy/compose.yaml`）：`shared_buffers=96MB`、`effective_cache_size=256MB`、`work_mem=4MB`、`maintenance_work_mem=32MB`、`max_connections=30`、`max_wal_size=512MB`；pgx 连接池上限 10。
- 本机按生产配置实测（`deploy/compose.local.yaml`，空库）：caddy 14 MB、app 10 MB、postgres 52 MB。

### 13.2 磁盘规划（30 GB）

系统约 5 GB、镜像约 2 GB、Postgres 数据 < 2 GB（第一阶段）、本地缓存上限 5 GB；Docker 日志设置 `max-size=10m, max-file=3`；部署后自动执行 `docker image prune`。

### 13.3 配置项（环境变量）

已实现（M1）：`DATABASE_URL`（必填，URL 格式）、`DB_MAX_CONNS`（默认 10）、`HTTP_ADDR`（默认 `:8080`）、`BASE_URL`、`LOG_LEVEL`（默认 `info`）、`LOG_FORMAT`（`json` / `text`，默认 `json`）。

已实现（M3）：`SNAP_BASE_URL`（必须与 `BASE_URL` 不同主机）、`SIGNING_KEY`（≥32 字符；快照链接签名，也用于派生公共邀请链接的令牌，轮换后两者都失效）、`TRUST_PROXY_HEADERS`（在 Caddy 后面时设为 true）、`SERVICE_NAME`、`REGISTRATION_MODE`（`open` / `invite`，默认 `invite`）、`MAX_OPEN_WATCHES`（50）、`MAX_DAILY_SUBMISSIONS`（30）、`SMTP_HOST`、`SMTP_PORT`（587；465 走隐式 TLS）、`SMTP_USER`、`SMTP_PASSWORD`、`MAIL_FROM`、`DAILY_EMAIL_LIMIT`（100）、`MONTHLY_EMAIL_LIMIT`（每个账号每月通知邮件数，20；0 表示不限）、`MONTHLY_INVITE_LIMIT`（每个用户每月邀请数，5；0 表示只有管理员能邀请）、`ADMIN_EMAILS`（逗号分隔）、`CACHE_DIR`、`CACHE_MAX_BYTES`（5 GiB）、`HEALTHCHECK_URL`。
公众号入口（2026-11-21，§6.7）：`WECHAT_TOKEN`（公众平台服务器配置的 Token，3–32 位字母或数字；设置了才开启）、`WECHAT_ACCOUNT_ID`（原始 ID `gh_…`，可选，用于核对消息）、`WECHAT_NAME`（公众号名称，网页上提示关注；默认同 `SERVICE_NAME`）。
生产环境取值：`BASE_URL=https://ob.portkey.click`、`SNAP_BASE_URL=https://snap.ob.portkey.click`、`SMTP_HOST=smtp.resend.com`、`SMTP_PORT=465`、`SMTP_USER=resend`、`SMTP_PASSWORD=<Resend API Key，只给发信权限并限定发件域名>`、`MAIL_FROM=折光观察者 <noreply@ob.portkey.click>`。env 文件按 Docker Compose `env_file` 的格式书写（值不加引号，可含空格和 `<>`），`observer mailtest -env-file <文件>` 可直接读取。
`BASE_URL` 为 https 时视为生产环境：`SIGNING_KEY`、`SMTP_HOST` 必填，`SNAP_BASE_URL` 也必须是 https；开发环境缺少它们时，分别退化为随机签名密钥、只把邮件写进日志。

已实现（M2）：`S3_ENDPOINT`、`S3_REGION`、`S3_BUCKET`、`S3_ACCESS_KEY`、`S3_SECRET_KEY`（设置 `S3_BUCKET` 时使用 S3）、`BLOB_DIR`（开发用本地目录，两者至少设置一个）、`WATCH_DAYS`（默认 60）、`FETCH_MIN_INTERVAL`（默认 `3s`，最小 `1s`）、`FETCH_USER_AGENT`（预设名 `ios` / `android` / `desktop` 或完整 UA，默认 `ios`）、`CANARY_ALIVE_URL`、`CANARY_DELETED_URL`。



### 13.4 备份与恢复

- 已实现（`internal/backup`）：每天北京时间 03:30 由 River 周期任务执行 `pg_dump --format=custom`（自带压缩），先写临时文件再上传到 Wasabi `backups/postgres/YYYY-MM-DD.dump`，保留 120 天（每次删除恰好过期的及之前 6 天的，覆盖偶尔漏跑的日子）；失败时告警。只在配置了 S3 时启用。连接信息通过 `PG*` 环境变量传给客户端工具，密码不出现在命令行。
- `observer admin backup` 立即备份；`observer admin restore -db <URL> [-date]` 只恢复到空数据库。本机用 PostgreSQL 18 真实工具演练过：备份 → 恢复到新库 → 数据与迁移版本一致；对在用的库会拒绝。
- 快照、图片、PDF 本身就在 Wasabi 中。
- 恢复手册见 `docs/runbook.md`（新机器 → 安装 Docker → 拉取镜像 → 恢复数据库 → 启动）。上线后在生产环境再演练一次。

### 13.5 监控告警

- `/healthz` 返回数据库连通性和 worker 最近一次心跳时间。
- 调度任务每轮完成后 ping Healthchecks.io；超时未 ping 时由 Healthchecks 发邮件告警。
- 应用内告警（发给 `ADMIN_EMAILS`，有频率限制）：金丝雀失败、连续被拦截/熔断、邮件日用量超过 80%。
- 日志：slog JSON 输出到 stdout；`observer admin stats` 输出统计信息。

### 13.6 CI/CD

- **PR / push**：golangci-lint → `go test -race ./...`（CI 使用 Postgres 18 service container）→ 前端 `vue-tsc` + 构建。
- **tag `v*`**（`.github/workflows/release.yml`）：构建 amd64 镜像并推送 `ghcr.io/mamaship/observer:vX.Y.Z` 与 `latest`；配置了部署密钥时，SSH 到 VPS 运行 `/opt/observer/update.sh vX.Y.Z`。部署密钥在服务器上用 `command=` 限定只能运行 `update.sh`，且只接受版本号格式的标签。`update.sh` 先只更新 app 并等健康检查，失败自动回滚到原镜像。迁移在 app 启动时自动执行。
- 没有发布版本时：开发机 `make image`，`docker save | ssh ... docker load`，`update.sh observer:local`。
- Dependabot（`.github/dependabot.yml`）每月为 Go、npm、GitHub Actions、Dockerfile 与 compose 镜像各开一个汇总 PR；镜像不自动升级大版本（Postgres 18 → 19 需要数据迁移，且 app 镜像里的 `pg_dump` 版本要一起换）。安全更新由 GitHub 随时单独提出。
- Dependabot 不提 TypeScript 与 `@types/node` 的大版本（TypeScript 7 是原生重写版，`openapi-typescript` 要求 ^5、`vue-tsc` 也还不支持；`@types/node` 跟构建用的 Node 22）。2026-11-20 的 #106 因此关闭。Release 只在正式版本标签上更新 `latest`，预发布标签（`v1.2.3-rc.1`）不动它。
- 2026-11-20 处理 Dependabot 安全警告：`minimatch`（<11.0.1）与 `glob`（9.0.0–14.0.1）只经 `@vue/test-utils` → `js-beautify` 进入开发依赖，不进生产构建；在 `web/package.json` 的 `overrides` 里只对 `js-beautify` 这一支升级到 `minimatch@^11.0.9`、`glob@^14.0.6`，`npm audit` 为 0。

---

## 14. 仓库结构

```
.
├── cmd/observer/            # main：serve / migrate / probe / admin
├── internal/
│   ├── api/                 # Huma handlers
│   ├── app/                 # serve 的组装：连接、迁移、worker、HTTP、优雅退出
│   ├── auth/
│   ├── backup/              # 每日 pg_dump 到 Wasabi、恢复
│   ├── blob/
│   ├── config/
│   ├── jobs/
│   ├── mail/
│   │   └── templates/
│   ├── snapserver/
│   ├── snapshot/
│   │   └── templates/       # 阅读版 HTML 模板
│   ├── store/
│   │   ├── migrations/      # goose SQL（River 的迁移在 store.go 中注册）
│   │   ├── storetest/       # 测试用：每个测试一个独立的临时数据库
│   │   ├── queries/         # sqlc 查询
│   │   └── db/              # sqlc 生成代码
│   ├── watch/               # 业务核心
│   ├── webui/               # 内嵌前端（dist/app 由 make web 生成，不进 git）
│   └── wechat/
│       └── testdata/        # 真实页面样本
├── web/                     # Vue 3 + Vite + TS（src/views、src/components、src/lib、tests、e2e/smoke.mjs）
├── deploy/
│   ├── compose.yaml         # 生产：caddy + app + postgres
│   ├── compose.local.yaml   # 在开发机上运行生产配置（localhost:8443，自签证书，本地存储）
│   ├── compose.dev.yaml     # 开发用 Postgres
│   ├── Caddyfile            # 无访问日志
│   ├── .env.example
│   └── update.sh            # 服务器上切换版本（含回滚）；也是部署密钥的强制命令
├── docs/
│   └── runbook.md
├── .github/workflows/
├── Dockerfile
├── .golangci.yml
├── Makefile                 # 常用命令：dev、test、lint、sqlc、build
├── go.mod
├── sqlc.yaml
├── plan.md
├── README.md
└── LICENSE                  # 保持 GPL-3.0
```

旧的 `src/`、`dev_docs/`、`img/`、`.chglog/`、`CHANGELOG.md`、`.all-contributorsrc` 在 M1 删除（存档于 tag `legacy-python`）。README 重写，贡献者名单予以保留。

---

## 15. 测试策略

| 层次 | 内容 | 工具 |
|---|---|---|
| 单元 | URL 规范化、删除检测（基于样本）、HTML 清洗、调度间隔计算、状态机转换、签名链接 | `go test`，表驱动 |
| 集成 | store 查询、任务流程（提交 → 抓取 → 快照 → 删除 → 通知）、验证码登录与每日限制、HTTP API 全流程（`internal/app/flow_test.go`） | 真实 Postgres；blob 用临时目录、mailer 用内存实现；微信用 `watchtest` 按样本回放 |
| 邮件 | 模板渲染；SMTP 发送、额度拒绝识别、登录检查 | 进程内的假 SMTP 服务器；真实送达用 `observer mailtest -to` 手动验证 |
| 对象存储 | `blob` 的读写测试用本地目录实现；S3 实现用 `TestS3` 验证 | 设置 `TEST_S3_*` 时连真实 bucket 运行（在 VPS 上对 Wasabi 跑过），否则跳过 |
| 前端 | 类型检查；工具函数与 API 错误处理的单元测试；登录页组件测试（假 API） | `vue-tsc`、Vitest + happy-dom、`@vue/test-utils` |
| 浏览器验收 | `web/e2e/smoke.mjs`：真实 Chrome、手机尺寸，从登录到打印 PDF 走完整流程 | `playwright-core` + 本机 Chrome；需要真实微信，手动运行，不进 CI |
| 线上 | 金丝雀检测 | §6.6 |

- 需要数据库的测试读取 `TEST_DATABASE_URL`，未设置时跳过；`make test` 会启动本地 Postgres 并设置它。每个测试新建一个独立的临时数据库并在结束时删除，因为不同包的测试是并行运行的。

每个修复的线上问题都必须补充对应的样本或回归测试。

---

## 16. 里程碑

### M0：抓取实测（spike）
> **已完成（2026-11-19）**：本机与香港 VPS 均已实测，结论见附录 B.3。香港 IP 未被拦截，第一阶段不需要本地抓取节点；Wasabi 选新加坡 `ap-southeast-1`。

- 实现最小 `observer probe`（纯 HTTP 抓取 + 初版检测），交叉编译后上传到 VPS 运行；拦截类型的对比分析在本机用 Chromium 完成。
- 采集 §11.1 所列各类样本，形成实测报告（追加到本文档附录）。
- **验收**：回答 §11.1 的 6 个问题；确定是否需要本地抓取节点（§11.3），以及 Wasabi 使用哪个 region。

### M1：项目骨架
> **已完成（2026-11-19）**：旧代码已删除；配置、日志、Postgres + goose + River 迁移、sqlc、River worker（心跳周期任务）、Huma `/api/v1/healthz` 与 `/api/v1/openapi.json`、`serve` / `migrate` / `healthcheck` / `admin stats` 子命令、`compose.dev.yaml`、golangci-lint 配置、CI 工作流。本机验收通过；CI 工作流要等首次推送后才能在 GitHub 上实际运行。空载 RSS 约 21 MB。

- 删除旧代码，初始化 Go module、目录结构、Makefile、配置、日志。
- Postgres + goose 迁移 + sqlc + River 接入；Huma 提供 `/healthz` 与 OpenAPI。
- `compose.dev.yaml` 本地开发环境；CI（lint + test）。
- **验收**：`make dev` 启动全部服务，`/healthz` 正常，CI 通过。

### M2：抓取、检测与快照
> **已完成（2026-11-19）**：`blob`（S3 / 本地目录）、`snapshot`（图片收集与内容寻址存储、样式清洗、bluemonday 二次清洗、阅读版模板与打印样式、单文件 HTML 内联）、`watch`（提交、首次抓取与文章合并、定期检查、删除二次确认、失败退避、全局限速、熔断、到期与休眠、金丝雀、`check_runs` 清理）、River 任务与周期任务、`admin submit / articles / check / export` 命令。
> 验收（本机，真实微信页面）：
> - 2 篇正常文章生成快照（56 张、8 张图，0 缺失）。
> - 作者删除、违规两篇首次抓取即判定不可用。
> - 缺 `chksm` 的长链用占位值抓取成功，并合并到同一篇文章。
> - 定期检查由调度器自动执行，每次约 200 KB，请求间隔约 4 秒。
> - 导出的单文件 HTML 用本机 Chrome 截图与打印 PDF，排版正常。
> - S3 实现已在 VPS 上对 Wasabi 测试通过。
> 未做：本地磁盘缓存移到 M3（与快照域一起）；删除通知、金丝雀告警邮件在 M3 接入（代码中已留位置）。真实文章的"删除二次确认"只有样本测试，没有线上实例。

- `wechat` 包：规范化、抓取、解析、检测，样本测试覆盖全部页面类型。
- `snapshot` 与 `blob`：图片去重上传、阅读版 HTML、原始 HTML 存档、本地缓存。
- 调度、检查任务、熔断、`check_runs`、金丝雀检测。
- **验收**：用 CLI 提交一组真实链接，能生成快照、按策略定时检查，对删除样本给出正确判定。

### M3：账号、API 与通知
> **已完成（2026-11-20）**
> - 邮箱验证码登录（每个邮箱每天 2 封、只存哈希、同步发信；2026-11-20 由魔法链接改为验证码，理由见 §6.1）、会话（Cookie 与 Bearer 两种，30 天滑动过期；2026-11-21 改为 60 天并补上 Cookie 续期）、邀请制、按 IP 与按邮箱限流、配额。
> - 全部 `/api/v1` 接口（§9），OpenAPI 文档自动生成；跨站写请求按 `Origin` / `Sec-Fetch-Site` 拒绝。
> - 快照域服务：签名链接、沙箱 CSP、单文件下载、图片接口、本地磁盘 LRU 缓存。
> - SMTP 发信（go-mail）与模板、摘要合并、每日额度保护、`email_log`、管理员告警、Healthchecks 心跳；Message-ID 使用发件域名。
> - 命令：`admin invite / disable / enable / logout-all`；`mailtest`（只登录 SMTP 验证配置，加 `-to` 发示例邮件）。
> - 验收集成测试 `internal/app/flow_test.go` 在真实 HTTP、River 和 Postgres 上走完：验证码登录（含 60 秒重发限制与每天 2 次上限）→ 提交 → 生成快照 → 查看 → 下载 → 删除确认 → 收到删除邮件 → 权限隔离 → 退出与删除账号。各包另有单元与集成测试；任务错误到 River 结果的映射（推迟、取消、重试）有单独测试。
> - 编译出的程序用 curl 冒烟测试通过。
> - 上线地址（2026-11-20 确定）：网站 `BASE_URL=https://ob.portkey.click`，快照域 `SNAP_BASE_URL=https://snap.ob.portkey.click`，发件人 `MAIL_FROM=折光观察者 <noreply@ob.portkey.click>`。
> - Resend（免费档，东京区域）：发件域名 `ob.portkey.click`。DNS 在 Namecheap：DKIM `resend._domainkey.ob`；SPF 用 CNAME `rsend.ob` / `send.ob`，另在 `ob` 上加 `v=spf1 include:rsend.ob.portkey.click include:send.ob.portkey.click ~all`；DMARC `_dmarc.ob`（`p=none`）。根域名的 Namecheap 邮件转发 MX 保持不变，Mail Settings 不要改成 Custom MX。
> - **送达实测**：Gmail 与 QQ 邮箱均进收件箱，显示正常；Gmail 显示 SPF、DKIM、DMARC 均为 PASS。QQ 邮箱起初提示"发件人无法验证"，原因是发件人域名 `ob.portkey.click` 本身没有 SPF 记录（QQ 不认可子域名上通过的 SPF），加上述 `ob` 的 SPF 后提示消失。QQ 仍会显示"由 …@rsend.ob.portkey.click 代发"，这是退信地址在子域名上的正常现象。163 邮箱没有测试（没有账号）。
> - 凭据只放在不进 git 的 env 文件里（开发机为 `.env.smtp`），上线时放进服务器的 `.env`。

- 验证码登录、会话、限流、配额、注册模式。
- 观察相关 API、快照签名链接、快照域服务、单文件 HTML 下载。
- 邮件：SMTP Mailer、模板、摘要合并、日限额 Snooze、`email_log`。
- **验收**：通过 API 完成 登录 → 提交 → 查看快照 → 模拟删除 → 收到邮件 的完整流程；集成测试覆盖该流程。

### M4：Web 前端与打印样式
> **进行中（2026-11-20）**：代码已完成，待真机验收。
> - §10 全部页面；OpenAPI 类型生成与 CI 中的同步检查（`make openapi-check`）；前端嵌入二进制（`internal/webui`）与主域 CSP。
> - 新增 `GET /api/v1/site`、`observer openapi`。
> - 删除账号时从验证码与发信记录中抹去邮箱，发信记录保留 90 天（为隐私说明的承诺补上实现）。
> - **修复**：快照页的沙箱缺少 `allow-same-origin`，导致在线查看时图片全部被浏览器拦截（见 §12）。
> - 测试：Vitest 单元测试与登录页组件测试（42 个）；Go 端 `webui` 路由、缓存头、CSP 与真实嵌入的测试；`web/e2e/smoke.mjs` 用真实 Chrome 以手机尺寸走完 登录 → 提交真实文章 → 等待备份 → iframe 内图片全部加载 → 下载 → 打印 PDF → 各页面 → 微信内置浏览器提示 → 退出，并检查控制台错误、CSP 违规与 5xx。在全新数据库上实测：提交后 9 秒可看到备份，7/7 图片加载，PDF 13 页、图片完整、页脚含原文链接与备份时间。
>
> **待办**：
> - 真机验收（需要用户）：iOS Safari、安卓 Chrome、iOS / 安卓微信内置浏览器各走一遍；iOS 与安卓上实际打印 / 存为 PDF。
> - 隐私说明与服务条款的文字请用户审阅（联系方式目前是 GitHub Issues）。

- 实现 §10 全部页面，OpenAPI 类型生成，移动端适配。
- 阅读版 HTML 的打印样式与"保存为 PDF"操作引导。
- **验收**：在手机浏览器与微信内置浏览器中完成全部操作；在桌面 Chrome / Safari、iOS Safari、安卓 Chrome 上打印出的 PDF 排版可用（不截断图片、分页合理）。

### M5：上线
> **已部署（2026-11-20）**：`https://ob.portkey.click` 上线（邀请制），以下为过程记录。
> - `Dockerfile`（多阶段：前端 → Go → alpine + PostgreSQL 18 客户端，约 40 MB，非 root）、`deploy/compose.yaml`（内存上限、日志轮转、健康检查）、`Caddyfile`（自动 HTTPS、无访问日志）、`.env.example`、`update.sh`、`release.yml`、`docs/runbook.md`。
> - 每日数据库备份与恢复命令（§13.4）；`observer admin session` 为冒烟测试签发会话；`web/e2e/smoke.mjs` 支持用会话令牌登录任意部署。
> - 去掉 Huma 在响应里加的 `$schema` 链接（在 TLS 代理后会写成 http://）。
> - 在开发机上用 `compose.local.yaml` 按生产配置跑通：HTTPS/HTTP3、HSTS、CSP、主站与快照域分流、Caddy 与 app 日志中都没有访问者 IP；浏览器冒烟测试全部通过；容器内备份与恢复演练通过。
>
> - VPS：Docker 29.10、Compose v5.8；journald 上限 100 MB；`/opt/observer`（`.env` 由服务器上已有的 Wasabi 凭据、Resend 配置与新生成的数据库密码、签名密钥组装，600）；金丝雀为 M0 用过的存活文章 `q-WkvTApjcwqtgk9LY7Q-A` 与违规删除文章 `hAeOjjWPixvnkiQ58-ohRg`。
> - 首个版本 `34dbd93` 由开发机构建、`docker save | ssh docker load` 传入（还没有发布过 `v*` 标签）。
> - Let's Encrypt 证书两个域名一次签发成功；外部检查 HTTP/2、HTTP/3、HSTS、CSP、HTTP→HTTPS 308 均正常。
> - 生产冒烟测试（专用测试账号，结束后经 `DELETE /me` 删除）：从香港抓取、11 秒后可看到备份，7/7 图片经快照域从 Wasabi 加载，下载与打印 PDF 正常，无控制台错误。
> - 生产恢复演练：备份 → 恢复到 `restore_drill` → 各表计数与迁移版本一致 → 删除演练库。
> - 冒烟测试中发现 Caddy 的警告日志带有访问者 IP，已加日志过滤器并验证（§12）。
> - 资源（上线后）：app 41 MB、postgres 85 MB、caddy 28 MB；整机已用 611 MB，swap 31 MB；磁盘已用 5.7 GB。
>
> **待办**：
> - 打第一个版本标签（如 `v0.1.0`），走通 Release workflow；GHCR 包设为公开后，服务器改用 `update.sh v0.1.0`。
> - 可选：自动部署密钥（runbook）、Healthchecks.io（`HEALTHCHECK_URL`）。
> - 真机验收（M4 遗留）：iOS Safari、安卓 Chrome、iOS / 安卓微信内置浏览器，各打印一次 PDF。
> - 邀请制运行两周，观察内存、拦截率、邮件用量，再决定是否开放注册。
>
> **收尾小项（2026-11-20）**：Dependabot；`/auth/session` 消除访客页面的 401 控制台报错；手机上观察详情的预览默认收起（避免页面内嵌套滚动），保留"在新窗口阅读"；查明"共 8 张图片、页面 7 个 `<img>`"不是问题（另 1 张是样式里的背景图，均被引用）。`legacy-python` 标签已在远端。
>
> **上线后（2026-11-20）**：用户登录验证、简单检查 PDF 通过；隐私说明与服务条款文字已审阅；`v0.1.0` 由 Release workflow 发布到 GHCR（公开），服务器已用 `update.sh v0.1.0` 切换。自动部署密钥等项目更成熟后再配置。新增每账号每月通知邮件上限（§5.6，迁移 00003）。
>
> **补充（2026-11-22，未发布）**：公共邀请链接（§6.1，迁移 00013）：管理员设定名额和有效期，`/join` 页面注册，后台「邀请链接」页与 `observer admin invite-link`。
>
> **v0.2.0（2026-11-20）**：管理后台、只在删除时发邮件、用户邀请（每月 5 个）；升级前先做了数据库备份，迁移 00003～00006 执行正常，Caddy 升到 2.11；用户账号设为管理员。Healthchecks.io 已接入（周期 5 分钟、宽限 10 分钟，邮件告警）。
>
> **仍待办**：图片直连 Wasabi（§8.1，已决定，稍后实施）；真机验收（M4 遗留）；邀请制观察到 2026-12-04 左右再决定是否开放注册；侵权投诉、账号封禁样本（决定是否改用 `head` 检查；接投诉后判违规的样本已于 2026-11-21 补上）；自动部署密钥（可选）。

- 生产 Compose、Caddy、swap、Postgres 调参、`.env` 模板。
- tag 触发部署、数据库每日备份、恢复演练、Healthchecks、告警。
- README、`docs/runbook.md`（隐私说明与服务条款页已在 M4 完成）。
- Caddy 访问日志不记录客户端 IP（见 §12 隐私）；`TRUST_PROXY_HEADERS=true`。
- 以邀请制软启动，观察两周后再决定是否开放注册。
- **验收**：生产环境运行两周，无未告警的停摆；完成一次恢复演练。

### M6：订阅号被动回复入口
> **代码完成（2026-11-21）**，未部署。§6.7 全部内容；迁移 00009；`observer mpsim` 在本地模拟微信发消息（签名、XML 与真实回调相同）。
> - 测试：`internal/mp` 单元测试（签名、XML、回复文案与字节上限、去重、限流）；`internal/auth` 绑定、换绑、并发绑定、免登录、取消关注与删除账号的清理；`internal/app/wechat_test.go` 在真实 HTTP、River 和 Postgres 上走完 关注 → 未绑定发链接 → 邮箱登录 → 绑定并自动观察 → 再发同一链接 → 免登录链接 → 删除邮件发到绑定邮箱 → 取消关注 → 网页上解绑；前端 Vitest 覆盖两个链接页面与设置卡片。
> - 本机用 `mpsim` 和真实 Chrome（手机尺寸加微信 UA、电脑尺寸）走完绑定、免登录跳转、设置页解绑，无前端报错。
>
> **上线（v0.4.1，2026-11-21）**：在微信开发者平台配置消息推送后真机走通 关注 → 绑定 → 发链接 → 免登录链接；未备案域名会弹警告但可以继续访问。
>
> **补充（2026-11-21，未发布）**：回复与页面写明公众号和网站的关系、带链接时说明备案警告；邀请制下的提示；发链接后等首次抓取再回复；删除提醒随下一条回复送达（迁移 00011）；首页与设置页的公众号二维码。
>
> **待办**：
> - 隐私说明新增条目的文字请用户审阅。
> - 读者的普通留言目前也会收到用法说明。是否改成只响应链接和指令，取决于开启服务器配置后后台「消息管理」里是否还能看到留言，待用户确认。

### 后续阶段（不在本次范围内）
- 内容变更检测（`content_hash` 变化时追加快照，用于发现"被修改/删减"的文章）。
- 快照分享链接、Passkey 登录（观察期续期已于 2026-11-20 实现，见 §6.5）。
- 服务端直出 PDF（纯 Go 实现，不引入浏览器），视用户需求决定。
- 本地抓取节点 `observer agent`（M0 显示香港 IP 未被拦截，暂不需要；上线后如熔断频繁再启用）。
- ~~订阅号被动回复入口~~（M6）；个人小程序入口（一次性订阅消息；需先确认 ICP 备案与个人主体类目限制）。
- 用户侧抓取（书签脚本或浏览器扩展）。

---

## 17. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 香港数据中心 IP 被微信频繁拦截 | 核心功能不可用 | M0 实测 3 秒一次、连续 100 次未被拦截，但微信策略可能变化；降频与熔断；金丝雀告警；本地抓取节点（住宅 IP）；用户侧抓取；最后才考虑付费代理 |
| 微信页面结构变化 | 漏报 / 误报 | 四态判定，不默认判为存在；样本测试；金丝雀检测与告警 |
| 1 GB 内存不足 | OOM、服务中断 | 服务器不运行浏览器；容器内存上限；流式处理大文件；swap |
| 用户不会用浏览器打印 PDF | PDF 体验差 | 页面内操作引导；单文件 HTML 始终可用；需求明确后再做服务端 Go 直出 PDF |
| Resend 每日 100 封限额 | 通知延迟 | 摘要合并；优先级；Snooze 到次日；80% 用量告警；SMTP 抽象层便于更换服务商 |
| 通知邮件被 QQ/163 判为垃圾邮件 | 用户收不到通知 | 已配置 SPF/DKIM/DMARC，Gmail、QQ 实测进收件箱；纯文本风格、无营销用语；引导用户添加白名单；Web 端可随时查看状态 |
| 发件域名在 `.click` 顶级域名下 | `.click` 在反垃圾统计中属于滥用较多的顶级域名，部分邮箱可能更严格 | 目前实测正常；上线后关注 `email_log` 与用户反馈，进垃圾箱比例偏高时换用 `.com` 等域名专门发信（只需改 `MAIL_FROM` 与 DNS） |
| 验证码每天 2 次的限制 | 用户输错或没收到时当天无法登录 | 会话 60 天滑动过期，平时很少需要重新登录；登录页显示剩余次数与重发倒计时；管理员可用 SQL 清除 `login_requests` 中的计数 |
| 微信内置浏览器拦截未备案域名 | 公众号回复里的链接打不开，无法绑定 | 上线后真机验证；后备方案为网页端绑定码（§6.7） |
| 大陆访问香港站点慢或不稳定 | 体验差 | 前端资源体积小、长缓存；图片走本地缓存；必要时评估 CDN |
| 滥用（邮件轰炸、批量提交） | 额度耗尽、资源耗尽 | 限流、配额、邀请制、PoW 验证（按需） |
| 单机故障 | 数据丢失 | 每日数据库备份到 Wasabi，文件本身已在 Wasabi；恢复手册与演练 |
| 版权或法律投诉 | 运营风险 | 快照默认私有；下架流程；服务条款 |

---

## 18. 已采用的默认决定（可调整）

| 事项 | 默认值 |
|---|---|
| 观察期 | 60 天 |
| 观察期满是否发邮件 | 不发；只在文章被删除时发邮件 |
| 续期 | 每次从当天起再观察 60 天，不限次数；不发到期提醒 |
| 注册模式 | 邀请制软启动；用户每月可邀请 5 人，管理员不限 |
| 每用户活跃观察上限 / 每日提交上限 | 50 / 30 |
| 每账号每月通知邮件 | 20 封（可按账号调整，预留付费提高上限） |
| 快照可见性 | 仅对观察过该文章的用户可见 |
| 域名规划 | `<domain>` 主站，`snap.<domain>` 快照 |
| VPS 架构 | amd64（M0 已确认，Ubuntu，内核 6.8） |
| Wasabi region | `ap-southeast-1`（新加坡，M0 实测延迟最低） |
| 定期检查方式 | 先用 `check`；`head` 待 M2 补齐删除样本后再决定是否启用 |
| 许可证 | 保持 GPL-3.0 |
| 旧代码 | 仅存档于 tag `legacy-python`，主干不保留 |

---

## 附录 A：旧实现中值得保留的经验

（参考 `git show legacy-python:src/article_checker/article_checker.py`）

- 删除页特征：带有 `weui-icon-warn` / `weui-icon-warn-gray` 图标，提示文字位于 `h2.weui-msg__title` / `h3.weui-msg__title` / `div.weui-msg__title.warn`。**需在 M0 中用新样本复核**。
- 正文位于 `div#js_content`，图片真实地址在 `data-src` 属性。
- 嵌套 `section` 会导致正文文本重复提取（旧 issue #85），新实现直接保留 DOM 结构做清洗，不再逐段抽取文本，从根本上避免该问题。
- 下载图片需要带 `Referer`；同一会话内保持 UA 一致。
- 被动回复的消息长度限制为 2048 字节（与未来订阅号入口相关）。

## 附录 B：M0 实测报告

### B.1 本机实测（大陆家庭宽带，2026-11-19）

工具：`observer probe` / `observer netcheck`（`internal/wechat`、`internal/probe`、`internal/netcheck`）。

**页面类型与识别特征**

| 页面 | 大小（解码后 / 传输） | 识别特征 | 判定 |
|---|---|---|---|
| 正常文章 | ~4 MB / ~880 KB | `#js_content` 有内容；头部 ~18 KB 处有 `og:type=article`、`og:title`、`og:url`；`window.PAGE_MID="mmbizwap:appmsg/newindex.html"` 在 ~0.7 MB 处 | alive |
| 作者删除 | ~32 KB / ~16 KB | `.weui-msg__title` = "该内容已被发布者删除" | deleted / author_deleted |
| 违规 | ~32 KB / ~16 KB | 标题 "此内容因违规无法查看"，说明 "接相关投诉，此内容违反《互联网用户公众账号信息服务管理规定》" | deleted / violation（按标题分类，说明里的"投诉"二字不影响） |
| 验证页 | ~17 KB / ~13 KB | 跳转到 `/mp/wappoc_appmsgcaptcha?poc_token=...`，`PAGE_MID='mmbizwap:secitptpage/verify.html'` | blocked |

**关键发现**

1. **长链缺 `chksm` 必定触发验证页**，带上任意 `chksm`（包括占位值 `00000000`）就能正常访问。旧实现的 `_trim_URL` 恰好把 `chksm` 删掉了。处理方式：请求时保留 `chksm`；用户给的长链没有 `chksm` 时，服务直接带上占位值请求（`Link.ServiceFetchURL`），避免每次先撞一次验证页、拉低 IP 信誉；首次抓取后改用页面中的 `msg_link`。
2. **`PAGE_MID` 赋值语句出现得很晚**（~0.7 MB 处，页面前部那次只是 `typeof` 判断）。所以页面头部判断只能依靠 Open Graph 标签，删除页和验证页都没有这些标签。
3. **提前停止读取有效**：HTTP/2 接收窗口设为 16 KB 时，`check` 方式约 210 KB，`head` 方式约 26 KB；窗口设为 128 KB 时，两者分别为 242 KB 和 58 KB（服务端会多推送数据）。默认窗口取 16 KB。
4. **图片**（`mmbiz.qpic.cn`）不带 Referer 也能下载（本机网络）。实现上仍然带上 Referer。
5. 三种 UA 预设（ios / android / desktop）都能正常拿到页面。
6. Resend SMTP 的 25、587、465 端口在本机都能连通。Wasabi `ap-east-1` 不存在（DNS 解析失败），东京 `ap-northeast-1` 的延迟最低。

测试样本（已精简，去掉脚本和样式，只保留关键变量）：`internal/wechat/testdata/`。

### B.2 香港 VPS 实测（2026-11-19 12:28–12:35 UTC）

环境：amd64，2 核，可见内存 913 MB（已用 494 MB，无 swap），Ubuntu，内核 6.8。脚本 `scripts/m0/run.sh`，未设置 `RATE_FAST`，未提供 S3 凭据。

**抓取与检测**

| 样本 | check（传输） | full（传输 / 解码） | head（传输） | 判定 |
|---|---|---|---|---|
| 正常文章 ×2 | 199–215 KB | 835–881 KB / 3.6–3.9 MB | 25.6 KB | alive（`content_node`；head 为 `og_article_head`） |
| 作者删除 | 16.3 KB | 8.6 KB | 16.3 KB | deleted / author_deleted |
| 违规 | 16.6 KB | 8.9 KB | 16.6 KB | deleted / violation |
| 长链缺 `chksm` | 12.8 KB | 5.0 KB | 12.8 KB | blocked（`wappoc_appmsgcaptcha`） |
| 长链带占位 `chksm=00000000` | 211 KB | 881 KB | 25.6 KB | alive，`msg_link` 返回真实 `chksm` |

- 判定结果与本机完全一致，所有样本都判定正确，没有 `error`。
- **持续频率测试**：同一篇文章每 3 秒检查一次、连续 100 次，**100 次全部 alive，没有出现验证页**，平均 212 KB/次，耗时 0.7–1.2 秒。
- 三种 UA 预设都正常。
- 图片（`mmbiz.qpic.cn`）带与不带 Referer 都返回 200，内容一致。
- 删除页和验证页中**没有任何 `og:type` 标签**，与 `head` 方式的前提一致（仍缺投诉、封号样本）。
- 样本的 `pass_ticket`、`appmsg_token` 等已脱敏。
- **2026-11-20 补充**：从线上服务器（v0.2.0）用 `observer probe` 复核了另一篇作者删除的文章，check / full / head 三种方式都判为 deleted / author_deleted（规则 `message_deleted`），传输量与上表完全相同（16.3 / 8.6 / 16.3 KB），说明作者删除页是不含文章信息的通用页面：没有标题，也没有 Open Graph 标签。这份完整页面没有精简（32 KB，令牌已脱敏），作为 `deleted_by_author_full.html` 加入样本测试。
- **2026-11-21 补充**：用户提供了一篇被投诉后删除的文章，在开发机（大陆家庭宽带）上 probe，check / full / head 都判为 deleted / violation（规则 `message_deleted`）。页面标题是"此内容因违规无法查看"，说明是"接相关投诉，此内容违反《互联网用户公众账号信息服务管理规定》"，与已有的违规样本结构相同：微信把"接投诉后认定违规"显示为违规页，按标题归为 `violation` 符合预期（合成测试"violation title wins over complaint desc"覆盖的正是这种情况）。完整页面（32 KB，令牌已脱敏）作为 `deleted_violation_full.html` 加入样本测试。仍缺"被投诉且经审核涉嫌侵权"（`complaint`）与账号封禁 / 迁移的真实样本。

**网络**

| 目标 | 连接 | TLS | 首字节 | 备注 |
|---|---|---|---|---|
| mp.weixin.qq.com | 3 ms | 7 ms | 191 ms | 走香港本地节点 |
| mmbiz.qpic.cn | 2 ms | 10 ms | 46 ms | |
| Wasabi ap-southeast-1（新加坡） | 34 ms | 70 ms | 416 ms | **最低** |
| Wasabi ap-northeast-1 / -2（东京 / 大阪） | 55 / 52 ms | 112 / 110 ms | 439 / 436 ms | |
| Wasabi 美国、欧洲各区 | 150–250 ms | 300–500 ms | 800–1200 ms | |
| Wasabi ap-east-1 | — | — | — | 不存在 |
| smtp.resend.com 25 / 587 / 465 | ~240 ms | 465 端口 229 ms | — | 三个端口都能拿到 banner，VPS 服务商未封锁 |

### B.3 M0 结论

对 §11.1 六个问题的回答：

1. **纯 HTTP 抓取在香港 IP 上可用**：按生产峰值频率（3 秒一次）连续 100 次未触发验证页。验证页目前只由"长链缺 `chksm`"触发，这一点已在链接规范化中处理。更高频率（1 秒一次）没有测试，生产环境也不会用到。
2. **拦截类型**：未遇到按 IP 或频率的拦截，不需要本机 Chromium 对比。第一阶段**不需要本地抓取节点**，保留为预案（§11.3）。
3. **页面特征**：已覆盖正常、作者删除、违规（含接投诉后判违规，2026-11-21）、验证页、短链、长链。**仍缺侵权投诉（`complaint`）、账号封禁 / 迁移样本**，规则中已按关键词预留分类，并有合成样本测试；拿到真实样本后补测试。
4. **元数据提取**：`biz/mid/idx/sn`、标题、公众号名与 ID、作者、发布时间、`msg_link` 都能从完整页面或 `check` 读取的部分中提取。`head` 方式只能拿到标题和 `og:url`，所以首次抓取必须用 `full`。
5. **图片**：香港 IP 下不带 Referer 也能下载；实现上仍带 Referer 并保持会话内 UA 一致。图片格式按响应的 `Content-Type` 保存（样本中为 jpeg / png），`wx_fmt` 和 webp 的处理留到 M2 实现快照时确认。
6. **Wasabi**：选新加坡 `ap-southeast-1`，bucket 与密钥已就绪，吞吐量与延迟见 §8。

其他决定：
- 定期检查**先全部用 `check`**。M2 补齐删除样本后，再决定是否把日常检查切换到 `head`。按当前检查间隔估算的流量见 §5.5。
- 邮件使用 Resend 的 587 端口（STARTTLS），465 作为备用。
- **内存**：VPS 部署前已占用约一半内存且无 swap，M5 需要先处理（§13.1）。
