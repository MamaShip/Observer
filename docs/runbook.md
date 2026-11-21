# 运维手册

本手册介绍如何部署和维护一个独立的观察者。开发环境的运行方法见[开发指南](development.md)。

## 官方站点的部署环境

| 项目 | 值 |
|---|---|
| 服务器 | 香港 VPS（Ubuntu 24.04，913 MB 内存 + 2 GB swap，30 GB 磁盘） |
| 网站 | `https://ob.portkey.click`，快照域 `https://snap.ob.portkey.click` |
| 目录 | `/opt/observer`：`compose.yaml`、`Caddyfile`、`.env`（600）、`update.sh` |
| 容器 | `caddy`（80/443，自动 HTTPS）、`app`（observer）、`postgres`（18） |
| 数据卷 | `observer_pgdata`（数据库）、`observer_cache`（快照本地缓存）、`observer_caddy_data`（证书） |
| 对象存储 | Wasabi `observer-backup-001`（新加坡）：快照、图片、`backups/postgres/` 每日数据库备份 |
| 邮件 | Resend（`noreply@ob.portkey.click`） |

下文命令都在 `/opt/observer` 下执行；`dc` 表示 `docker compose`。

## 首次部署

1. **准备域名。** 选择自己的站点域名（下文以 `observer.example.org` 为例），把它和 `snap.observer.example.org` 的 A 记录指向你的服务器。第二个域名用于阅读文章快照。确认两条记录都已生效，且服务器对外开放 TCP 80 和 443 端口，以便 Caddy 申请证书。
2. **安装 Docker 和 Compose 插件。** 已安装的可跳过。下面是 Ubuntu 24.04 上以 root 身份安装的示例。
   ```sh
   # Docker（官方源）
   install -m 0755 -d /etc/apt/keyrings
   curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
   echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release; echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list
   apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
   ```
3. **准备配置。** 在服务器上创建 `/opt/observer` 目录，把仓库 [deploy](../deploy) 下的 `compose.yaml`、`Caddyfile` 和 `update.sh` 复制进去，再将 [.env.example](../deploy/.env.example) 复制为同目录下的 `.env`。进入该目录，生成两份独立的密钥，将输出分别填入对应的配置项。
   ```sh
   openssl rand -hex 24   # POSTGRES_PASSWORD
   openssl rand -hex 32   # SIGNING_KEY
   chmod 600 .env
   ```
   在 `.env` 中将 `DOMAIN` 改为你的站点域名，`SERVICE_NAME` 改为你想展示的名称。选择 `REGISTRATION_MODE=invite`（受邀邮箱才能登录）或 `open`（开放注册）。

   填写自己的 SMTP 发信配置和已验证的发件地址（`MAIL_FROM`），供站点发送验证码和删除提醒。将 `S3_ENDPOINT`、`S3_REGION`、`S3_BUCKET` 和访问密钥改为自己的 S3 兼容对象存储配置。文章、图片和数据库备份会写入这个存储桶。
4. **选择镜像。** 默认使用项目发布的 `ghcr.io/mamaship/observer:latest`。如果使用私有镜像，先在服务器上用有读取权限的账号运行 `docker login ghcr.io`。也可以在开发机上运行 `make image`，将自行构建的镜像传到服务器。
   ```sh
   docker save observer:local | gzip | ssh root@<服务器> 'gunzip | docker load'
   ```
   使用本地镜像时，把 `.env` 中的 `OBSERVER_IMAGE` 设为 `observer:local`。
5. **启动并验证。** 在 `/opt/observer` 下运行 `docker compose up -d --wait`，再用 `docker compose ps` 确认三个容器都在运行。把下面的示例域名换成自己的域名，检查网站是否能响应。
   ```sh
   curl -fsS https://observer.example.org/api/v1/healthz
   ```
6. **开通第一个账号。** 把下面的示例邮箱换成你自己的邮箱。第一条命令开通账号并安排发送邀请邮件，第二条授予管理后台权限。点击邮件中的注册链接，就可以提交第一篇文章。
   ```sh
   docker compose exec app observer admin invite you@example.org
   docker compose exec app observer admin role you@example.org admin
   ```

## 日常操作

```sh
dc ps                                   # 状态
dc logs -f --since 1h app               # 日志（JSON；不含访问者 IP）
dc logs --since 1h caddy                # Caddy 只记自身事件；请求信息已被过滤（无 IP、请求头、签名）
dc exec app observer admin stats        # 统计
dc exec app observer admin invite <email>       # 用户也可以在设置页邀请（每月 5 个），后台账户页可看邀请关系
dc exec app observer admin disable <email>      # 停用并退出所有设备
dc exec app observer admin articles -n 20
dc exec app observer admin email-limit <email> <n|default>  # 该账号每月通知邮件上限（0 = 不限）
dc exec app observer admin role <email> admin    # 授予本站 /admin 管理后台权限
dc exec app observer mailtest [-to <email>]     # 检查 SMTP（用容器内的环境变量）
docker stats --no-stream                # 内存
```

**冒烟测试**在开发机上用浏览器走一遍完整流程。将服务器、邮箱、站点域名和文章链接换成自己的值。

```sh
TOKEN=$(ssh root@<服务器> 'cd /opt/observer && docker compose exec -T app observer admin session <你的邮箱>')
cd web && BASE=https://observer.example.org SESSION=$TOKEN ARTICLE=https://mp.weixin.qq.com/s/... npm run smoke
```

脚本最后会退出登录，这个会话随之失效。

## 发布新版本

1. 在 `dev` 上确认 CI 通过，打标签并推送：`git tag v1.2.3 && git push origin v1.2.3`。
2. Release workflow 构建镜像 `ghcr.io/mamaship/observer:v1.2.3`（同时更新 `latest`）。
3. 配置了部署密钥时自动更新服务器；否则手动：`/opt/observer/update.sh v1.2.3`。

`update.sh` 先只更新 app 并等待健康检查，失败时自动回滚到之前的镜像。数据库迁移在 app 启动时自动执行；迁移只能向前，回滚到旧版本前先确认没有新迁移（`git diff v旧..v新 -- internal/store/migrations`）。

**自动部署的配置**（可选）：打 `v*` 标签后，Release workflow 构建镜像，再用一把专用钥匙 SSH 到服务器运行 `update.sh <标签>`。

1. 在开发机上生成钥匙（不要设密码）：`ssh-keygen -t ed25519 -N '' -f deploy_key -C github-deploy-observer`
2. 服务器 `/root/.ssh/authorized_keys` 末尾加一行，限制这把钥匙只能运行 `update.sh`（`restrict` 禁止终端和各种转发）：
   ```
   command="/opt/observer/update.sh",restrict ssh-ed25519 AAAA...（deploy_key.pub 的内容） github-deploy-observer
   ```
3. 服务器的主机公钥，作为 `DEPLOY_KNOWN_HOSTS`（直接从服务器读取，比 `ssh-keyscan` 可靠）：
   ```sh
   ssh root@<服务器 IP> 'for f in /etc/ssh/ssh_host_*_key.pub; do echo "<服务器 IP> $(cut -d" " -f1,2 $f)"; done'
   ```
4. GitHub 仓库 Settings → Secrets and variables → Actions：Secret `DEPLOY_SSH_KEY`（`deploy_key` 私钥全文）、`DEPLOY_KNOWN_HOSTS`（第 3 步的输出）；Variables 标签页里加 `DEPLOY_HOST`（服务器 IP）。
5. 验证：`ssh -i deploy_key root@<服务器 IP> <当前版本号>` 会重新部署当前版本（无变化）；`ssh -i deploy_key root@<服务器 IP> ls` 应被拒绝（打印 usage）。然后删除开发机上的 `deploy_key`。

`update.sh` 在切换前先备份数据库（失败则不升级），每次升级、回滚记录在 `/opt/observer/deploy.log`。它只换 app 镜像；`compose.yaml`、`Caddyfile` 有改动的版本需要先手动复制到服务器。撤销自动部署：删掉 `authorized_keys` 里那一行，或删掉 `DEPLOY_HOST` 变量。

## 备份与恢复

- 每天北京时间 03:30（UTC 19:30）自动执行 `pg_dump`，上传到 Wasabi `backups/postgres/YYYY-MM-DD.dump`（UTC 日期），保留 120 天。失败时给 `ADMIN_EMAILS` 发告警。
- 快照和图片本身就在 Wasabi，不需要另外备份；`observer_cache` 卷只是缓存，丢了会自动重新下载。
- 立即备份：`dc exec app observer admin backup`。

**恢复演练**（每次大改动后、至少每季度一次）：

```sh
dc exec postgres psql -U observer -c 'CREATE DATABASE restore_drill'
dc exec app sh -c 'observer admin restore -db "postgres://observer:$POSTGRES_PASSWORD@postgres:5432/restore_drill?sslmode=disable" -date YYYY-MM-DD'
dc exec postgres psql -U observer -d restore_drill -c 'select count(*) from users; select count(*) from watches'
dc exec postgres psql -U observer -c 'DROP DATABASE restore_drill'
```

`restore` 只接受空数据库，不会覆盖正在使用的库。

**整机恢复**（服务器丢失）：按"首次部署"在新机器上准备好，`.env` 用原来的值（尤其 `SIGNING_KEY`、S3 与 SMTP 凭据），然后：

```sh
dc up -d postgres
dc run --rm --no-deps --entrypoint sh app -c 'observer admin restore -db "postgres://observer:$POSTGRES_PASSWORD@postgres:5432/observer?sslmode=disable" -date <最近一天>'
dc up -d
```

最后把 DNS 指向新服务器。

## 告警处理

告警邮件发到 `ADMIN_EMAILS`，同一类告警 6 小时内只发一次。

| 告警 | 含义 | 处理 |
|---|---|---|
| 金丝雀检测失败 | 已知存活或已删除的文章判定结果不符，微信页面结构可能变了 | 用 `observer probe <链接>` 看微信返回什么；必要时补样本、改检测规则 |
| 微信频繁拦截，抓取暂停 | 熔断：短时间内多次被拦截，暂停 30 分钟 | 看日志里 `blocked` 的频率；持续出现时调大 `FETCH_MIN_INTERVAL`，参考 plan.md §11.3 |
| 文章连续检查失败 | 同一篇文章连续 6 次出错 | `observer admin articles` 查看，`observer probe` 复现 |
| 今日邮件用量达到 80% | 接近 Resend 免费档每天 100 封 | 偶发可忽略；经常出现时升级套餐或换服务商 |
| 数据库备份失败 | 每日 `pg_dump` 或上传失败 | `dc logs app | grep backup`；手动 `observer admin backup` 复现 |
| Healthchecks 邮件（如已配置） | 5 分钟一次的心跳停了：app 挂了或任务队列卡住 | `dc ps`、`dc logs app`、`curl .../api/v1/healthz` |

## 公众号入口

用户可以在个人订阅号里发送文章链接、绑定邮箱（plan.md §6.7）。只用被动回复，不需要 AppSecret 和 IP 白名单。

1. 在 `.env` 设置 `WECHAT_TOKEN`（自己生成，3–32 位字母或数字，如 `openssl rand -hex 16`）、`WECHAT_ACCOUNT_ID`（公众号原始 ID，`gh_` 开头，在「设置与开发 → 公众号设置」里）、`WECHAT_NAME`（公众号名称），然后 `dc up -d app`。
2. 先在开发机上确认回调可用（要求本机能访问站点）：`observer mpsim -url https://<域名> -token <WECHAT_TOKEN> -account <gh_…> 帮助`，应打印用法说明。这会生成一个 30 分钟有效的绑定链接，不影响别人。
3. 微信开发者平台（developers.weixin.qq.com，公众平台已不再提供「服务器配置」）里这个公众号的消息推送设置：URL 填 `https://<域名>/wechat/callback`，Token 同上，EncodingAESKey 随机生成（不使用），消息加解密方式选**明文模式**，数据格式选 **XML**（回调只解析 XML，选 JSON 会让每条消息都失败），提交（平台会用 GET 请求验证地址），再点「启用」。启用后后台的「自动回复」和「自定义菜单」不再生效。
4. 用手机关注公众号，按回复绑定一次，发一篇文章链接，确认回复里的链接能在微信里打开。

停用：在公众平台停用服务器配置，或清空 `WECHAT_TOKEN` 后 `dc up -d app`（回调返回 404，设置页不再显示公众号卡片，已有绑定保留）。

## 密钥轮换

| 密钥 | 方法 | 影响 |
|---|---|---|
| Resend API Key | Resend 后台新建 → 改 `.env` 的 `SMTP_PASSWORD` → `dc up -d app` → `observer mailtest` → 删除旧 Key | 无 |
| Wasabi Access Key | Wasabi 后台新建 → 改 `.env` → `dc up -d app` → 删除旧 Key | 无 |
| `SIGNING_KEY` | 改 `.env` → `dc up -d app` | 已发出的快照查看链接失效（最长 1 小时） |
| `WECHAT_TOKEN` | 改 `.env` → `dc up -d app` → 公众平台服务器配置里改成同一个 Token 并提交 | 两边不一致期间公众号无法回复 |
| `POSTGRES_PASSWORD` | `dc exec postgres psql -U observer -c "ALTER USER observer PASSWORD '新密码'"` → 改 `.env` → `dc up -d` | 重启期间短暂不可用 |

## 资源

- 内存：`docker stats`。容器上限 caddy 64 MB、app 256 MB、postgres 320 MB；app 设置了 `GOMEMLIMIT=200MiB`。
- 磁盘：`df -h /`、`docker system df`。容器日志每个最多 30 MB；快照缓存上限 `CACHE_MAX_BYTES`（默认 5 GB）。
- 不要运行 `docker system prune --volumes`：会删除数据库卷。
