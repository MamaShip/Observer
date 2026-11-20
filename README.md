<p align="center">
  <img alt="logo" src="./docs/logo.jpg" width="150px" />
  <h1 align="center">Observer 折光观察者</h1>
</p>

微信公众号文章备份与删除监测服务：提交文章链接后，服务会立即备份文章，并在观察期内定期检查文章是否还能访问；文章被删除时发邮件通知，备份可以在网页上查看，或下载为单文件 HTML / 打印为 PDF。

> **正在重写（v2）。** 旧版（Python + 微信公众号机器人，2022 年已停服）存档在 tag [`legacy-python`](../../tree/legacy-python)。
> 新版的设计、进度与实测记录见 [plan.md](plan.md)。

## 开发

需要 Go（版本见 `go.mod`）、Node.js 22+（只用于构建前端）、Docker（用于本地 Postgres）、[sqlc](https://sqlc.dev/)、[golangci-lint](https://golangci-lint.run/)。

```sh
make web        # 构建前端（web/）并放到 internal/webui/dist/app，之后的 go build 会把它嵌入二进制
make dev        # 启动本地 Postgres（127.0.0.1:55432）并运行服务：http://localhost:8080
make test       # 全部 Go 测试，包括需要 Postgres 的集成测试
make test-unit  # 只跑不需要数据库的 Go 测试
make web-test   # 前端类型检查与单元测试
make lint
make sqlc       # 修改 internal/store/queries 或迁移后重新生成代码
make openapi    # 修改 API 后重新生成前端的类型（web/src/api/schema.d.ts）
make build      # 构建前端并编译 bin/observer
make dev-down   # 停止本地 Postgres（保留数据）
```

改前端时用 Vite 开发服务器（热更新），API 请求代理到 Go 服务：

```sh
BASE_URL=http://localhost:5173 make dev   # 终端 1：BASE_URL 要等于浏览器访问的地址
make web-dev                              # 终端 2：打开 http://localhost:5173
```

浏览器验收（真实 Chrome、手机尺寸，从登录到打印 PDF 走一遍，会真实抓取一次微信）：

```sh
make dev > /tmp/observer.log 2>&1 &
cd web && LOG=/tmp/observer.log OUT=/tmp/shots ARTICLE=https://mp.weixin.qq.com/s/... npm run smoke
```

`make dev` 是开放注册模式，并且没有配置 SMTP：登录验证码只写进服务日志，从日志里复制即可。8080 端口被占用时换端口，三个地址要一起改：`HTTP_ADDR=127.0.0.1:18080 BASE_URL=http://localhost:18080 SNAP_BASE_URL=http://127.0.0.1:18080 make dev`。

其他子命令：

```sh
observer migrate [up|status]       # 数据库迁移（serve 启动时也会自动执行）
observer admin stats               # 统计
observer admin invite <email>      # 邀请制下开通账号（另有 disable / enable / logout-all / submit / articles / check / export）
observer mailtest -env-file F [-to ADDR]  # 验证 SMTP 配置；加 -to 发送示例邮件
observer openapi                   # 输出 API 的 OpenAPI 文档
observer admin backup | restore    # 立即备份数据库到对象存储 / 恢复到空数据库
observer probe <url>...            # 抓取诊断：看微信对某个链接返回了什么
observer netcheck                  # 测量到微信、Wasabi、SMTP 的网络路径
```

API 的 OpenAPI 文档：`http://localhost:8080/api/v1/openapi.yaml`（或 `.json`）。

配置全部来自环境变量，见 [plan.md §13.3](plan.md)。

部署、发布、备份恢复与告警处理见 [docs/runbook.md](docs/runbook.md)。

## 背景

受启发于[端点星计划](https://github.com/Terminus2049/Terminus2049.github.io)（参见维基词条：[端点星事件](https://zh.wikipedia.org/wiki/%E7%AB%AF%E7%82%B9%E6%98%9F%E4%BA%8B%E4%BB%B6)）。

本项目只为个人用户备份自己关注的文章，不做被审查文章的全量备份。全量备份工作有香港大学的 [WeChatSCOPE](https://wechatscope.jmsc.hku.hk/) 项目在做，欢迎向他们提交值得备份的公众号。

## Contributors ✨

<table>
  <tr>
    <td align="center"><a href="http://www.twisted-meadows.com"><img src="https://avatars3.githubusercontent.com/u/7104870?v=4" width="100px;" alt=""/><br /><sub><b>游荡</b></sub></a><br /><a href="https://github.com/MamaShip/Observer/commits?author=MamaShip" title="Code">💻</a> <a href="https://github.com/MamaShip/Observer/commits?author=MamaShip" title="Documentation">📖</a> <a href="#maintenance-MamaShip" title="Maintenance">🚧</a></td>
    <td align="center"><a href="https://github.com/ChenliangLi205"><img src="https://avatars2.githubusercontent.com/u/33442091?v=4" width="100px;" alt=""/><br /><sub><b>ChenliangLi205</b></sub></a><br /><a href="https://github.com/MamaShip/Observer/commits?author=ChenliangLi205" title="Code">💻</a> <a href="#maintenance-ChenliangLi205" title="Maintenance">🚧</a></td>
    <td align="center"><a href="https://github.com/Friiiii"><img src="https://avatars2.githubusercontent.com/u/66207271?v=4" width="100px;" alt=""/><br /><sub><b>Friiiii</b></sub></a><br /><a href="#design-Friiiii" title="Design">🎨</a></td>
  </tr>
</table>

Contributions of any kind welcome!

## License

[GPL-3.0](LICENSE)
