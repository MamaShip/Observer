# 运维手册

Observer 生产环境的部署、日常操作、发布、备份恢复与告警处理。设计背景见 [plan.md](../plan.md) §13。

## 概览

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

## 首次部署（新机器）

1. **DNS**：在 Namecheap 为 `ob` 和 `snap.ob` 添加 A 记录，指向服务器 IP。用 `dig +short ob.portkey.click @1.1.1.1` 确认生效后再启动 Caddy，否则证书申请失败会触发 Let's Encrypt 的频率限制。
2. **系统**：
   ```sh
   # Docker（官方源）
   install -m 0755 -d /etc/apt/keyrings
   curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
   echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release; echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list
   apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
   # 日志上限
   mkdir -p /etc/systemd/journald.conf.d && printf '[Journal]\nSystemMaxUse=100M\n' > /etc/systemd/journald.conf.d/size.conf && systemctl restart systemd-journald
   ```
   swap（2 GB，`vm.swappiness=10`）已在 M0 配置。
3. **文件**：把仓库里的 `deploy/compose.yaml`、`deploy/Caddyfile`、`deploy/update.sh` 复制到 `/opt/observer`，`deploy/.env.example` 复制为 `.env` 并填写：
   ```sh
   openssl rand -hex 24   # POSTGRES_PASSWORD
   openssl rand -hex 32   # SIGNING_KEY
   chmod 600 .env
   ```
4. **镜像**：GHCR 的包需设为公开（GitHub → Packages → observer → Package settings → Change visibility），或在服务器上 `docker login ghcr.io`。没有发布版本时，可以在开发机 `make image` 后：
   ```sh
   docker save observer:local | gzip | ssh root@<服务器> 'gunzip | docker load'
   ```
   并把 `.env` 中 `OBSERVER_IMAGE` 设为 `observer:local`。
5. **启动**：`dc up -d`，然后 `dc ps` 确认三个容器都是 healthy，`curl -s https://ob.portkey.click/api/v1/healthz`。
6. **管理员账号**：`dc exec app observer admin invite <你的邮箱>`，然后在网页上用验证码登录。

## 日常操作

```sh
dc ps                                   # 状态
dc logs -f --since 1h app               # 日志（JSON；不含访问者 IP）
dc logs --since 1h caddy                # Caddy 只记自身事件；请求信息已被过滤（无 IP、请求头、签名）
dc exec app observer admin stats        # 统计
dc exec app observer admin invite <email>
dc exec app observer admin disable <email>      # 停用并退出所有设备
dc exec app observer admin articles -n 20
dc exec app observer admin email-limit <email> <n|default>  # 该账号每月通知邮件上限（0 = 不限）
dc exec app observer mailtest [-to <email>]     # 检查 SMTP（用容器内的环境变量）
docker stats --no-stream                # 内存
```

**冒烟测试**（在开发机上用浏览器走一遍完整流程）：

```sh
TOKEN=$(ssh root@<服务器> 'cd /opt/observer && docker compose exec -T app observer admin session <你的邮箱>')
cd web && BASE=https://ob.portkey.click SESSION=$TOKEN ARTICLE=https://mp.weixin.qq.com/s/... npm run smoke
```

脚本最后会退出登录，这个会话随之失效。

## 发布新版本

1. 在 `dev` 上确认 CI 通过，打标签并推送：`git tag v1.2.3 && git push origin v1.2.3`。
2. Release workflow 构建镜像 `ghcr.io/mamaship/observer:v1.2.3`（同时更新 `latest`）。
3. 配置了部署密钥时自动更新服务器；否则手动：`/opt/observer/update.sh v1.2.3`。

`update.sh` 先只更新 app 并等待健康检查，失败时自动回滚到之前的镜像。数据库迁移在 app 启动时自动执行；迁移只能向前，回滚到旧版本前先确认没有新迁移（`git diff v旧..v新 -- internal/store/migrations`）。

**自动部署的配置**（可选）：

```sh
ssh-keygen -t ed25519 -N '' -f deploy_key -C github-deploy
# 服务器 /root/.ssh/authorized_keys 加一行，限制这把钥匙只能运行 update.sh：
command="/opt/observer/update.sh",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... github-deploy
ssh-keyscan <服务器 IP>   # 输出作为 DEPLOY_KNOWN_HOSTS
```

GitHub 仓库 Settings → Secrets and variables → Actions：Secret `DEPLOY_SSH_KEY`（`deploy_key` 私钥内容）、`DEPLOY_KNOWN_HOSTS`；Variable `DEPLOY_HOST`（服务器 IP）。

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

## 密钥轮换

| 密钥 | 方法 | 影响 |
|---|---|---|
| Resend API Key | Resend 后台新建 → 改 `.env` 的 `SMTP_PASSWORD` → `dc up -d app` → `observer mailtest` → 删除旧 Key | 无 |
| Wasabi Access Key | Wasabi 后台新建 → 改 `.env` → `dc up -d app` → 删除旧 Key | 无 |
| `SIGNING_KEY` | 改 `.env` → `dc up -d app` | 已发出的快照查看链接失效（最长 1 小时） |
| `POSTGRES_PASSWORD` | `dc exec postgres psql -U observer -c "ALTER USER observer PASSWORD '新密码'"` → 改 `.env` → `dc up -d` | 重启期间短暂不可用 |

## 资源

- 内存：`docker stats`。容器上限 caddy 64 MB、app 256 MB、postgres 320 MB；app 设置了 `GOMEMLIMIT=200MiB`。
- 磁盘：`df -h /`、`docker system df`。容器日志每个最多 30 MB；快照缓存上限 `CACHE_MAX_BYTES`（默认 5 GB）。
- 不要运行 `docker system prune --volumes`：会删除数据库卷。
