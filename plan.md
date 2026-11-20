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

- 公众号入口、小程序入口、微信消息推送（无企业主体，见 §2）。
- 被审查文章的全量备份（定位仍是个人关注文章的备份）。
- 管理后台 Web 界面（第一阶段用 CLI 子命令完成管理操作）。
- 多机部署 / 高可用。

---

## 2. 约束

| 类别 | 约束 | 对设计的影响 |
|---|---|---|
| 主体 | 无企业主体 | 无法使用服务号模板消息/订阅通知、开放平台 UnionID、微信扫码登录 → 通知只用邮件，登录用邮箱 |
| 部署地 | 香港 VPS | 无需 ICP 备案；需实测香港数据中心 IP 抓取微信文章是否频繁触发验证 |
| VPS | 2 核 / **1 GB 内存** / 30 GB 磁盘 / 500 GB 月流量 | 服务器只运行后端服务，**不运行浏览器**；Postgres 调小；本地缓存设上限 |
| 已有资源 | Wasabi 对象存储套餐、域名 | 备份文件全部存 Wasabi；注意 90 天最短存储期、出站 ≤ 存储量的公平使用条款 |
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
- **删除需确认**：首次判定为 `deleted` 后，10 分钟后再检查一次，两次一致才进入 `deleted`。
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
| < 24 小时 | 1 小时 |
| 1–7 天 | 3 小时 |
| 7 天以上 | 8 小时 |

- 每次在间隔上加 ±20% 随机抖动，避免集中请求。
- 全局抓取限速：同一时刻最多 1 个抓取请求，两次请求最少间隔 3 秒（可配置）。
- 遇到 `blocked`：指数退避（5 分钟起，上限 2 小时）；短时间内多篇文章都被拦截时触发**全局熔断**，暂停所有抓取 30 分钟。
- 文章没有活跃观察时进入 `dormant`，停止检查。
- 一个周期任务（每分钟）领取 `next_check_at <= now()` 的文章并入队检查任务；River 的唯一任务机制保证同一文章不会重复排队。

**容量估算**：串行 3 秒一次约 1200 次/小时；按平均 6 小时检查一次，可支撑约 7000 篇同时观察中的文章，远超第一阶段需要。

**流量估算**（按 M0 实测数据修正，见附录 B）：正文页解码后约 4 MB，gzip 后约 880 KB。定期检查不下载整页，有两种读取方式：

| 检查方式 | 读到哪里为止 | 实测每次流量 | 60 天 × 230 次 |
|---|---|---|---|
| `check`：确认正文节点存在 | `#js_content` 之后 64 KB | ~210 KB | ~48 MB |
| `head`：只读页面头部（实验性） | Open Graph 标签 | ~26 KB | ~6 MB |
| 已删除、验证页 | 页面本身很小 | ~16 KB | — |

另外，首次快照（整页加图片下载，再上传 Wasabi）约 10 MB/篇。

- 全部使用 `check`：约 58 MB/篇，500 GB 月流量（同机代理每月约 20 GB，再预留约 200 GB 给用户查看）大约能支撑每月 5000 篇新文章。
- 可选方案：**`head` 日常检查，每天一次 `check` 复核**：约 29 MB/篇，可支撑每月 1 万篇以上。
- **第一阶段先全部用 `check`**。M0 中删除页和验证页都没有 Open Graph 标签，但样本还不全；M2 补齐投诉、封号等删除样本后，再决定是否切换到 `head`。

### 5.6 通知策略

- **文章被删除**：周期任务每 5 分钟扫描一次"已结束但未通知"的观察，按用户合并成一封摘要邮件（同一用户这段时间内的多篇删除只发一封）。用扫描而不是每次删除单独入队，任务失败或并发时都不会漏发。
- **观察期满**：默认不发邮件（用户可在设置中打开）。
- 邮件只包含标题、状态、删除原因和**查看链接**，不带附件。
- **每日发送上限保护**：Resend 免费档每日 100 封。通知类邮件只用到额度的 90%，剩下 10% 留给登录邮件。发送遇到限额（本地计数或服务商返回）时，摘要任务顺延到次日 UTC 零点之后。当日发送量达到 80% 时给管理员发告警。
- **登录邮件同步发送，不进任务队列**：任务参数以明文存在 `river_job` 表里，验证码不能明文落库。额度用完时，登录接口直接返回 503"今天的邮件发送额度已用完"。
- **管理员告警**（`ADMIN_EMAILS`）：金丝雀失败、熔断打开、单篇文章连续 6 次检查无结论、邮件用量达到 80%。同类告警 6 小时内只发一次，并且都会写入错误日志。

---

## 6. 关键流程

### 6.1 登录（邮箱验证码）

1. 用户在登录页输入邮箱 → `POST /api/v1/auth/code`，服务端发送 6 位数字验证码（标题里也带验证码，手机通知栏可直接看到）。
2. 验证码只存哈希，10 分钟有效，一次性使用；每个验证码最多尝试 5 次，只有最近一次发送的验证码有效。
3. 用户在**同一页面**输入验证码 → `POST /api/v1/auth/verify {email, code}`。
4. 验证成功：首次登录则创建用户（开放模式）；签发会话 token（256 位随机数，哈希存库，30 天滑动过期），写入 `__Host-session` Cookie（`Secure; HttpOnly; SameSite=Lax`）。
5. 发送限制：
   - **每个邮箱每天最多 2 封**（按北京时间自然日），两次之间至少隔 60 秒。用完后接口返回 429"这个邮箱今天已获取过 2 次验证码，请明天再试"。
   - 同一 IP 每小时最多 10 次。
   - 计数对所有邮箱一视同仁（无论是否有账号、是否被邀请、是否停用），响应也相同，不泄露邮箱是否注册。发信失败时退还这次计数。
   - 接口返回当天剩余次数和可重发的等待秒数，供前端显示倒计时。

邮箱的所有权由验证码本身验证，解决了旧系统"可绑定他人邮箱"的问题。

**为什么用验证码而不是魔法链接**（2026-11-20 决定）：
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
5. 配额：每用户最多 50 个活跃观察，每日最多提交 30 次（可配置）。

### 6.3 抓取与快照生成

1. 用 HTTP 客户端抓取页面（移动端 UA，每次任务内 UA 固定，自动 gzip，10 秒超时，**只允许请求白名单域名**，重定向后再次校验）。
2. 检测结果为 `blocked` 时按 §5.5 退避重试，不在服务器上换用浏览器。
3. 解析：标题、公众号名、作者、发布时间、正文节点 `#js_content`。
4. 下载图片：`data-src` 优先，带 `Referer: https://mp.weixin.qq.com/`，串行下载，单图 20 秒超时、上限 20 MB；计算 sha256，Wasabi 中已存在则跳过上传。
5. 清洗正文：移除脚本、事件属性、追踪像素；视频/音频 iframe 替换为占位卡片（附原链接）；图片地址改写为 `snap.<domain>/a/<sha256>`。
6. 生成阅读版 HTML（内置简洁样式模板，含标题、公众号、发布时间、原文链接、备份时间），上传到 Wasabi。
7. 同时上传原始 HTML（gzip），以便日后解析逻辑改进时重新处理。
8. 部分图片下载失败时快照仍然生效，记录 `missing_assets`，后续检查时补抓缺失图片。

### 6.4 查看与下载

- **在线查看**：前端调用 `POST /api/v1/snapshots/{id}/view-url` 获取签名链接（HMAC，1 小时有效），以 `<iframe sandbox>` 嵌入 `snap.<domain>/s/<id>?sig=...`。
- **下载单文件 HTML**：快照服务在请求时把图片内联为 data URI，流式输出，不缓存成品。
- **保存为 PDF**：阅读版 HTML 内置打印样式（分页、图片适配纸宽、隐藏无关元素、页脚标注原文链接与备份时间）。用户点击"打印 / 保存为 PDF"后，前端在新标签页打开快照，并提示使用浏览器的打印功能（iOS、安卓需从系统浏览器打开，微信内置浏览器不支持打印）。服务器不生成 PDF。
- 资源 `/a/<sha256>` 内容不可变，设置长期缓存头。读取顺序为 本地磁盘缓存 → Wasabi，回源后写入缓存。

### 6.5 观察到期与收尾

周期任务（每 10 分钟）将超过 `expires_at` 的观察标记为 `ended(expired)`，按用户设置决定是否通知；文章不再有活跃观察时转为 `dormant`。

### 6.6 金丝雀检测

周期任务（每 6 小时）检查一组配置好的金丝雀链接：一篇长期存在的文章和一篇已确认删除的文章。结果与预期不符时立即告警。这样微信页面结构变化会在第一时间暴露，不会等到用户反馈。

---

## 7. 数据库设计（草案）

```sql
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  email            citext NOT NULL UNIQUE,
  role             text NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  notify_on_expire boolean NOT NULL DEFAULT false,
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
  kind        text NOT NULL CHECK (kind IN ('login','deleted','expired','alert')),
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

---

## 8. 对象存储设计（Wasabi）

```
assets/<sha256 前 2 位>/<sha256>          图片（不可变，内容寻址）
snapshots/<snapshot_id>/page.html        阅读版 HTML（图片引用为 /a/<sha256>）
snapshots/<snapshot_id>/raw.html.gz      原始页面
backups/postgres/<YYYY-MM-DD>.sql.gz                 数据库备份
```

- Bucket 私有，只由 app 访问，用户永远不直连 Wasabi（大陆访问速度不可控，并且可以把 Wasabi 出站量控制在存储量以内）。
- **90 天最短存储期**：除数据库备份外，所有对象都是长期保留，不受影响；数据库备份默认保留 120 天。**不要把 Wasabi 当临时存储用**，临时文件只放本地磁盘。
- 本地磁盘 LRU 缓存上限 5 GB（配置项），存放热点图片与快照 HTML。只在读取（快照域服务）时用到，所以与快照域一起在 M3 实现。
- 开发与测试用本地目录（`BLOB_DIR`）代替 S3，代码路径相同；S3 实现另有一个连真实 bucket 的测试（`TestS3`，需设置 `TEST_S3_*`），M2 已在 VPS 上对 Wasabi 跑通。
- Region 选 **新加坡 `ap-southeast-1`**（`s3.ap-southeast-1.wasabisys.com`）：M0 从香港 VPS 实测连接耗时 34 ms，是所有区域中最低的（东京 55 ms，Wasabi 没有香港区域）。Bucket 的区域在创建时确定，Wasabi 套餐按账号计费，新建 bucket 不增加费用。
- 已创建 bucket `observer-backup-001`（2026-11-19）：私有、未开启版本控制与 Object Lock；程序使用专用子用户 `observer-app` 的密钥，策略只允许访问该 bucket（`netcheck -s3` 已验证列出全部 bucket 会被拒绝）。
- 从 VPS 实测：8 MB 对象上传 13–15 MB/s、下载 4–5 MB/s；64 KB 对象（接近文章图片大小）上传中位数 48–69 ms、下载 76–101 ms。一篇 55 张图的文章串行上传约 3–4 秒。

---

## 9. API 草案（`/api/v1`，JSON）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/code` | `{email}` → 202 `{remaining, resend_after}`；超限 429 |
| POST | `/auth/verify` | `{email, code}` → 设置会话 Cookie |
| POST | `/auth/logout` | 注销当前会话 |
| GET | `/me` | 当前用户信息与设置 |
| PATCH | `/me` | 修改设置（`notify_on_expire`） |
| DELETE | `/me` | 删除账号 |
| POST | `/watches` | `{url}` → 201（已备份）/ 202（解析中） |
| GET | `/watches` | 列表，支持 `status` 过滤，游标分页 |
| GET | `/watches/{id}` | 详情（含文章状态、最近检查记录、快照列表） |
| DELETE | `/watches/{id}` | 停止观察 |
| POST | `/snapshots/{id}/view-url` | 返回签名的在线查看链接 |
| POST | `/snapshots/{id}/download-url` | 返回单文件 HTML 的签名下载链接 |
| GET | `/openapi.json` | API 契约 |
| GET | `/healthz` | 健康检查（DB 可用、worker 心跳） |

**鉴权**：Web 使用会话 Cookie；未来小程序在 `Authorization: Bearer` 中携带同一种会话 token，共用 `sessions` 表。

**CSRF**：会话 Cookie 使用 `SameSite=Lax`；所有写操作要求 JSON Content-Type 并校验 `Origin`。

**错误格式**：RFC 9457 Problem Details（Huma 默认支持）。

快照域 `snap.<domain>`：`GET /s/{snapshot_id}?sig=...`、`GET /s/{snapshot_id}/single.html?sig=...`、`GET /a/{sha256}`。

---

## 10. Web 前端

| 页面 | 内容 |
|---|---|
| 首页 / 介绍 | 服务说明、使用方法、隐私说明入口 |
| 登录 | 输入邮箱 → 获取验证码（60 秒重发倒计时、当天剩余次数）→ 在同一页面输入验证码完成登录 |
| 我的观察 | 列表（状态徽章：解析中 / 观察中 / 已删除 / 已到期 / 已停止），顶部为提交链接输入框 |
| 观察详情 | 文章信息、删除原因、检查历史、快照在线查看（iframe）、下载单文件 HTML、打印 / 保存为 PDF（附操作提示）、停止观察 |
| 设置 | 到期通知开关、退出登录、删除账号 |
| 关于 / 隐私 / 服务条款 | 静态页 |

- 移动端优先：大部分用户会在微信内置浏览器或手机浏览器中使用。
- 构建产物通过 `go:embed` 嵌入二进制，由 app 直接提供；SPA 路由回退到 `index.html`。

---

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
  `Content-Security-Policy: sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; frame-ancestors https://<domain>`。
  此外，入库前已经过 bluemonday 清洗。
- **SSRF 防护**：抓取只允许白名单域名，页面只允许 `mp.weixin.qq.com`，资源只允许 `*.qpic.cn`、`*.qlogo.cn`、`res.wx.qq.com`（M0 样本中出现的全部资源域名）。拨号时拒绝内网、回环、链路本地、CGNAT 等非公网地址，重定向后再次校验（已在 `internal/wechat/fetch.go` 实现）。
- **签名链接**：HMAC-SHA256，密钥来自配置，带过期时间；签名绑定快照 ID。
- **会话安全**：token 只存哈希；Cookie 使用 `__Host-` 前缀；用户可以注销全部会话（`admin` 命令也支持）。
- **滥用防护**：登录、提交接口按 IP 与用户限流；用户配额；注册模式配置 `REGISTRATION_MODE=open|invite|closed`（默认 `invite`，即邀请制软启动，见 §16）；后续如有邮件轰炸，可加自托管工作量证明验证（如 ALTCHA），不依赖第三方验证码服务。
- **密钥管理**：所有密钥放在服务器 `.env`（权限 600），不进仓库；代码中不再硬编码任何邮箱、openid 或默认口令。
- **隐私**：只收集邮箱；提供删除账号功能；隐私说明页明确数据用途与保留策略。
- **版权与下架**：快照默认仅对观察过该文章的用户可见；提供联系邮箱处理下架请求。
- **HTTP 安全头**：主域设置 HSTS、`X-Content-Type-Options`、严格 CSP（Vue 构建产物不需要 `unsafe-eval`）。

---

## 13. 部署与运维

### 13.1 Compose 服务与内存预算（1 GB 内存）

| 服务 | 镜像 | 常态内存 | 容器上限 |
|---|---|---|---|
| caddy | `caddy:2-alpine` | ~30 MB | 64 MB |
| postgres | `postgres:18-alpine` | 150–250 MB | 320 MB |
| app | `distroless/static` + `observer` 二进制（另加 `pg_dump`，见 §13.4） | 40–100 MB | 256 MB |
| 系统 + Docker | — | ~150–200 MB | — |

- 常态总占用约 400–600 MB，1 GB 内存有余量。仍建议配置 1 GB swap，防止偶发峰值导致 OOM。
- **M0 实测提醒**：VPS 实际可见内存 913 MB，部署前已用 494 MB、可用约 420 MB，且没有 swap。
- **与 xray / x-ui 共存**（用户自用代理，2026-11-19 核实）：x-ui 约 84 MB、xray 约 25 MB；其余为系统服务，其中 journald 约 55 MB、fail2ban 约 39 MB、multipathd 约 27 MB、udisks2 约 10 MB、ModemManager 约 9 MB。
  - 停用 VPS 上用不到的 multipathd、udisks2、ModemManager，并限制 journald 日志大小，可省约 50–100 MB。
  - 配置 **2 GB swap**（`vm.swappiness=10`）。
  - 加上 dockerd/containerd（约 70 MB）后，上表各容器合计 220–380 MB，物理内存仍然偏紧。M5 上线后按实测调整：必要时把 Postgres 的 `shared_buffers` 降到 64–96 MB，容器上限降到 256 MB。

### 13.1.1 端口规划（与 xray / x-ui 共存）

| 端口 | 占用者 | 说明 |
|---|---|---|
| 22/tcp | sshd | 不变 |
| 80/tcp | **caddy** | ACME HTTP-01 与跳转 HTTPS（当前空闲） |
| 443/tcp | **caddy** | 主站与 `snap.` 子域。原来由 xray 占用，**部署前需把 xray 入站移到其他端口** |
| 443/udp | caddy（可选） | HTTP/3，当前空闲 |
| 52154/tcp、2096/tcp | x-ui | 面板与订阅服务，不变；建议面板只监听 127.0.0.1，通过 SSH 隧道访问 |
| 127.0.0.1:62789 | xray API | 仅本机，不冲突 |

app 与 Postgres 只在 Compose 内部网络通信，不映射到宿主机端口。
- app 设置 `GOMEMLIMIT=200MiB`；图片下载按流处理，不在内存中整体缓存大文件；单文件 HTML 也以流式输出。
- Postgres 参数：`shared_buffers=128MB`、`effective_cache_size=256MB`、`work_mem=4MB`、`maintenance_work_mem=32MB`、`max_connections=30`；pgx 连接池上限 10。

### 13.2 磁盘规划（30 GB）

系统约 5 GB、镜像约 2 GB、Postgres 数据 < 2 GB（第一阶段）、本地缓存上限 5 GB；Docker 日志设置 `max-size=10m, max-file=3`；部署后自动执行 `docker image prune`。

### 13.3 配置项（环境变量）

已实现（M1）：`DATABASE_URL`（必填，URL 格式）、`DB_MAX_CONNS`（默认 10）、`HTTP_ADDR`（默认 `:8080`）、`BASE_URL`、`LOG_LEVEL`（默认 `info`）、`LOG_FORMAT`（`json` / `text`，默认 `json`）。

已实现（M3）：`SNAP_BASE_URL`（必须与 `BASE_URL` 不同主机）、`SIGNING_KEY`（≥32 字符）、`TRUST_PROXY_HEADERS`（在 Caddy 后面时设为 true）、`SERVICE_NAME`、`REGISTRATION_MODE`（`open` / `invite`，默认 `invite`）、`MAX_OPEN_WATCHES`（50）、`MAX_DAILY_SUBMISSIONS`（30）、`SMTP_HOST`、`SMTP_PORT`（587；465 走隐式 TLS）、`SMTP_USER`、`SMTP_PASSWORD`、`MAIL_FROM`、`DAILY_EMAIL_LIMIT`（100）、`ADMIN_EMAILS`（逗号分隔）、`CACHE_DIR`、`CACHE_MAX_BYTES`（5 GiB）、`HEALTHCHECK_URL`。
`BASE_URL` 为 https 时视为生产环境：`SIGNING_KEY`、`SMTP_HOST` 必填，`SNAP_BASE_URL` 也必须是 https；开发环境缺少它们时，分别退化为随机签名密钥、只把邮件写进日志。

已实现（M2）：`S3_ENDPOINT`、`S3_REGION`、`S3_BUCKET`、`S3_ACCESS_KEY`、`S3_SECRET_KEY`（设置 `S3_BUCKET` 时使用 S3）、`BLOB_DIR`（开发用本地目录，两者至少设置一个）、`WATCH_DAYS`（默认 60）、`FETCH_MIN_INTERVAL`（默认 `3s`，最小 `1s`）、`FETCH_USER_AGENT`（预设名 `ios` / `android` / `desktop` 或完整 UA，默认 `ios`）、`CANARY_ALIVE_URL`、`CANARY_DELETED_URL`。



### 13.4 备份与恢复

- 周期任务每天执行 `pg_dump | gzip` 上传到 Wasabi `backups/postgres/`，保留 120 天。app 镜像中包含 `postgresql-client`。
- 快照、图片、PDF 本身就在 Wasabi 中。
- 编写恢复手册（`docs/runbook.md`），内容包括：新机器 → 安装 Docker → 拉取镜像 → 恢复数据库 → 启动。上线前至少演练一次。

### 13.5 监控告警

- `/healthz` 返回数据库连通性和 worker 最近一次心跳时间。
- 调度任务每轮完成后 ping Healthchecks.io；超时未 ping 时由 Healthchecks 发邮件告警。
- 应用内告警（发给 `ADMIN_EMAILS`，有频率限制）：金丝雀失败、连续被拦截/熔断、邮件日用量超过 80%。
- 日志：slog JSON 输出到 stdout；`observer admin stats` 输出统计信息。

### 13.6 CI/CD

- **PR / push**：golangci-lint → `go test -race ./...`（CI 使用 Postgres 18 service container）→ 前端 `vue-tsc` + 构建。
- **tag `v*`**：构建 amd64 镜像并推送 GHCR → 通过 SSH 在 VPS 上执行 `docker compose pull && docker compose up -d`；迁移在 app 启动时自动执行。
- Renovate 或 Dependabot 每月汇总依赖更新 PR。

---

## 14. 仓库结构

```
.
├── cmd/observer/            # main：serve / migrate / probe / admin
├── internal/
│   ├── api/                 # Huma handlers
│   ├── app/                 # serve 的组装：连接、迁移、worker、HTTP、优雅退出
│   ├── auth/
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
│   └── wechat/
│       └── testdata/        # 真实页面样本
├── web/                     # Vue 3 + Vite + TS
├── deploy/
│   ├── compose.yaml
│   ├── compose.dev.yaml
│   ├── Caddyfile
│   └── postgres.conf
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
| 集成 | store 查询、任务流程（提交 → 抓取 → 快照 → 删除 → 通知）、验证码登录与每日限制 | 真实 Postgres；blob 与 mailer 用内存实现；抓取用 `httptest` 回放样本 |
| 对象存储 | 用一个集成测试验证 S3 读写 | 本地 MinIO 容器（只在开发/CI 中使用） |
| 前端 | 类型检查；关键组件少量测试 | `vue-tsc`、Vitest |
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
> **进行中（2026-11-19）**：不依赖外部账号的部分已完成。
> - 邮箱验证码登录（每个邮箱每天 2 封、只存哈希、同步发信；2026-11-20 由魔法链接改为验证码，理由见 §6.1）、会话（Cookie 与 Bearer 两种，30 天滑动过期）、邀请制、按 IP 与按邮箱限流、配额。
> - 全部 `/api/v1` 接口（§9）；跨站写请求按 `Origin` / `Sec-Fetch-Site` 拒绝。
> - 快照域服务：签名链接、沙箱 CSP、单文件下载、图片接口、本地磁盘 LRU 缓存。
> - SMTP 发信（go-mail）与模板、摘要合并、每日额度保护、`email_log`、管理员告警、Healthchecks 心跳。
> - `admin invite / disable / enable / logout-all` 命令。
> - 验收集成测试 `internal/app/flow_test.go` 在真实 HTTP、River 和 Postgres 上走完：验证码登录 → 提交 → 生成快照 → 查看 → 下载 → 删除确认 → 收到删除邮件 → 权限隔离 → 退出与删除账号。
> - 编译出的程序用 curl 冒烟测试通过。
>
> - 上线地址（2026-11-20 确定）：网站 `BASE_URL=https://ob.portkey.click`，快照域 `SNAP_BASE_URL=https://snap.ob.portkey.click`，发件人 `MAIL_FROM=折光观察者 <noreply@ob.portkey.click>`。
> - Resend 已配置（2026-11-20）：发件域名 `ob.portkey.click`（东京区域），DKIM、SPF（CNAME `rsend.ob` / `send.ob`，以及 `ob` 上的 `v=spf1 include:rsend.ob.portkey.click include:send.ob.portkey.click ~all`，供只按发件人域名查 SPF 的 QQ 邮箱使用）、DMARC（`_dmarc.ob`，`p=none`）均已生效；根域名的 Namecheap 邮件转发 MX 保持不变。`observer mailtest -env-file .env.smtp` 验证 SMTP 登录通过（加 `-to` 可发送示例邮件）。
>
> **待办（需要用户提供）**：Resend 账号与 SMTP 凭据、发件域名的 SPF/DKIM/DMARC 记录，拿到后用真实邮箱验证送达率（QQ、163、Gmail）。

- 验证码登录、会话、限流、配额、注册模式。
- 观察相关 API、快照签名链接、快照域服务、单文件 HTML 下载。
- 邮件：SMTP Mailer、模板、摘要合并、日限额 Snooze、`email_log`。
- **验收**：通过 API 完成 登录 → 提交 → 查看快照 → 模拟删除 → 收到邮件 的完整流程；集成测试覆盖该流程。

### M4：Web 前端与打印样式
- 实现 §10 全部页面，OpenAPI 类型生成，移动端适配。
- 阅读版 HTML 的打印样式与"保存为 PDF"操作引导。
- **验收**：在手机浏览器与微信内置浏览器中完成全部操作；在桌面 Chrome / Safari、iOS Safari、安卓 Chrome 上打印出的 PDF 排版可用（不截断图片、分页合理）。

### M5：上线
- 生产 Compose、Caddy、swap、Postgres 调参、`.env` 模板。
- tag 触发部署、数据库每日备份、恢复演练、Healthchecks、告警。
- README、隐私说明、服务条款、`docs/runbook.md`。
- 以邀请制软启动，观察两周后再决定是否开放注册。
- **验收**：生产环境运行两周，无未告警的停摆；完成一次恢复演练。

### 后续阶段（不在本次范围内）
- 内容变更检测（`content_hash` 变化时追加快照，用于发现"被修改/删减"的文章）。
- 快照分享链接、观察期续期、Passkey 登录。
- 服务端直出 PDF（纯 Go 实现，不引入浏览器），视用户需求决定。
- 本地抓取节点 `observer agent`（M0 显示香港 IP 未被拦截，暂不需要；上线后如熔断频繁再启用）。
- 订阅号被动回复入口（提交链接、绑定账号）、个人小程序入口（一次性订阅消息）。
- 用户侧抓取（书签脚本或浏览器扩展）。
- 简易管理后台。

---

## 17. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 香港数据中心 IP 被微信频繁拦截 | 核心功能不可用 | M0 实测 3 秒一次、连续 100 次未被拦截，但微信策略可能变化；降频与熔断；金丝雀告警；本地抓取节点（住宅 IP）；用户侧抓取；最后才考虑付费代理 |
| 微信页面结构变化 | 漏报 / 误报 | 四态判定，不默认判为存在；样本测试；金丝雀检测与告警 |
| 1 GB 内存不足 | OOM、服务中断 | 服务器不运行浏览器；容器内存上限；流式处理大文件；swap |
| 用户不会用浏览器打印 PDF | PDF 体验差 | 页面内操作引导；单文件 HTML 始终可用；需求明确后再做服务端 Go 直出 PDF |
| Resend 每日 100 封限额 | 通知延迟 | 摘要合并；优先级；Snooze 到次日；80% 用量告警；SMTP 抽象层便于更换服务商 |
| 通知邮件被 QQ/163 判为垃圾邮件 | 用户收不到通知 | 配置 SPF/DKIM/DMARC；纯文本风格、无营销用语；引导用户添加白名单；Web 端可随时查看状态 |
| 大陆访问香港站点慢或不稳定 | 体验差 | 前端资源体积小、长缓存；图片走本地缓存；必要时评估 CDN |
| 滥用（邮件轰炸、批量提交） | 额度耗尽、资源耗尽 | 限流、配额、邀请制、PoW 验证（按需） |
| 单机故障 | 数据丢失 | 每日数据库备份到 Wasabi，文件本身已在 Wasabi；恢复手册与演练 |
| 版权或法律投诉 | 运营风险 | 快照默认私有；下架流程；服务条款 |

---

## 18. 已采用的默认决定（可调整）

| 事项 | 默认值 |
|---|---|
| 观察期 | 60 天 |
| 观察期满是否发邮件 | 默认不发，用户可开启 |
| 注册模式 | 邀请制软启动 |
| 每用户活跃观察上限 / 每日提交上限 | 50 / 30 |
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
3. **页面特征**：已覆盖正常、作者删除、违规、验证页、短链、长链。**仍缺投诉删除、账号封禁 / 迁移样本**，规则中已按关键词预留分类，并有合成样本测试；拿到真实样本后补测试。
4. **元数据提取**：`biz/mid/idx/sn`、标题、公众号名与 ID、作者、发布时间、`msg_link` 都能从完整页面或 `check` 读取的部分中提取。`head` 方式只能拿到标题和 `og:url`，所以首次抓取必须用 `full`。
5. **图片**：香港 IP 下不带 Referer 也能下载；实现上仍带 Referer 并保持会话内 UA 一致。图片格式按响应的 `Content-Type` 保存（样本中为 jpeg / png），`wx_fmt` 和 webp 的处理留到 M2 实现快照时确认。
6. **Wasabi**：选新加坡 `ap-southeast-1`，bucket 与密钥已就绪，吞吐量与延迟见 §8。

其他决定：
- 定期检查**先全部用 `check`**（约 58 MB/篇，月流量够每月约 5000 篇新文章）。M2 补齐删除样本后，再决定是否把日常检查切换到 `head`（约 29 MB/篇）。
- 邮件使用 Resend 的 587 端口（STARTTLS），465 作为备用。
- **内存**：VPS 部署前已占用约一半内存且无 swap，M5 需要先处理（§13.1）。
