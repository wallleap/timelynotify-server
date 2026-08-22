# 优化建议可行性核对

> 本文档针对《timelynotify-server 项目优化点建议》逐条核对现有代码实现，标注：
> - ✅ 已实现 | 🟡 部分实现 | ⬜ 未实现（建议项）
> - 附代码定位。核对基线：当前 master（go 1.25.5，fiber v2.52.13）。
>
> 状态仅代表"建议与现状的一致性核对"，非承诺排期；未实现项如需落地，另起 issue/改动。

## 一、功能与业务体验优化

### 1.1a 推送结果回执 / 异步回调（回执、重试、降级）
⬜ 未实现。`push()`（`route_push.go:208`）是同步调用 `apns.Push` 并返回 HTTP；HTTP 侧仅透传 APNs 错误码，无异步回调、无自动重试。`410/400 BadDeviceToken` 会清除设备记录（`route_push.go:273`）属"降级"，但无定时重试。
> 涉及：`route_push.go` push、`apns/apns.go` Push。

### 1.1b Gotify 兼容层增强
🟡 部分实现：
- **已实现**：消息持久化（`internal/gotifycompat/store.go` bbolt/内存降级）、消息上限裁剪（`max` 条数，新增滚动删除最旧）、`DELETE /message` 与 `DELETE /message/:id`（`route_gotify.go:24-25`）、WebSocket `/stream`（恒定时间 token 校验）、优先级字段映射（`gotify_publish.go:39` `gotifyPriority`，把 `level` 映射到 gotify 0-2）。
- ⬜ 未实现：已读状态同步、按时间自动过期清理（现仅按条数上限裁剪）、多客户端 Token（仅单 token，`service.go` / `token.go`）。

### 1.2c MCP 接口能力扩展
🟡 部分实现：
- 已实现：`notify` 单工具（`route_mcp.go:53`）、通用 `/mcp` + 设备专属 `/mcp/:device_key` 两个端点、工具参数含 title/subtitle/body/level/volume/badge/sound/…、`ToolCapabilities` 声明（`route_mcp.go:41`）。
- ⬜ 未实现：设备列表、历史回读、批量、定时等其它工具。
> 注：建议中"多设备批量推送"实际可通过现有 `device_keys`（V2 `/push`）实现；批量推送已有（`route_push.go:137-175`）。

### 1.2d 鸿蒙直连（HMS Push 内置）
⬜ 未实现。当前仅取道 hotify-bridge 中转（`gotifyPublish` → `/stream`）。无华为 Push Kit 适配。
> 与上游架构差异大，属独立新模块；宜单列拆分评估。

### 1.2e 设备与权限管理（列表/拉黑/分组/记录）
🟡 部分实现：
- 已实现：设备删除（`database.Database.DeleteDeviceByKey`，各实现：`bbolt.go:96` / `mysql.go:128` / `membase.go:40` / `envbase.go:33`）、设备查证（`/register/:device_key`，`route_register.go:75`）、总数（`CountAll`，`/info`）。
- ⬜ 未实现：HTTP 层拉黑/禁用接口、设备备注/分组、推送记录查询、多业务分组隔离、除详情外的设备/历史管理接口。
> 抽查确认：`DeleteDeviceByKey` 目前只在 APNs 无效 token 时内部调用（`route_push.go:274`），**未暴露 HTTP 管理接口**。

## 1、性能与架构优化

### 1.1a Bbolt 写锁 / compact
⬜ 未实现。`push` 仍单机同步写；无定期 compact。bbolt 会自动合并页，但无显式 `compact`/`DB.View` 批写优化。
- 涉及：`push.go` / `database/bbolt.go`。

### 1.1b MySQL 连接池全参 + 索引
🟡 部分实现：DSN 支持（`mysql.go:32` `NewMySQL`）。但仅透传 DSN，**未提供 MaxOpenConns/MaxIdleConns/LifeTime 等全参数**（需在 DSN 或代码中配置，`db.SetMaxOpenConns` 等均未调用）。索引未复核（表仅有 `key` 唯一索引，`mysql.go:28`）。

### 1.2a APNs 连接池动态伸缩/健康检测
🟡 部分实现：客户端池由 `--max-apns-client-count` 定长（`apns.go` `clients chan`）。⬜ 动态伸缩、健康检测、失效自动重建（当前池满即阻塞等待，无淘汰重建）。

### 1.2b 批量推送合并/异步队列削峰
🟡 部分实现：批量推送并发 goroutine（`route_push.go:147-175`）、`--max-batch-push-count` 限制（`main.go:320`）。⬜ 无消息合并（APNs 一次性多设备）、无异步队列削峰/背压。

### 1.3a Gotify 订阅者上限/消息背压
🟡 部分实现：消息条数上限（`store.go` max，新增滚动删除）+ WebSocket 读限（`route_gotify.go:153`）。⬜ 订阅者数量上限、生产者背压。

### 1.3b 历史消息自动清理（时间维度）
⬜ 未实现。现仅按条数上限裁剪，无"按天保留/定期清理"。

## 2、运维与可观测性优化

### 2.1 YAML 配置 + 热加载
⬜ 未实现。当前仅 urfave/cli 参数 + 环境变量（`main.go` getAppFlags）。

### 2.2 Prometheus / JSON 日志 / 分级
✅ 已实现：
- **分级与 JSON 日志**：新增 `--log-level`（`debug|info|warn|error`）与 `--log-format`（`console|json`），接入底层 `mritd/logger`（zap）原生能力；解析逻辑独立成 `internal/logging/` 可单测。
- **Prometheus 指标**：新增 `internal/metrics/`（`GET /metrics`），基于 `prometheus/client_golang`，统计 HTTP 请求（method×status 分类）、活跃 `/stream` WebSocket 连接数、Go/进程运行时指标。请求计数经 fiber 中间件全量埋点。

### 2.3 深度健康检查 / 备份 / 迁移
🟡 部分实现：`/healthz`（`route_misc.go:28`）仅返回字符串，无 DB/APNs/Gotify 深度检测；`/ping`、`/info`、`/version` 已有。
⬜ Bbolt 备份工具、Bbolt↔MySQL 迁移脚本未包含。

## 3、代码与工程质量优化

### 3.1 Go 版本与依赖
🟡 已达标：`go.mod` 已为 `go 1.25.5`（超过建议 1.21+），依赖为 2025/2026 版本。建议中"升级至 1.21+"在实际已满足；但仍建议定期核对上游 bark-server 上游安全修复（本项目为独立 fork，不自动同步，见 `DIFFERENCES.md`）。

### 3.2 测试补充
🟡 已补充：新增 `apns/apns_test.go`（`IsEmptyAlert`/`IsDelete` 纯逻辑表驱动）与 `database/database_test.go`（`bbolt`/`membase`/`envbase` CRUD 与错误路径，bbolt 用临时目录）。连同已有 `gotifycompat`、`ratelimit`，可直接运行的测试覆盖正常路径 + 边界 + 部分错误路径。`package main` 的推送/路由测试仍受 `push_test.go` deviceToken 门槛（见 AGENTS.md）；MySQL 各分支因需真实 DSN 未覆盖。

### 3.3 统一错误码
🟡 部分实现：已有统一 `try/fail` 结构 `CommonResp`（`router.go:97-124`），但错误码语义（HTTP/APNs codce 混合）未全局定义枚举。

### 3.4 接口版本治理
⬜ 未实现。上游已有 `/push` V2，但无全局前缀+版本号机制（现有 `/register`、`/mcp`、gotify 路径为自定义）。

## 4、安全性专项核对

### 2.1 现有安全机制盘点（确认）
- ✅ 可选全局 Basic Auth：`route_auth.go`，`authFreeRouters` 白名单。
- ✅ Gotify 接口独立 Client Token：`gotifyToken`（`route_gotify.go:33`）+ 恒定时间比较、哈希持久化（`service.go`、`store.go`）。
- ✅ Device Key 凭证：`push` 用 `device_key` 定位 token。
- ✅ 服务端 TLS（`--cert`/`--key`，`main.go:266`）。
- ✅ MySQL TLS（`--mysql-tls` 全套参数，`main.go:231`）。

### 2.2 核心安全风险点——逐条核对并纠偏

1. 【⚡重点】**默认无鉴权**：属实。`routerAuth` 在 user/pass 都为空时直接跳过（`route_auth.go:33`）。`/push`、`/register`、`/mcp*`、`/version` 默认均开放。
2. 【⚡重点】**无频率限制**：确认无全局限流中间件，`/push`、`/register` 均无限流；Basic Auth 无锁封/失败次数限制。
3. 【⚡重点】**Device Key 泄露止损难**：确认无 HTTP 拉黑/禁用接口、无推送频率阈值；`DeleteDeviceByKey` 无 HTTP 暴露。
4. 【🟠中危】**Gotify 流信息泄露**：`/stream` 无订阅数上限（见 1.3a）；`gotify.db` 明文、无加密存储（`route_gotify_msg` / `store.go` 明文 JSON），`volatile client token` 说明见 Notes。
5. 【🟠中危】**MCP 鉴权不明确**：`/mcp*`（`route_mcp.go`）**无独立鉴权**，也未挂到 Basic Auth 白名单之外（若开了 Basic Auth 其实会被拒绝，但误读者的默认部署无 Basic Auth 时即裸奔）。需在文档明确。
6. 【🟠中危】**Bbolt 明文/无内容加密**：确认无加密，依赖文件系统权限（文件 `0600`，`store.go:55`）。
7. 【🟠中危】**缺少 DDos/CC 防护**：确认无 IP 维度限流。
8. 【🟠中危】**容器以 root 运行**：确认 `Dockerfile`（`deploy/Dockerfile`）最终阶段 `FROM alpine` 未 `USER` 降权，默认以 root 运行。

### 2.3 安全加固建议（对应修订后状态）
- ✅ 文档中附加公网部署提示：未做，建议补齐（README 一键）。→ 已补齐 `README.md`「安全建议（公网部署必备）」小节。
- ⬜ MCP 复用 Basic Auth：现状**未强制**，建议在文档/实现中明确或强制。
- ⬜ API Key 模式、多密钥管理：未实现。
- ⬜ 单 Device 频率限制、设备拉黑/禁用接口：未实现。
- ⬜ 内存/存储加密、消息保留时间清理：未实现。
- ⬜ 强制 HTTPS 建议 + 反代最佳实践（帮反向代理配置）：文档未提。
- ✅ 全局限流中间件、Basic Auth 失败封禁、IP 白名单：未实现。→ 已加 IP 限流（`internal/ratelimit/`，`--rate-limit-ip` 作用于 `/register` `/mcp*`，`--rate-limit-push` 可覆盖推送端点；Basic Auth 失败封禁、IP 白名单仍未实现）。
- ✅ 镜像非 root + Alpine 更新机制：未实现。→ 镜像已非 root（`app` uid 1000，见 `deploy/Dockerfile`）；Alpine 更新机制未实现。

> 已落地项（具体见 `docs/DIFFERENCES.md` 与 README「安全建议」）：
> - IP 限流：`--rate-limit-ip` / `--rate-limit-burst` / `--rate-limit-push`（`internal/ratelimit/`、`route_rate_limit.go`）。
> - 镜像非 root：`app` 用户 + `USER app` + `/data` chown；同时修复了 entrypoint 改 `/etc/localtime` 在 `set -e` 下导致容器启动失败的问题。
> - 无鉴权醒目警告：`route_auth.go` 未配置 Basic Auth 时打印多行 WARN 横幅。
> - Helm PVC 持久化 + MySQL 凭据走 Secret（见 5.1/5.2）。

## 5、补充发现（原建议文档未覆盖）

> 本节为深入核查中额外发现的优化点，原建议文档未提及。

### 5.1 Helm 部署数据不持久化（重点）
✅ 已落地：`deploy/helm-chart/` 新增 **PVC** + `volumeMounts`（`pvc.yaml`、`deployment.yaml`），由 `values.yaml` 的 `persistence.enabled` / `storageClass` / `size` 控制，挂载 `/data`（bbolt + gotify.db）。

### 5.2 Helm 注入 MySQL 明文密钥（重点）
✅ 已落地：`deployment.yaml:55` 的 `-dsn=...` 明文参数已移除，改为 `mysql-secret.yaml`（stringData）+ env `BARK_SERVER_DSN` 引用 Secret。

### 5.3 Gotify 消息上限不可配置
✅ 已落地：新增 `--gotify-max-messages` / `BARK_SERVER_GOTIFY_MAX_MESSAGES`，`initGotifyCompat`（`main.go:66`）现已把该值传入 `gotifycompat.Config.MaxMessages`，默认 `0` 用内置 `1000`。

### 5.4 `/info` 暴露设备总数
✅ 已解决：`/info` 加入 Basic Auth 白名单（无凭据可访问，返回版本/构建信息），设备总数仅当请求携带**有效 Basic Auth 凭据**时才返回，避免对匿名请求泄露部署规模。

### 5.5 环境变量命名不一致
✅ 已解决：核对后确认 `main.go` 实际 EnvVar 为 `--addr` → `BARK_SERVER_ADDRESS`、`--data` → `BARK_SERVER_DATA_DIR`，与其它 flag 的 EnvVar 风格一致（Flag 名 ≠ EnvVar 全对应，属正常）。原 review 声称「README/AGENTS 惯用 `BARK_SERVER_ADDR`」实不成立——仓库从未使用过该名。已按实据在 README 参数表补齐 `BARK_SERVER_ADDRESS` / `BARK_SERVER_DATA_DIR`，代码未动。

### 5.6 APNs 投递过期与 `ttl` 各成体系
`apns/apns.go:144` 固定 `Expiration = now+24h`；`ttl`（`route_push.go`）仅作用于**归档消息**保留，不参与 APNs 投递过期。如希望 ttl 控制投递时效需另做映射，当前语义不互通，建议在文档澄清。

### 5.7 请求日志未脱敏 `devicetoken` query
`router.go` 的 `redactingWriter` 只掩码 `?token=`；`/register?devicetoken=...` 里的设备令牌仍以明文进日志（与 AGENTS 记录的「其它 query 保持原样」一致）。属已知权衡；如需更严格可一并掩码 `devicetoken`/`device_key`。

> **分析（2026-08）**：Fiber logger `${url}` 标签映射 `c.OriginalURL()`（含 query string，见 `logger/tags.go` 的 `TagURL`），而 `redactingWriter`（`router.go:74`）的 `tokenParamRe` 仅匹配 `([?&]token=)[^&\s]*`。故 `route_register.go` 的旧版 GET 注册 `GET /register?devicetoken=<APNs token>&key=<device_key>` 会把**持久凭证**（device_token / device_key，与 client token 同级敏感）明文写入访问日志，而日志常被采集到集中存储/第三方，泄露面大于进程内。
>
> **触发面窄**：仅旧版 **GET** `/register?devicetoken=` 走 query；新版 POST `/register` 用 JSON body（body 不记录，已防护），因此只有老客户端注册会中招。
>
> **结论**：低必要性、低成本的加固。倾向把 `tokenParamRe` 扩展为一组 `(?i)` query 参数（`token|devicetoken|device_token|device_key|key`）一次覆盖；或保守只加 `devicetoken`、不碰 `key`；亦可维持现状并在文档记录为已知权衡。**尚未实施。**

### 5.8 多副本一致性（无锁/无共享态）
默认 bbolt 单文件 + 进程内 WebSocket hub 均**无跨副本一致性**；多实例部署时注册与 /stream 会分片。叠加 5.1 的无 PVC，多副本可靠性低，只适合单副本或 MySQL 场景。

> **分析（2026-08）**：确认现状仍成立，且 5.1 的 Helm PVC 落地后问题边界更清晰：
> - **bbolt（默认）**：`store.go` 用 `bolt.Open(path, 0600, ...)`（单进程独占文件锁）。即使挂共享 PVC（RWO 本就只允许单节点读写），bbolt 也**不支持多进程并发写**，多副本必然冲突。因此默认栈**只能单副本**。
> - **进程内 hub**：`hub.go` 的订阅表（`map[uint64]chan Message`）纯进程内存，/stream 扇出不跨副本；即使换 MySQL，`/stream` 仍按副本各自为政，某副本重启即断该副本的连接。
> - **MySQL 模式**：注册数据落 MySQL 可多副本共享，但 gotify 监控（`/stream` WEB 订阅、`gotify.db`）与推送链路仍进程内状态。
>
> **结论**：属架构级约束，非小改动可消解。多副本只适用于 MySQL 模式且接受 `/stream` 各副本独立（需上游负载均衡时自行固定副本）；默认 bbolt 明确**单副本**。多副本高可用需引入共享缓存/消息总线（Redis pub/sub → DB 推送），涉及范围大，列 P3。**尚未实施，文档约束已明确。**

## 结论（优先级建议）

| 优先级 | 建议项 | 理由 |
|---|---|---|
| P0（安全） | 明确并强制 MCP 鉴权；默认无鉴权加醒目警告；/register、/push 限流 | 直接面对公网风险 | ✅ /register、/mcp 限流 + 无鉴权警告已落地；MCP 强制鉴权未做 |
| P0 | Docker 镜像非 root、数据目录收紧 | 容器安全基线 | ✅ 已落地（`app` 非 root + `/data` chown） |
| P0 | Helm PVC 持久化 + MySQL 凭据走 secret（5.1/5.2） | K8s 数据不丢、避免明文密钥 | ✅ 已落地 |
| P1 | 单 Device 推送频率限制 + 拉黑/禁用接口 | 泄露止损 |
| P1 | 结构化 JSON 日志 / Prometheus 指标 | 可观测性 | ✅ 已落地（`--log-level`/`--log-format` + `/metrics`） |
| P1 | Gotify 消息上限可配置；统一环境变量命名（5.3/5.5） | 运维一致性 | ✅ 5.3（`--gotify-max-messages`）与 5.5（README 补齐实际 EnvVar）均已解决 |
| P2 | 消息按时间清理、订阅者上限 | 资源控制 |
| P2 | APNs 连接池健康检测、批量异步削峰 | 性能 |
| P3 | HMS 直连、YAML 配置、Bbolt↔MySQL 迁移 | 涉及架构/范围大 |

> 以上核对均为代码阅读结论，未做变更；如需对某条落地，请单独提出，按 TDD 方式实现并提交。