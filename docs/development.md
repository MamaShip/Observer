# 开发指南

以下命令从仓库根目录运行。部署自己的站点请参阅[运维手册](runbook.md#首次部署)。

## 本地运行

准备好 Git、Make、Go（版本见 [go.mod](../go.mod)）、Node.js 22.12 或更高版本，以及带 Compose 插件的 Docker。

```sh
make web
make dev
```

`make web` 安装前端依赖并构建网页。`make dev` 启动本地 PostgreSQL（`127.0.0.1:55432`）和 Go 服务。打开 <http://localhost:8080> 即可使用。本地默认开放注册，未配置 SMTP 时可以从运行 `make dev` 的终端复制登录验证码。

按 Ctrl+C 停止服务，再运行 `make dev-down` 可以停止 PostgreSQL 并保留数据。如果 8080 已被占用，一起调整监听地址、网站地址和快照地址。网站和快照使用不同的主机名。

```sh
HTTP_ADDR=127.0.0.1:18080 BASE_URL=http://localhost:18080 SNAP_BASE_URL=http://127.0.0.1:18080 make dev
```

## 修改前端

需要热更新时，在两个终端分别运行以下命令，再打开 <http://localhost:5173>。Vite 会把 API 请求转发给 Go 服务。`BASE_URL` 要与浏览器访问的地址一致。

```sh
# 终端 1
BASE_URL=http://localhost:5173 make dev

# 终端 2
make web-dev
```

## 测试与构建

| 命令 | 用途 |
| --- | --- |
| `make test` | 启动本地 PostgreSQL，运行全部 Go 测试（含集成测试） |
| `make test-unit` | 运行无需数据库的 Go 测试 |
| `make web-test` | 检查前端类型，运行前端单元测试 |
| `make lint` | 使用 golangci-lint 检查 Go 代码 |
| `make sqlc` | 修改数据库查询或迁移后，用 sqlc 重新生成 Go 代码 |
| `make openapi` | 修改 API 后，重新生成前端类型 `web/src/api/schema.d.ts` |
| `make build` | 构建前端并嵌入可执行文件 `bin/observer` |

运行 `make lint` 或 `make sqlc` 前，分别安装 golangci-lint 或 sqlc。工具版本见 [CI 配置](../.github/workflows/ci.yml)。本地服务也提供 OpenAPI 文档。可访问 <http://localhost:8080/api/v1/openapi.yaml> 或 <http://localhost:8080/api/v1/openapi.json>。

要用真实浏览器验收登录、提交文章、查看备份和打印 PDF 的流程，先运行 `make web`，然后在两个终端执行下列命令。把 `ARTICLE` 换成一篇可访问的真实公众号文章链接，脚本会实际抓取该文章。默认使用 `/usr/bin/google-chrome`，也可设置 `CHROME` 指向 Chromium。

```sh
# 终端 1
make dev > /tmp/observer.log 2>&1

# 终端 2
cd web
LOG=/tmp/observer.log OUT=/tmp/shots ARTICLE='https://mp.weixin.qq.com/s/...' npm run smoke
```

## 命令行工具与配置

构建后可以用 `bin/observer` 调用以下子命令。配置来自环境变量。连接本地开发数据库时，先在运行工具的终端设置以下两个变量。

```sh
export DATABASE_URL='postgres://observer:observer@127.0.0.1:55432/observer?sslmode=disable'
export BLOB_DIR=data/blob
```

| 子命令 | 用途 |
| --- | --- |
| `migrate [up\|status]` | 执行或查看数据库迁移。服务启动时也会自动迁移 |
| `admin stats`、`admin articles` | 查看站点统计、最近的文章及其状态 |
| `admin invite <email>`、`admin role <email> admin` | 开通账号、授予管理后台权限 |
| `admin disable`、`admin enable`、`admin logout-all`、`admin email-limit`、`admin session` | 管理账号状态、会话和通知额度 |
| `admin submit`、`admin check`、`admin export` | 提交文章、安排检查、导出备份 |
| `admin backup`、`admin restore -db URL` | 将数据库备份到对象存储，或恢复到空数据库 |
| `mailtest -env-file F [-to ADDR]` | 验证 SMTP 配置，加 `-to` 发送测试邮件 |
| `openapi` | 输出 OpenAPI JSON |
| `probe <url>...`、`netcheck` | 诊断文章抓取，测量到微信、Wasabi 和 SMTP 的网络路径 |

配置项见[部署示例](../deploy/.env.example)和[配置定义](../internal/config/config.go)。设计背景、开发记录与实测结果见 [v2 重构方案](../plan.md)。旧版 Python 微信公众号机器人于 2022 年停服，源码保存在 [legacy-python 标签](https://github.com/MamaShip/Observer/tree/legacy-python)。
