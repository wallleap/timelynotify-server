# device_token、device_key 与 client token 说明

本服务端涉及四种"凭证"，分别服务于不同的链路，容易混淆，这里统一说明它们是什么、由谁生成、用在哪里。

## 1. device_token（iOS 设备令牌）

- **是什么**：Apple APNs 分配给**每一台 iOS 设备 + 每一个 App** 的唯一推送地址。Bark App 注册 APNs 成功后，系统会返回一段 64 字节的十六进制字符串（约 128 个字符），这就是 device_token。
- **谁生成**：**iOS 系统**（通过 APNs 注册流程），服务端**不生成**、也无法伪造。
- **用在哪**：Bark App 启动后调用 `POST /register`（或兼容的 `GET /register?devicetoken=...`）把 device_token 上报给服务端；服务端存储它，推送时把它作为 APNs 投递目标。
- **怎么查看**：在 Bark App 内查看（通常位于"设置 → 更多/设备信息"，或 App 展示的推送 URL 里）。
- **注意**：device_token 会因重装 App、恢复系统等原因失效。服务端在推送返回 `410 BadDeviceToken` 时会自动清除该设备的记录，需要重新注册。

## 2. harmony_token（鸿蒙设备令牌）

- **是什么**：华为 Push Kit 分配给**每一台 HarmonyOS 设备 + 每一个 App** 的唯一推送地址。鸿蒙应用集成 Push Kit 后，系统会返回一段字符串作为推送 token。
- **谁生成**：**HarmonyOS 系统**（通过华为 Push Kit 注册流程），服务端**不生成**。
- **用在哪**：鸿蒙 App 启动后调用 `POST /register` 把 `device_token` 和 `platform: harmony` 上报给服务端；服务端根据 platform 字段自动选择华为通道推送。
- **注册方式**：

```sh
# 注册鸿蒙设备
curl -X POST http://<host>:18080/register \
     -H 'Content-Type: application/json' \
     -d '{"device_key":"my-harmony-device","device_token":"<harmony_push_token>","platform":"harmony"}'
```

- **注意**：harmony_token 失效时，服务端会清除该设备的记录，需要重新注册。

## 3. device_key（设备 key，即 Bark URL 里的那段）

- **是什么**：设备的**短标识**，注册时由**用户自定义**或由**服务端生成**。推送时用它定位 device_token，不需要在每次推送时携带完整的 token。
- **谁生成**：
  - 用户注册时**指定** `key`（如 `mydemo`）：服务端原样保存。
  - 用户**不指定**：服务端用 `shortuuid` 生成一个随机 key（`database/bbolt.go`、`database/mysql.go` 中的 `SaveDeviceTokenByKey`）。
- **用在哪**：推送 URL 的路径段与 `device_key` 参数，例如 `GET /mydemo/标题/内容` 或 `POST /push` 的 JSON 里 `"device_key": "mydemo"`；也是旧版兼容推送路由 `/:device_key` 的凭证。注册成功响应会返回 `key`、`device_key` 和 `platform`（新增）字段。
- **多平台共存**：**同一个 `device_key` 可以同时绑定 iOS 与鸿蒙设备**——iPhone 用 `platform=ios` 注册、鸿蒙手机用 `platform=harmony` 注册到同一个 key，数据库按 `(key, platform)` 唯一约束并存。推送时服务端**默认扇出到该 key 下所有有效平台**（iPhone 和鸿蒙手机同时收到），任一平台投递成功即返回 200；若只想推某一端，在推送请求体带 `"platform": "ios"` 或 `"harmony"` **收窄**到指定平台。注意：失效 token 清理是按平台定向的（`ClearDeviceTokenByKeyAndPlatform`），不会因为一端失效而连累另一端。
- **⚠️ 安全提示**：**device_key 就是推送凭证**——任何人拿到你的 Bark URL（含 key）就能向你的设备推送。不要在公开渠道晒 Bark 推送 URL。它是"设备级"凭证，与 client token 无关。

## 4. client token（gotify 兼容接口令牌）

- **是什么**：访问 Gotify 兼容监控接口（设备级 `/:device_key/message`、`/:device_key/stream`）的令牌。所有推送（包括 iOS 和 HarmonyOS）都会进入这个监控流。
- **谁生成**：两种方式：
  1. **推荐——预置**：启动时设置环境变量 `BARK_SERVER_GOTIFY_CLIENT_TOKEN` 或参数 `--gotify-client-token`。服务端**只保存 SHA-256 哈希**，不落明文；token 本身由你指定（比如用 `openssl rand -base64 32` 生成）。
  2. **自动生成**：未设置时，服务端用 `crypto/rand` 生成 32 字节随机数并做 base64url 编码（43 字符），**首次启动时在日志打印一次**（`internal/gotifycompat/token.go`、`service.go`）。明文会以 0600 权限存进 `<data>/gotify.db` 以保持重启稳定。
- **用在哪**：hotify-bridge 的 `bridge_config.yaml` 里 `gotify_token`（或环境变量 `GOTIFY_CLIENT_TOKEN`）。
- **重启后怎么拿回自动生成的 token**：出于防日志泄漏考虑，自动生成的 token **只在生成那一刻打印一次**，重启后不再打印。此时日志会提示 token 已持久化在 `<data>/gotify.db`。若丢失，二选一：
  - 用环境变量/参数**固定**一个 token（推荐）；
  - 停止服务后**删除 `<data>/gotify.db`** 再启动，重新生成（会同时清空监控消息历史）。
  - 例外：若数据目录不可用，服务退化为内存存储，每次启动都会**重新生成并打印**一次 token，但重启后即失效——这种部署应使用预置 token。
- **⚠️ 安全提示**：client token 能读取全部监控消息并订阅实时流，泄露后请用预置方式更换（operator token 生效时旧的明文记录会被自动清除）。

## 5. 华为 Push Kit 服务账号凭证

- **是什么**：服务端调用华为 Push Kit API 时使用的鉴权凭证。采用"服务账号 JWT"方式（HarmonyOS NEXT 推荐）。
- **发送所需四个字段**：`keyID`、`subAccount`、`projectID`、`privateKey`。如需使用鸿蒙通知删除（`delete`），还需配置应用级 `clientID`（普通发送不需要）。
- **谁生成**：**华为开发者联盟**控制台（`https://developer.huawei.com/consumer/cn/console/api/myApi`）。
- **配置方式**：将凭证填入 `harmony/harmony_certs.go` 文件。
- **⚠️ 安全提示**：**禁止将真实凭证提交到公开仓库**。`privateKey` 是 RSA 私钥，一旦泄露可被伪造 JWT 发送任意推送。

## 凭证与接口一览

| 凭证 | 用途 | 谁生成 | 认证哪些接口 |
| ----- | ----------- | ---- | ----------- |
| device_token | APNs 投递目标 | iOS 系统 | `/register` 上报，推送时由服务端内部使用 |
| harmony_token | 华为 Push Kit 投递目标 | HarmonyOS 系统 | `/register` 上报，推送时由服务端内部使用 |
| device_key | 定位设备、推送凭证 | 用户或服务端（shortuuid） | `/push`、`/:device_key` 兼容推送、`/mcp`、`/mcp/:device_key` |
| client token | 监控接口访问 | 用户预置或服务端自动生成 | 设备级 `/:device_key/message`、`/:device_key/stream`（`/:device_key/version` 无需认证） |
| 服务账号密钥 | 华为 API 鉴权 | 华为开发者联盟 | 服务端内部使用，用于签名 JWT |

补充说明（均属上游设计，非本 fork 引入）：

- **MCP 接口与 push 等价**，以 device_key 为凭证，无独立认证。开启 Basic Auth（`--user`/`--password`）后 `/mcp`、`/push` 会被保护（白名单只放行 `/ping`、`/register`、`/healthz`、`/info` + 设备级 `/:device_key/version`、`/:device_key/message`、`/:device_key/stream`；其中设备级监控接口走 gotify token 鉴权，`/info` 无凭据返回基础信息，带有效 Basic Auth 才返回设备数）。
- **Basic Auth 配置注意**：`--user` 有值而 `--password` 为空时，除白名单外所有请求都会被拒绝。
- 服务端内置了 Bark App 的 APNs p8 私钥（`apns/apns_certs.go`），这是"服务端代发"架构——任何运行本服务端的人都能以 Bark App 名义发推送，请只在你信任的主机上部署。
- 服务端鸿蒙推送使用的华为服务账号密钥需要用户自行配置（`harmony/harmony_certs.go`），不会随代码仓库分发。
