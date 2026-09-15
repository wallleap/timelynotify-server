# 相对上游的改动

本仓库是 [Finb/bark-server](https://github.com/Finb/bark-server) 的修改版分支，**不会同步回原项目**。本文件记录相对上游 master 的改动，便于审计与升级时对照。

上游基线：`3df8990`（Finb/bark-server master）。

## 新增功能

| 功能 | 说明 | 文档 |
| ----- | ----------- | ---- |
| 原生 HarmonyOS 推送 | 华为 Push Kit 服务账号 JWT 鉴权，与 iOS APNs 并存，统一 API 按 `platform` 路由；Bark `level` 无 V3 直接对应，点击统一进入应用首页（`actionType=0`） | [API.md](docs/API.md) |
| 鸿蒙通知撤回（revoke） | 推送请求带真值 `revoke` + `id`（原 notifyId，正整数）时，调用华为 v1 `messages:revoke` 撤回该 key 下鸿蒙设备上未点击/未下发的通知；仅鸿蒙生效（iOS 无远程撤回），忽略其它推送参数、不写监控流；撤回端点用应用级 `clientID`（与发送的 v3 `projectId` 不同，在 `harmony/harmony_certs.go` 配置，留空不影响发送）；V1/V2/批量/MCP 全入口支持 | [API.md](docs/API.md) |
| 多平台 fan-out（同一 device_key 多端并存） | 数据库按 `(key, platform)` 唯一约束，**同一 `device_key` 可同时绑定 iOS 与鸿蒙记录**；推送时默认扇出到该 key 下所有有效平台（任一成功即 200，全部失败才 500），推送请求体的 `platform` 字段语义从"覆盖存储平台"改为"**收窄**到指定平台"；失效 token 清理改为按平台定向 `ClearDeviceTokenByKeyAndPlatform`，修复跨平台误清 bug | [TOKENS.md](docs/TOKENS.md) |
| Gotify 兼容监控及其它消息相关接口 | 设备级 `GET /<device_key>/version`、`GET /<device_key>/message`、`GET /<device_key>/stream`(WebSocket)，让 hotify-bridge 能像监测 Gotify 一样监测 bark | [GOTIFY_COMPAT.md](docs/GOTIFY_COMPAT.md) |
| 消息查找与导出 | 设备级 `GET /<device_key>/message` 支持 `?query=<关键词>`（title+body 不区分大小写子串匹配，响应含 `paging.total` 命中总数）与 `?limit=-1`（**分块流式导出**该设备全部现存消息，内存占用与总量无关） | [API.md](docs/API.md) |
| 消息增量同步（v0.7.0） | `GET /<device_key>/message` 新增 `?after=<id>`（返回更新消息、**id 升序**、`paging.hasMore` 翻页，与 since/query/limit=-1 互斥）与 `?deletedSince=<cursor>`（响应顶层附 `deletions` 信封：单删 ids、清空 purges/ceiling、500 条/页独立分页、保留期缺口 reset 重播种）；新增删除流水（单删/清空/TTL 过期/容量淘汰同事务埋点，30 天保留，清理前写 reset 哨兵）；推送 `ttl` 参数真正生效——后台约每分钟扫描过期归档消息并删除、写流水；严格向后兼容（缺省参数行为不变，普通响应仅多 `paging.hasMore`，流式导出无此字段） | [API.md](docs/API.md)、[GOTIFY_COMPAT.md](docs/GOTIFY_COMPAT.md) |
| MCP 推送 | `POST /mcp`、`POST /mcp/:device_key`，AI 代理可通过 Model Context Protocol 直接发推送 | [MCP.md](docs/MCP.md) |
| Basic Auth | 可选 `--user/--password`，`/ping` `/register` `/healthz` `/info` 全局白名单 + 设备级 `/:device_key/version` `/:device_key/message` `/:device_key/stream` 白名单（`/info` 无凭据显示基础信息，带凭据才含设备数） | |
| MySQL TLS | `--mysql-tls` 及配套 `mysql-ca`/`mysql-client-cert`/`mysql-client-key`/`mysql-tls-name`/`mysql-tls-skip-verify` | |
| Gotify 客户端 token | `--gotify-client-token`：预置 token 仅持久化 SHA-256 哈希；自动生成 token 为保持重启稳定，会在权限为 0600 的 `gotify.db` 中保存明文并仅打印一次 | |
| Gotify 消息上限 | `--gotify-max-messages`，配置监控消息保留条数（默认 `1000`） | |
| 日志分级/JSON | `--log-level`（`debug` \| `info` \| `warn` \| `error`）与 `--log-format`（`console` \| `json`） | |
| Prometheus `/metrics` | `GET /metrics`，提供 HTTP 请求指标 + 活跃 `/stream` 连接数 + Go/进程指标 | |
| 全局 `/version` 探测 | `GET /version`，以 `CommonResp` 格式返回 `data.version`，供 Bark/Hotify 客户端校验服务端身份；不在 Basic Auth 白名单（区别于设备级 `/:device_key/version` 与已移除的全局 gotify `/version`） | [API.md](docs/API.md) |
| IP 限流 | `--rate-limit-ip` / `--rate-limit-burst`，按来源 IP 对 `/register` `/mcp*` 限流（429）；推送端点 `/push` `/:device_key` 默认不限流，可经 `--rate-limit-push` 开启 | |
| 镜像非 root | Dockerfile 以非 root 用户 `app` 运行，数据目录归属该用户 | |
| Helm 持久化与密钥 | Helm chart 增加 PVC（`/data` 挂载）与 MySQL 凭据走 Secret（`BARK_SERVER_DSN` 从 secret 注入） | |

## 命名变更

- Go module：`github.com/finb/bark-server/v2` → `github.com/wallleap/timelynotify-server`
- 二进制：`bark-server` → `timelynotify-server`
- Docker 镜像：`finab/bark-server` → `wallleap/timelynotify-server`
- HTTP ServerHeader：`Bark` → `TimelyNotify`
- CLI 名称 / 日志 / MCP 服务名均改为 TimelyNotify 前缀
- 无鉴权部署在启动日志给出醒目警告，提示公网部署需开启 Basic Auth

## 其它调整

- `deploy/` 下部署产物（Dockerfile、docker-compose.yaml、systemd unit、helm chart）已改用新二进制与镜像名，不依赖原项目
- `.github/workflows/ci.yaml` 打 `v*` 标签时构建并推送自有镜像，Docker Hub 命名空间读取 `DOCKERHUB_USERNAME` secret，另推送 GHCR（`packages: write` 权限已在 workflow 声明）
- README 增加 CI 推送与 Secrets（`DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN`）及 GHCR Workflow 权限配置说明
- README 重写为本项目自有内容，不再指向原项目部署方式
- `docs/API.md` 统一说明容器对外端口示例（`18080`）、`id`/`delete` 字段、批量推送响应、参数优先级、认证与本 fork 新增接口（register / gotify / mcp）
