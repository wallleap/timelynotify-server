# TimelyNotify Server

TimelyNotify Server 是 [Bark](https://github.com/Finb/Bark) 服务端（[Finb/bark-server](https://github.com/Finb/bark-server)）的一个**修改版分支**，扩展了以下能力：

- **原生双平台推送**：同时支持 iOS（APNs）和 HarmonyOS（华为 Push Kit）原生推送，统一 API 自动路由
- **Gotify 兼容监控**：每一条推送都会进入 Gotify 风格的监控流，供 [hotify-bridge](https://github.com/sakura-lolipop/hotify-bridge) 消费
- **MCP 接口**：AI 代理可直接通过 MCP 协议调用推送

> **注意**：本项目基于上游修改，**不会同步回原项目**，也不会再使用上游的构建产物 / 镜像。所有二进制、镜像、module 路径均已独立命名，与上游可明确区分。

- iOS 需下载 Bark
- HarmonyOS 需下载 Hotify

目前 hotify-bridge 可以监控到所有消息，因此只适合个人部署使用，不推荐作为公共服务开放给其他用户。
如果想让 hotify-bridge 只监控某个 device_key 的消息，可在 hotify-bridge 的 `gotify_url` 里追加该 `device_key` 路径（见下文依赖 hotify-bridge 小节）。

## 与原项目的区别

- 独立的 Go module、二进制名与 Docker 镜像名（`wallleap/timelynotify-server`）
- 原生 HarmonyOS 推送支持（华为 Push Kit 服务账号 JWT 鉴权，与 iOS APNs 并存，统一 API 按 `platform` 路由）
- 内置 [Gotify 兼容接口](./docs/GOTIFY_COMPAT.md)（设备级 `/<device_key>/version`、`/<device_key>/message`、`/<device_key>/stream` 等），供 hotify-bridge 监测 bark 推送或操作消息
- 内置 [MCP](./docs/MCP.md) 接口（`/mcp`、`/mcp/:device_key`），AI 代理可直接调用推送
- 可选 Basic Auth、MySQL TLS、gotify 客户端 token 等

> 改动清单见 [DIFFERENCES.md](DIFFERENCES.md)。

## 安装

### 部署文件

本项目的部署产物位于 `deploy/` 目录，均已改为独立命名、不依赖原项目：

| 文件 | 说明 |
|---|---|
| `deploy/Dockerfile` | 构建镜像（二进制 `timelynotify-server`） |
| `deploy/docker-compose.yaml` | Docker Compose 部署（远程镜像 `wallleap/timelynotify-server`） |
| `deploy/docker-compose.local.yaml` | Docker Compose 部署（本地构建镜像，`bin/up` 默认使用） |
| `deploy/timelynotify-server.service` | systemd 服务 |
| `deploy/entrypoint.sh` | 容器入口，设置时区 |
| `deploy/helm-chart/` | Kubernetes Helm Chart |

### Docker

```sh
docker run -dt --name timelynotify-server --restart unless-stopped \
  -p 18080:8080 \
  -v `pwd`/bark-data:/data \
  -e BARK_SERVER_GOTIFY_CLIENT_TOKEN="your-gotify-client-token" \
  -e BARK_SERVER_BASIC_AUTH_USER="admin" \
  -e BARK_SERVER_BASIC_AUTH_PASSWORD="secret" \
  wallleap/timelynotify-server
```

> 容器以非 root 用户 `app`（uid 1000）运行，首次挂载 host 数据目录时需把属主改为该 uid，否则报 `permission denied`：
> ```sh
> sudo chown -R 1000:1000 `pwd`/bark-data
> ```
> 用 Docker 命名卷（`docker volume create` + `-v <volume>:/data`）可免去手动 chown。

> 镜像推送到 Docker Hub（用户 `wallleap`）。如需自己的仓库，用 `docker tag wallleap/timelynotify-server yourname/timelynotify-server`。

使用 docker-compose：

```sh
# 复制本项目 deploy/docker-compose.yaml 到任意目录
mkdir timelynotify-server && cd timelynotify-server
curl -sL https://raw.githubusercontent.com/wallleap/timelynotify-server/master/deploy/docker-compose.yaml -o docker-compose.yaml
# 提前赋权避免容器权限报错
sudo chown -R 1000:1000 ./data
# 后台启动
docker compose up -d
```

`deploy/docker-compose.yaml` 已内置注释的环境变量入口，按需取消注释即可。

> **本地开发**：`bin/up` / `bin/down` 默认使用 `deploy/docker-compose.local.yaml`（本地构建镜像，不拉远程），`bin/up` 会自动 `--build`。
> 如需用远程镜像可运行 `COMPOSE_FILE=deploy/docker-compose.yaml bin/up`。

### CI 构建与推送（GitHub Actions）

`.github/workflows/ci.yaml` 会在打上 `v*` 开头的标签时自动构建并推送镜像到 **Docker Hub** 和 **GHCR**：

> **版本规范**：`v*` 开头的 tag 必须符合[语义化版本](https://semver.org/)（`vMAJOR.MINOR.PATCH`，可带 `-prerelease`/`+build`，如 `v1.2.3`、`v2.0.0-rc.1`），推送到 Docker Hub 与 GHCR 的镜像 tag 即该版本号。

推荐用 `bin/release` 发版（自动更新 CHANGELOG → 打 tag → 推送触发 CI）：

```sh
bin/release          # 自动递增 MINOR（如 v0.2.0 → v0.3.0，基于最近发布的 tag，可跨 MAJOR）
bin/release v0.5.0   # 自定义版本号（语义化版本，可带 -prerelease/+build）
bin/release --dry-run  # 只打印将要执行的动作，不实际修改
bin/release --no-push  # 更新 CHANGELOG + 打 tag，但不推送（手动推）
```

- Docker Hub：`<DOCKERHUB_USERNAME>/timelynotify-server`
- GHCR：`ghcr.io/<GitHub 账号>/timelynotify-server`

推送镜像需要配置两个 Secrets（仓库 **Settings → Secrets and variables → Actions**）：

| Secret | 说明 |
|---|---|
| `DOCKERHUB_USERNAME` | Docker Hub 用户名，用于登录及镜像命名空间 |
| `DOCKERHUB_TOKEN` | Docker Hub [Access Token](https://hub.docker.com/settings/security)（非登录密码） |

GHCR 推送使用仓库自带 `GITHUB_TOKEN`，需在 **Settings → Actions → General → Workflow permissions** 中开启 **Read and write permissions**（`packages: write` 已在 workflow 内显式声明）。

### systemd

```sh
# 1. 安装二进制
install -m 755 timelynotify-server /usr/local/bin/timelynotify-server

# 2. 复制服务文件
cp deploy/timelynotify-server.service /etc/systemd/system/

# 3. 创建参数环境文件（修改后的参数放在这里）
cat > /etc/timelynotify-server.env <<'EOF'
BARK_SERVER_GOTIFY_CLIENT_TOKEN=your-gotify-client-token
BARK_SERVER_BASIC_AUTH_USER=admin
BARK_SERVER_BASIC_AUTH_PASSWORD=secret
EOF

# 4. 启动
systemctl daemon-reload
systemctl enable --now timelynotify-server
```

### 直接运行

1. 自行编译或从 [releases](https://github.com/wallleap/timelynotify-server/releases) 下载预编译二进制
2. 添加执行权限：`chmod +x timelynotify-server`
3. 启动（含修改后的参数）：

```sh
./timelynotify-server --addr 0.0.0.0:8080 --data ./bark-data \
  --gotify-client-token your-gotify-client-token \
  --user admin --password secret
```

4. 测试：`curl localhost:8080/ping`

**注意：服务端默认使用 `/data` 目录存储数据，请确保有写权限，否则用 `--data` 指定目录。**

### 主要参数（相对上游新增/常用）

| 参数 / 环境变量 | 说明 |
|---|---|
| `--addr` / `BARK_SERVER_ADDRESS` | 监听地址，默认 `0.0.0.0:8080` |
| `--data` / `BARK_SERVER_DATA_DIR` | 数据目录（bbolt + gotify.db），默认 `/data` |
| `--gotify-client-token` / `BARK_SERVER_GOTIFY_CLIENT_TOKEN` | Gotify 兼容监控客户端 token，自动生成并持久化。**hotify-bridge 需用它当作 `gotify_token`**（依赖见下文） |
| `--gotify-max-messages` / `BARK_SERVER_GOTIFY_MAX_MESSAGES` | Gotify 监控消息保留上限，默认 `0`（使用内置默认 `1000`） |
| `--user` / `--password` / `BARK_SERVER_BASIC_AUTH_{USER,PASSWORD}` | 可选 Basic Auth，同时设置后开启，所有非白名单路径请求头要带 `Authorization: Basic base64(user:password)` |
| `--dsn` / `BARK_SERVER_DSN` | 改用 MySQL 替代 Bbolt |
| `--mysql-tls` `--mysql-ca` `--mysql-client-cert` `--mysql-client-key` `--mysql-tls-name` `--mysql-tls-skip-verify` | MySQL TLS 相关 |
| `--max-batch-push-count` / `BARK_SERVER_MAX_BATCH_PUSH_COUNT` | 批量推送上限，默认 `-1` 不限 |
| `--max-apns-client-count` | APNs 客户端连接数 |
| `--rate-limit-ip` / `BARK_SERVER_RATE_LIMIT_IP` | 按来源 IP 对 `/register` `/mcp*` 限流（请求/秒），默认 `0` 关闭 |
| `--rate-limit-burst` / `BARK_SERVER_RATE_LIMIT_BURST` | IP 限流突发窗口 token 数，默认等于 `rate-limit-ip` |
| `--rate-limit-push` / `BARK_SERVER_RATE_LIMIT_PUSH` | 额外把限流应用到推送端点 `/push` 与 `/:device_key`（默认关闭，推送默认不限流） |
| `--log-level` / `BARK_SERVER_LOG_LEVEL` | 日志级别 `debug|info|warn|error`，默认 `info` |
| `--log-format` / `BARK_SERVER_LOG_FORMAT` | 日志格式 `console|json`，默认 `console` |
| `--unix-socket`、`--url-prefix`、`--cert`/`--key` | 监听方式 / 前缀 / TLS |

完整参数见 `./timelynotify-server --help`。

### 安全建议（公网部署必备）

> **默认无鉴权**：未配置 Basic Auth 时，`/push`、`/register`、`/mcp*` 与 `/:device_key` 对全网开放（启动日志会给出醒目警告）。**公网部署务必**：

1. 开启 Basic Auth：`BARK_SERVER_BASIC_AUTH_USER` / `BARK_SERVER_BASIC_AUTH_PASSWORD`（`/push`、`/mcp*`、`/:device_key` 受保护；白名单路径 `/ping /register /healthz /info` + 设备级 `/:device_key/version /:device_key/message /:device_key/stream` 仍开放——其中 `/:device_key/message` `/:device_key/stream` 走 gotify token 鉴权，`/info` 无凭据返回基础信息、带有效 Basic Auth 才返回设备数）。
2. 配置限流：`BARK_SERVER_RATE_LIMIT_IP=10`（每秒每 IP 最多 10 次）可缓解 CC / 刷注册。推送端点 `/push`、`/:device_key` 默认不限流（避免误伤正常推送），确需限制时再加 `BARK_SERVER_RATE_LIMIT_PUSH=true`。
3. 建议前置 **HTTPS 反向代理**（如 Caddy / Nginx），并限制其仅转发到 `:8080`。
4. 数据目录 `/data` 收紧为服务运行用户可读写。

### 依赖 hotify-bridge（Gotify 兼容监控）

本 fork 的 Gotify 兼容接口供 [hotify-bridge](https://github.com/sakura-lolipop/hotify-bridge) 监测推送。部署时：

1. 设置 `BARK_SERVER_GOTIFY_CLIENT_TOKEN`（**强烈推荐预置**：预置时服务端只存 SHA-256 哈希、不落明文凭证；不设置时自动生成的 token 明文会写进 `<data>/gotify.db`，且仅首次启动在日志打印一次）。
2. 在 hotify-bridge 的 `bridge_config.yaml` 填入：
   ```yaml
   gotify_url: http://<bark-host>:18080 # 监控所有，如果只想监控某个 device_key 可以写 http://<bark-host>:18080/<device_key>
   gotify_token: <上面的 client token>
   ```
   或使用环境变量 `GOTIFY_HTTP_URL` / `GOTIFY_CLIENT_TOKEN`。
3. 数据目录需可写，以便持久化监控消息（`<data>/gotify.db`）。

详见 [GOTIFY_COMPAT.md](./docs/GOTIFY_COMPAT.md)。

### 编译

依赖：

- Golang 1.18+
- Go Mod Enabled（`GO111MODULE=on`）
- Go Mod Proxy Enabled（`GOPROXY=https://goproxy.cn`）
- [go-task](https://taskfile.dev/installation/)

```sh
# 交叉编译全部平台
task

# 编译指定平台（参考 Taskfile.yaml）
task linux_amd64
task linux_amd64_v3
```

也可以使用脚本 `bin/build` 直接编译本机二进制。

发布 Docker 镜像（实际使用的 GitHub workflow 自动构建发布）：

```sh
bin/publish              # 构建并推送单架构镜像（宿主机架构）
bin/publish --all        # 多架构构建并推送（linux/amd64, linux/arm64, linux/arm/v7）
PLATFORM=linux/amd64,linux/arm64 bin/publish   # 指定架构
```

### 使用 MySQL 替代 Bbolt

以 `-dsn=user:pass@tcp(mysql_host)/bark` 启动即可使用 MySQL：

```sh
./timelynotify-server --dsn "user:pass@tcp(mysql_host)/bark"
```

开启 TLS：

```sh
./timelynotify-server \
  --dsn "user:pass@tcp(mysql_host)/bark" \
  --mysql-tls \
  --mysql-tls-name custom \
  --mysql-ca /etc/ssl/certs/ca.pem \
  --mysql-client-cert /etc/ssl/client-cert.pem \
  --mysql-client-key /etc/ssl/client-key.pem
# 忽略证书校验（仅测试环境）: --mysql-tls-skip-verify
```

## 文档

* [TUTORIAL.md](./docs/TUTORIAL.md) — 快速上手教程：token / key 填写到哪里
* [API_V2.md](./docs/API_V2.md) — 推送 API
* [GOTIFY_COMPAT.md](./docs/GOTIFY_COMPAT.md) — Gotify 兼容监控接口
* [MCP.md](./docs/MCP.md) — MCP 推送接口
* [TOKENS.md](./docs/TOKENS.md) — device_token、device_key、client token 是什么及如何生成
* [OPTIMIZATION_REVIEW.md](./docs/OPTIMIZATION_REVIEW.md) — 优化建议可行性核对（对现有代码逐条标注已实现/未实现）
* [DIFFERENCES.md](./DIFFERENCES.md) — 相对上游的改动清单

## 其它

`push_test` 测试前需要修改 `deviceToken` 为真实的 Bark App 上显示的 Device Token

## License

MIT，见 [LICENSE](LICENSE)。上游版权归原作者（mritd / Finb）所有。
