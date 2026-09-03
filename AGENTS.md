# AGENTS.md — timelynotify-server

Bark 服务端（Finb/bark-server）的独立 fork：Go + Fiber v2 的 iOS (APNs) **和 HarmonyOS (华为 Push Kit)** 推送服务，扩展了 Gotify 兼容监控接口（推送也会进入监控流）和 MCP 接口。不向上游回同步；二进制/镜像/module 均已独立命名。改动清单见 `docs/DIFFERENCES.md`。

## Project

- 入口：根目录 `package main`（`main.go`），urfave/cli v2 定义参数（`BARK_SERVER_*` 环境变量），fiber.New 构建应用。
- 模块路径：`github.com/wallleap/timelynotify-server`，go.mod `go 1.25.5`（README 里的 "Go 1.18+" 已过时，以 go.mod 为准）。
- 推送链路：HTTP → `routeDoPush`（JSON→V2 / 其他→V1）→ `push()` → `db.DevicesByKey`（返回该 key 下所有 `(key, platform)` 记录）→ 过滤空 token、按 `_platform` 显式收窄或全平台并发扇出 → `gotifyPublish`（监控流，单次）→ `pushToDevice` → `pushToAPNs` / `pushToHarmony`。单目标走原路径；多目标用 `sync.WaitGroup` 并发，**任一成功即 200**，全部失败才 500。

## Commands

- 构建：`go build ./...`；本地二进制 `bin/build`（输出 `dist/timelynotify-server`）；全平台交叉编译 `task`，单平台如 `task linux_amd64`。
- 运行：`go run . --data ./dev-data`（默认监听 `0.0.0.0:8080`，数据目录默认 `/data` 需可写）；docker compose：`bin/up` / `bin/down`（本地镜像）。
- 测试：`go test ./internal/gotifycompat/` ✅ 可直接跑；`go test ./harmony/...` ✅ 鸿蒙推送单元测试；`go test ./database/...` ✅ 数据库层测试；**`go test ./...` 会失败**——`push_test.go` 的 `TestMain` 要求手填有效 `deviceToken` 常量（当前为空会 panic）。
- 集成测试：`./scripts/test_integration.sh` 一键运行鸿蒙推送完整集成测试（需 Python3 + 已配置 `harmony_certs.go`）。
- Mock 服务器：`python3 scripts/mock_huawei.py 9999` 本地启动模拟华为推送服务。
- 静态检查：`go vet ./...` 当前干净（此前 `apns/apns.go:147` 非恒定格式串已修复）。
- 发布新版本：`bin/release` 无参自动递增 **MINOR**（基于最近创建的 tag，PATCH 归零，可跨 MAJOR，如 v0.2.0→v0.3.0）；`bin/release vX.Y.Z` 指定完整版本号（须符合语义化版本）；`--no-push` 只打 tag 不推送，`--dry-run` 预览。

## Architecture

- `apns/` — APNs 推送客户端：`apns.go`（PushMessage、Push、客户端池）、`apns_certs.go`（证书/JWT 鉴权）。全局客户端数由 `--max-apns-client-count` 控制。**`apns_certs.go` 内置 Bark 的 APNs p8 私钥**（`keyID`/`teamID` 常量 + 私钥）——"服务端代发"架构、上游设计，不是密钥泄漏，勿移除。
- `harmony/` — 华为 Push Kit 推送客户端：`harmony_certs.go`（真实鉴权配置，"服务端代发"架构、上游设计，不是密钥泄漏，勿移除。）、`token.go`（JWT PS256 签名、缓存、并发安全）、`client.go`（HTTP 调用、错误码处理、重试机制）。使用服务账号 JWT 鉴权方式，需用户自行填写华为开发者账号密钥。
- `database/` — `Database` 接口（`database.go`）+ 实现：`bbolt.go`（默认）、`mysql.go`（`--dsn`）、`membase.go` / `envbase.go`（测试 / serverless）。新增 `DeviceInfo` 结构支持 `Platform` 字段（`ios`/`harmony`）用于路由分发。
- `internal/gotifycompat/` — Gotify 兼容监控：`service.go`（Init/ValidateToken/Publish）、`store.go`（bbolt 持久化，不可用则内存降级）、`hub.go`（WebSocket 扇出）、`token.go`（token 生成/hash）。数据在 `<data>/gotify.db`。
- 根目录路由文件（`package main`，各自 `init()` 中 `registerRoute` 注册）：`route_push.go`（V1/V2、批量推送）、`route_register.go`、`route_gotify.go`（设备级 `/<device_key>/version`、`/<device_key>/message`、`/<device_key>/stream`）、`route_mcp.go`（`/mcp`、`/mcp/:device_key`）、`route_misc.go`、`route_auth.go`（可选 Basic Auth）、`route_rate_limit.go`（限流中间件与初始化）。
- 限流：`internal/ratelimit/`（token bucket，按 key 即 IP，并发安全）；`route_rate_limit.go` 的 `setupRateLimits(ip, burst, push)` 在 `runServer` 里从 flags 构建 `ipLimiter`。`/register`、`/mcp*` **始终限流**；推送端点 `/push`、`/:device_key` 默认不限流，仅当 `--rate-limit-push` 开启。**中间件必须按单路由挂（`route_rate_limit.go` 的 `rateLimitMiddleware` / `rateLimitPushMiddleware`），不能 group 级 `Use`**——所有路由共享一个 Fiber group，group Use 会把限流器泄漏到无关路径。
- 可观测性：`internal/metrics/`（Prometheus，`GET /metrics` 暴露 HTTP 请求指标 + 活跃 `/stream` 连接数 + Go/进程指标，`tnMetrics.Middleware()` 全量埋点）；`internal/logging/`（`--log-level`/`--log-format` 解析，接入 `mritd/logger`；暴露 `MaskMiddle`/`MaskSensitiveFields` 脱敏工具）。日志时间格式 `2006-01-02 15:04:05`（mritd/logger 默认 `TimeEncodingDefault` 与 fiberlogger `TimeFormat` 一致，无需配置）。trace id 经 fiber `middleware/requestid`（`routerSetupCommon` group-level Use，先于 fiberlogger）注入 `c.Locals("requestid")`，业务代码用 `ridFrom(c)` 取值；MCP 路径经 `adaptor.HTTPHandlerFunc` 包装层把 rid 注入 `r.Context()`（`ridCtxKey`）后由 `notifyHandler` 读取。监控中间件可 group 级 `Use`（测量是全局预期，与限流不同）。
- 认证模型：`/push`、`/:device_key` 兼容推送、`/mcp*` **无独立认证**（device_key 即凭证）；设备级 `/:device_key/message`、`/:device_key/stream` 用 gotify client token（恒定时间比较）；设备级 `/:device_key/version` 无需认证；Basic Auth 开启时白名单经 `isAuthFreePath` 按**精确路径/子路径**匹配放行——勿改回裸前缀匹配，否则 `/messageevil` 类路径会被放行（曾为此出过 auth bypass）。全局 gotify 接口（`/version`、`/message`、`/stream`）已移除，仅保留设备级路径。
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
- gotify 监控流 id 覆盖：`gotifyPublish` 把 `msg.ExtParams`（含 `id`）原样放入 `extras`；`Service.Publish` 检测到 `extras.id` 非空且 `device_key` 非空时调 `store.UpsertByExtraID(device, extraID, &m)`，在 `messages` bucket 倒序扫描匹配 `SourceDevice()==device && extras.id==extraID` 的消息，找到则原地覆盖内容（保留 bbolt ID，不新增计数），找不到则新建；无 `id` 或无 `device_key` 走 `store.Add`（追加新消息）。同一 `extras.id` 在不同 `device_key` 下互不影响。内存降级存储同样实现 `UpsertByExtraID`。
- 设备注册支持 `platform` 字段（`ios` 或 `harmony`），默认 `ios`。**同一 `device_key` 可同时绑定 iOS 与鸿蒙两条记录**（数据库按 `(key, platform)` 唯一约束，见 `database/database.go`）；推送时默认**扇出到该 key 下所有有效平台**，任一成功即返回 200。推送请求体可带 `platform` 字段**收窄**到指定平台（仅推该平台记录，不再覆盖到无关平台）。失效 token 清理用 `ClearDeviceTokenByKeyAndPlatform`（按平台定向），**不可**用 `SaveDeviceTokenByKey(key, "")`（默认 `ios`，会跨平台误清）。
- `harmony/harmony_certs.go` 是华为 Push Kit 凭证配置文件，包含 `keyID`、`subAccount`、`projectID`、`privateKey` 四个变量，**和 `apns_certs.go` 一样是真实的凭证**。
- 华为 Push Kit V3 场景化消息：请求体为 `{payload:{notification},target:{token},pushOptions}`，HTTP 头必带 `push-type:0`（Alert）。`notification.clickAction` 是对象 `{actionType:0|1}`（0=进首页、1=进内页），**不是** V1 的 `click_action` 字符串（`launch`/`banner`/`page`）。`category` 默认 `SUBSCRIPTION`（订阅类，服务通讯）、`foregroundShow` 默认 `true`、`pushOptions.ttl` 默认 86400。`notification.sound` 映射 Bark `sound` 参数：裸铃声名自动补 `.mp3`（iOS 侧补 `.caf`，互不影响）、`.caf` 自动转 `.mp3`、已带 `.mp3`/`.wav`/`.mpeg` 后缀保持不变，归一化在 `harmony/client.go` 的 `normalizeSoundName`；`notification.soundDuration` 映射 `soundDuration` 参数（秒，仅与 `sound` 同传才生效，`clampSoundDuration` 截断到 `[1,60]`，非正数省略该字段）。`notification.inboxContent` 映射 Bark `inboxContent` 参数（`string[]`，多行消息），`push()` 的 `toInboxStrings` 把 V2 JSON 数组、V1 JSON 编码字符串、裸字符串统一归一化为 `[]string`；有该字段时 `Send` 自动同时设置 `notification.style=3`（收件箱样式），空数组省略两者。`notification.notifyId` 映射 Bark `id` 参数（int，范围 `[0, 2147483647]`，相同 `notifyId` 的通知互相覆盖），`pushToHarmony` 用 `toInt` 把 `msg.Id`（string）解析为 int，非数字或空值得 0 由 `omitempty` 省略（Push Kit 自动生成标识）。**JSON 数字参数归一化**：V2 JSON 数字解码为 `float64`，`fmt.Sprint(float64(2147483647))` 会产出科学计数法 `"2.147483647e+09"`（旧 `toInt` 用 `fmt.Sscanf("%d")` 会静默截断成 2 且不报错）——因此 `push()` 的 id/badge/soundDuration/foregroundShow 归一化一律走 `numberToString`（float64 整数→`strconv.FormatInt`，不产出指数），`toInt` 用 `strconv.Atoi` 严格解析（非法输入报错而非吞前缀数字）；改数字参数处理时勿退回 `fmt.Sprint`。**`SUBSCRIPTION` 等服务通讯类 category 须先在 AGC 申请「通知消息自分类权益」并通过审核**，否则消息会被华为降级为 `MARKETING`（资讯营销类），受每设备每日 2/5 条频控且自定义铃声失效；若暂未申请权益，临时把 `harmony/client.go` 的 `defaultCategory` 改回 `MARKETING` 即可零门槛发送。Bark `level` 字段是 APNs 概念，V3 无直接对应，统一用 `actionType=0`（点击进应用首页）；V3 通知展示样式由系统按 `category` 与前台状态决定，不再有 V1 的 launch/banner/page 之分。
- 华为**消息撤回**（push revokes）：推送请求带真值 `revoke` 参数时走撤回分支，与发送链路差异如下——**端点是 v1** `https://push-api.cloud.huawei.com/v1/{clientId}/messages:revoke`（**不是**发送的 v3 projectId），URL 里的**应用级** Client ID 存在 `harmony/harmony_certs.go` 的 `clientID` 变量（AGC「项目设置→常规→应用信息→OAuth 2.0客户端ID(凭据)-Client ID」，值=APP ID；**不是** `agconnect-services.json` 顶层 `client.client_id`——那是项目级 Client ID，填错报 80300002 "No permission to send message to these tmIDs"；留空不影响发送，撤回快速报错）；**请求体是扁平结构** `revokeMessage{notifyId int, token []string}`（`harmony/client.go`），**不是** v3 的嵌套 payload/target；头同样是 Bearer JWT + `push-type:0`，成功码同为 80000000、80200003 过期重试一次（`withRetry`/`postOnce` 为 send/revoke 共用）。入口侧：`push()` 把 `revoke` 标志（V1 query/form 字符串、V2 JSON bool/数字）经 `isTruthyFlag`（纯函数，可测）归一化为内部 `ExtParams["_revoke"]` 标记（原始键删除，避免透传 APNs 载荷），在 device_key 校验后、`gotifyPublish` 前**短路**进 `pushRevoke()`——**仅鸿蒙**（iOS 无远程撤回 API，无鸿蒙目标返回 400）、`id` 必须解析为正整数 notifyId（否则 400 且不调撤回）、**忽略其它所有推送参数**、**不写 gotify 监控流**；80200001/80300007 按 `ClearDeviceTokenByKeyAndPlatform(key,"harmony")` 清死 token。撤回 seam 变量 `revokeHarmony`（与 `pushHarmony`/`pushAPNs` 同级）供测试替换；MCP 入口因共用 `push()` 自动支持。**端侧生效差异（实测）**：撤回返回 80000000 只表示 Push 服务端受理，已展示通知的移除需真机 HarmonyOS NEXT（Phone/Tablet/PC 5.1+）；**DevEco 模拟器不处理已展示消息的撤回指令**（API 返回成功但通知保留，未下发消息仍可拦下）——模拟器上"撤回成功通知还在"是平台限制不是 bug，端到端验证撤回效果必须用真机。

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
- **敏感信息**：client token、密码默认不写日志、不进提交；`device_key`/`device_token`/`client_token` 在业务日志中**中间脱敏后可写**（保留前 4 + `***` + 后 4，确保中间至少遮 4 个字符——长度 < 12 全 `***`，实现于 `internal/logging/sanitize.go` 的 `MaskMiddle`）。token 的"首次启动打印一次"是刻意设计（见 Notes），其余场景不打印。
- **日志脱敏范围**：访问日志**不记录原始请求体**（fiberlogger 无 `${body}`；推送正文与敏感字段不进访问日志）；业务日志（V1/V2 入口、push 失败、APNs/鸿蒙失败等）记录**按字段脱敏后的请求体**——敏感字段 `device_token`/`devicetoken`/`token`/`client_token` 中间打码（`MaskMiddle`），其他字段原样保留（`MaskSensitiveFields`）。query 中仅 client token 脱敏（`?token=<值>` → `?token=***`，实现于 router.go 的 `redactingWriter`/`tokenParamRe`）；其它 query 参数（如 `GET /register?devicetoken=`）保持原样。fiberlogger 访问日志新增 `${locals:requestid}` 字段，与业务日志的 `rid=` 前缀对齐做链路贯通。
- **文档同步**：新接口/新功能同步更新 `docs/`（README 文档列表里的对应文档）与 `docs/DIFFERENCES.md`（相对上游的改动清单）。
- **改动聚焦**：一次改动解决一个问题，不顺手重构无关代码；新逻辑优先放 `internal/` 包（可测性，见 Testing）。
- **分步提交**：一次提交只含一个逻辑单元（按步骤/功能拆分 commit），不把无关改动塞进同一 commit；使用中文提交信息，每步提交后仓库保持可构建（`go build ./...` 通过）。

## Notes

- gotify client token 打印策略：未预置时首次启动打印一次；重启后不打印（防日志泄漏），日志提示持久化位置；数据目录不可用降级内存存储时每次启动重新生成并打印。
- `docs/TOKENS.md` 说明 device_token / device_key / client token 三者区别与生成方式。
- 安全部署：默认无鉴权（`/push`、`/register`、`/mcp*`、`/:device_key` 对网络开放）；公网部署建议开启 Basic Auth（`BARK_SERVER_BASIC_AUTH_USER/PASSWORD`）与限流（`BARK_SERVER_RATE_LIMIT_IP` 等），详见 README「安全建议」小节。未配置 Basic Auth 时 `route_auth.go` 的 `routerAuth` 打印醒目的多行 WARN 横幅，这是刻意的提示，勿降级为普通日志。
- trace id（`rid`）贯通：业务日志统一 `rid=<uuid>` 前缀；fiberlogger 访问日志输出 `${locals:requestid}`。rid 来自 fiber `middleware/requestid`（默认 `utils.UUID`），客户端也可传 `X-Request-ID` header 覆盖。批量推送 goroutine 与多平台 fan-out goroutine 通过闭包捕获 rid，保证并发日志可关联。MCP 路径因 `adaptor.HTTPHandlerFunc` 不传递 fiber Locals，在 fiber 包装层注入 `r.Context()`（`ridCtxKey`）后由 `notifyHandler` 读取。

## TODO

- [ ] 支持用户级非对称密钥对，用于消息签名，鸿蒙客户端生成，注册时把公钥发送到服务器，由服务器存储公钥（加密消息并存数据库），用户存储私钥（不上传服务器，客户端解密消息）。
