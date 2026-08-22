# AGENTS.md — hotify-bark-server

Bark 服务端（Finb/bark-server）的独立 fork：Go + Fiber v2 的 iOS (APNs) **和 HarmonyOS (华为 Push Kit)** 推送服务，扩展了 Gotify 兼容监控接口（推送也会进入监控流）和 MCP 接口。不向上游回同步；二进制/镜像/module 均已独立命名。改动清单见 `docs/DIFFERENCES.md`。

## Project
- 入口：根目录 `package main`（`main.go`），urfave/cli v2 定义参数（`BARK_SERVER_*` 环境变量），fiber.New 构建应用。
- 模块路径：`github.com/wallleap/hotify-bark-server`，go.mod `go 1.25.5`（README 里的 "Go 1.18+" 已过时，以 go.mod 为准）。
- 推送链路：HTTP → `routeDoPush`（JSON→V2 / 其他→V1）→ `push()` → `db.DeviceInfoByKey` → 按 `platform` 分发 → `gotifyPublish`（监控流）→ `pushToAPNs` / `pushToHarmony`。HarmonyOS 推送需 `harmony/harmony_certs.go` 配置华为服务账号密钥。

## Commands
- 构建：`go build ./...`；本地二进制 `bin/build`（输出 `dist/hotify-bark-server`）；全平台交叉编译 `task`，单平台如 `task linux_amd64`。
- 运行：`go run . --data ./dev-data`（默认监听 `0.0.0.0:8080`，数据目录默认 `/data` 需可写）；docker compose：`bin/up` / `bin/down`（本地镜像）。
- 测试：`go test ./internal/gotifycompat/` ✅ 可直接跑；`go test ./harmony/...` ✅ 鸿蒙推送单元测试；`go test ./database/...` ✅ 数据库层测试；**`go test ./...` 会失败**——`push_test.go` 的 `TestMain` 要求手填有效 `deviceToken` 常量（当前为空会 panic）。
- 集成测试：`./scripts/test_integration.sh` 一键运行鸿蒙推送完整集成测试（需 Python3 + 已配置 `harmony_certs.go`）。
- Mock 服务器：`python3 scripts/mock_huawei.py 9999` 本地启动模拟华为推送服务。
- 静态检查：`go vet ./...` 当前干净（此前 `apns/apns.go:147` 非恒定格式串已修复）。
- 发布新版本：`bin/release` 无参自动递增 **MINOR**（基于最近创建的 tag，PATCH 归零，可跨 MAJOR，如 v0.2.0→v0.3.0）；`bin/release vX.Y.Z` 指定完整版本号（须符合语义化版本）；`--no-push` 只打 tag 不推送，`--dry-run` 预览。

## Architecture
- `apns/` — APNs 推送客户端：`apns.go`（PushMessage、Push、客户端池）、`apns_certs.go`（证书/JWT 鉴权）。全局客户端数由 `--max-apns-client-count` 控制。**`apns_certs.go` 内置 Bark 的 APNs p8 私钥**（`keyID`/`teamID` 常量 + 私钥）——"服务端代发"架构、上游设计，不是密钥泄漏，勿移除。
- `harmony/` — 华为 Push Kit 推送客户端：`harmony_certs.go`（服务账号密钥配置模板）、`token.go`（JWT PS256 签名、缓存、并发安全）、`client.go`（HTTP 调用、错误码处理、重试机制）。使用服务账号 JWT 鉴权方式，需用户自行填写华为开发者账号密钥。
- `database/` — `Database` 接口（`database.go`）+ 实现：`bbolt.go`（默认）、`mysql.go`（`--dsn`）、`membase.go` / `envbase.go`（测试 / serverless）。新增 `DeviceInfo` 结构支持 `Platform` 字段（`ios`/`harmony`）用于路由分发。
- `internal/gotifycompat/` — Gotify 兼容监控：`service.go`（Init/ValidateToken/Publish）、`store.go`（bbolt 持久化，不可用则内存降级）、`hub.go`（WebSocket 扇出）、`token.go`（token 生成/hash）。数据在 `<data>/gotify.db`。
- 根目录路由文件（`package main`，各自 `init()` 中 `registerRoute` 注册）：`route_push.go`（V1/V2、批量推送）、`route_register.go`、`route_gotify.go`（`/version`、`/message`、`/stream`）、`route_mcp.go`（`/mcp`、`/mcp/:device_key`）、`route_misc.go`、`route_auth.go`（可选 Basic Auth）、`route_rate_limit.go`（限流中间件与初始化）。
- 限流：`internal/ratelimit/`（token bucket，按 key 即 IP，并发安全）；`route_rate_limit.go` 的 `setupRateLimits(ip, burst, push)` 在 `runServer` 里从 flags 构建 `ipLimiter`。`/register`、`/mcp*` **始终限流**；推送端点 `/push`、`/:device_key` 默认不限流，仅当 `--rate-limit-push` 开启。**中间件必须按单路由挂（`route_rate_limit.go` 的 `rateLimitMiddleware` / `rateLimitPushMiddleware`），不能 group 级 `Use`**——所有路由共享一个 Fiber group，group Use 会把限流器泄漏到无关路径。
- 可观测性：`internal/metrics/`（Prometheus，`GET /metrics` 暴露 HTTP 请求指标 + 活跃 `/stream` 连接数 + Go/进程指标，`barkMetrics.Middleware()` 全量埋点）；`internal/logging/`（`--log-level`/`--log-format` 解析，接入 `mritd/logger`）。监控中间件可 group 级 `Use`（测量是全局预期，与限流不同）。
- 认证模型：`/push`、`/:device_key` 兼容推送、`/mcp*` **无独立认证**（device_key 即凭证）；`/message`、`/stream` 用 gotify client token（恒定时间比较，`/version` 无需认证）；Basic Auth 开启时白名单 `authFreeRouters`（`route_auth.go` 包级变量：`/ping /register /healthz /version /message /stream`）经 `isAuthFreePath` 按**精确路径/子路径**匹配放行——勿改回裸前缀匹配，否则 `/messageevil` 类路径会被放行（曾为此出过 auth bypass）。
- `router.go` — 路由注册表（`registerRoute` / `registerRouteWithWeight`，按 weight 降序）+ 通用响应 `CommonResp`（`success()` / `failed()` / `data()`）+ fiber logger/recover 中间件。
- `deploy/helm-chart/` — Kubernetes 部署：PVC 持久化 `/data`（bbolt + gotify.db，`persistence` 值控制）；MySQL DSN 经 Secret 注入 `BARK_SERVER_DSN`（`mysql-secret.yaml`），不以明文 args 传递。

## Conventions
- 新增路由：在根目录新建 `route_*.go`，`init()` 里调 `registerRoute(name, func(router fiber.Router){...})`；带权重的用 `registerRouteWithWeight`（0–100，名字不区分大小写且不可重复）。
- 响应统一用 `CommonResp` 助手（`success()` / `failed(code, msg, ...)` / `data(v)`），失败时 `c.Status(code).JSON(...)` 返回。
- 日志用 `github.com/mritd/logger`（`logger.Infof/Errorf/...`），不要用标准库 log。
- JSON 序列化统一 jsoniter（fiber `JSONEncoder` 已配置）。
- CLI 参数风格：urfave/cli 的 `StringFlag/BoolFlag/IntFlag`，`EnvVars: []string{"BARK_SERVER_*"}`，大小写转换参数名。
- Docker 运行用户是 `app`（uid 1000，非 root），`/etc` 运行时不可写；**entrypoint 不要做改 `/etc/localtime` 之类的运行时写操作**（曾在 `set -e` 下 `ln -sf` 因 target 已存在而 `File exists` 导致容器启动失败退出 1），时区在 Dockerfile 构建期烘焙、`BARK_SERVER_DATA_DIR=/data` 且 `/data` 已 chown 给 `app`。
- gotifycompat 的降级原则：存储不可用 → 内存降级，日志记录，**绝不致命**（参考 `service.go` Init）。
- 设备注册支持 `platform` 字段（`ios` 或 `harmony`），默认 `ios`；推送时根据存储的 platform 自动选择通道，也可在推送请求体中通过 `platform` 字段临时覆盖。
- `harmony/harmony_certs.go` 是华为 Push Kit 凭证配置文件，包含 `keyID`、`subAccount`、`projectID`、`privateKey` 四个变量；**禁止将真实凭证提交到公开仓库**，文件默认是占位符会导致启动失败。
- Bark level → 华为 click_action 映射：默认（不指定 level）→ `launch`（全屏通知），`critical`/`timeSensitive` → `launch`，`active` → `banner`（横幅通知），其他值（如 `passive`）→ `page`（普通通知）。

## Testing
- **先写测试用例，再实现功能**（TDD）：新功能或修复先补失败用例，实现到变绿再收工；不要"先实现后补测"。
- 用例要全面：正常路径 + 边界（空值、上限、非法输入）+ 错误/降级路径（存储不可用、token 无效、APNs/华为错误码等），参考 `internal/gotifycompat/gotify_test.go` 的覆盖风格。
- 包选择约束：`package main` 的测试受 `push_test.go` `TestMain` 的 `deviceToken` 门槛约束（未填会 panic，见 Commands）——**可测逻辑优先放 `internal/` 包，或提取为纯函数**（如 `route_auth.go` 的 `isAuthFreePath`），保证测试可直接运行。
- 运行：`go test ./harmony/...`（鸿蒙推送单元测试）、`go test ./database/...`（数据库层测试）、`go test ./internal/gotifycompat/`（可直接跑）；package main 测试需先填 `push_test.go` 的 `deviceToken` 常量。
- 集成测试：`./scripts/test_integration.sh` 可一键运行完整的鸿蒙推送端到端测试（注册→推送→监控流验证），使用 `scripts/mock_huawei.py` 模拟华为服务端。

## Constraints
- **不自动提交**：除非用户明确说"提交/commit"，一律只改文件不执行 `git commit`（包括 `--amend`）；改动完成后口头汇报，等用户指示再提交。
- **提交前门禁**：`go build ./...` + `go vet ./...` + `go test ./internal/gotifycompat/` 必须全绿（package main 测试受 `deviceToken` 门槛限制，见 Commands）。
- **提交信息**用 conventional commits（`feat:`/`fix:`/`docs:`/`chore:`/`build:`，可带 scope，如 `chore(deps):`）——与仓库现有历史保持一致。
- **语义化版本**：`v*` 开头的 git tag 用于触发 CI 构建推送（见 `.github/workflows/ci.yaml`），必须遵循 [Semantic Versioning](https://semver.org/)（`vMAJOR.MINOR.PATCH`，可带 `-prerelease`/`+build`，如 `v1.2.3`、`v2.0.0-rc.1`）；不要用前缀为 `v` 但非法版本的 tag（如 `v1`、`vnext`）触发构建。
- **错误处理**：一律 wrap 后向上传播（`fmt.Errorf("...: %w", err)`），不吞错；对外错误信息用 `failed(code, msg, ...)` 统一返回。
- **敏感信息**：client token、密码、device_token 默认不写日志、不进提交；token 的"首次启动打印一次"是刻意设计（见 Notes），其余场景不打印。
- **日志脱敏范围**：访问日志**不记录请求体**（无 `${body}`——推送正文与 `device_token`/`device_key` 不进日志，审计走 gotify 历史 `/message`）；query 中仅 client token 脱敏（`?token=<值>` → `?token=***`，实现于 router.go 的 `redactingWriter`/`tokenParamRe`）；其它 query 参数（如 `GET /register?devicetoken=`）保持原样。
- **文档同步**：新接口/新功能同步更新 `docs/`（README 文档列表里的对应文档）与 `docs/DIFFERENCES.md`（相对上游的改动清单）。
- **改动聚焦**：一次改动解决一个问题，不顺手重构无关代码；新逻辑优先放 `internal/` 包（可测性，见 Testing）。
- **分步提交**：一次提交只含一个逻辑单元（按步骤/功能拆分 commit），不把无关改动塞进同一 commit；每步提交后仓库保持可构建（`go build ./...` 通过）。

## Notes
- gotify client token 打印策略：未预置时首次启动打印一次；重启后不打印（防日志泄漏），日志提示持久化位置；数据目录不可用降级内存存储时每次启动重新生成并打印。
- `docs/TOKENS.md` 说明 device_token / device_key / client token 三者区别与生成方式。
- 安全部署：默认无鉴权（`/push`、`/register`、`/mcp*`、`/:device_key` 对网络开放）；公网部署建议开启 Basic Auth（`BARK_SERVER_BASIC_AUTH_USER/PASSWORD`）与限流（`BARK_SERVER_RATE_LIMIT_IP` 等），详见 README「安全建议」小节。未配置 Basic Auth 时 `route_auth.go` 的 `routerAuth` 打印醒目的多行 WARN 横幅，这是刻意的提示，勿降级为普通日志。
