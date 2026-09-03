# API

TimelyNotify Server HTTP API 参考。兼容上游 Bark V1（URL 路径参数推送）与 V2（JSON RESTful 推送）API，并扩展了 HarmonyOS 推送、设备注册、Gotify 兼容监控、MCP 等接口。

> 地址中的 `18080` 是容器对外映射端口（`-p 18080:8080`）。若直接运行二进制（默认监听 `0.0.0.0:8080`），请使用 `8080`。

## 目录

- [通用响应格式](#通用响应格式)
- [认证](#认证)
- [限流](#限流)
- [推送](#推送)
  - [POST /push（V2 单设备推送）](#post-pushv2-单设备推送)
  - [POST /push（V2 批量推送）](#post-pushv2-批量推送)
  - [V1 兼容推送（路径参数）](#v1-兼容推送路径参数)
  - [Push 字段参考](#push-字段参考)
  - [多平台扇出](#多平台扇出)
  - [参数优先级](#参数优先级)
  - [HarmonyOS 推送](#harmonyos-推送)
- [设备注册](#设备注册)
  - [POST /register](#post-register)
  - [GET /register（兼容旧客户端）](#get-register兼容旧客户端)
  - [GET /register/:device\_key（校验存在）](#get-registerdevice_key校验存在)
- [Gotify 兼容监控（设备级）](#gotify-兼容监控设备级)
  - [GET /:device\_key/version](#get-device_keyversion)
  - [GET /:device\_key/message](#get-device_keymessage)
  - [DELETE /:device\_key/message](#delete-device_keymessage)
  - [DELETE /:device\_key/message/:id](#delete-device_keymessageid)
  - [GET /:device\_key/stream（WebSocket）](#get-device_keystreamwebsocket)
- [MCP 接口](#mcp-接口)
  - [ALL /mcp](#all-mcp)
  - [ALL /mcp/:device\_key](#all-mcpdevice_key)
- [杂项](#杂项)
  - [GET /](#get-)
  - [GET /ping](#get-ping)
  - [GET /healthz](#get-healthz)
  - [GET /version](#get-version)
  - [GET /info](#get-info)
  - [GET /metrics](#get-metrics)
- [多语言示例](#多语言示例)

***

## 通用响应格式

绝大多数接口统一返回 `CommonResp`：

| 字段        | 类型     | 必返 | 说明                                                                       |
| --------- | ------ | -- | ------------------------------------------------------------------------ |
| code      | int    | 是  | 业务码，成功 `200`，失败与 HTTP 状态码一致（`400`/`401`/`410`/`418`/`429`/`500`/`503` 等） |
| message   | string | 是  | `"success"` 或错误描述                                                        |
| data      | any    | 否  | 附加数据，失败或无数据时省略（`omitempty`）                                              |
| timestamp | int    | 是  | unix 秒级时间戳                                                               |

成功示例：

```json
{"code":200,"message":"success","timestamp":1700000000}
```

带 data 的成功示例（注册返回）：

```json
{
  "code": 200,
  "message": "success",
  "timestamp": 1700000000,
  "data": {
    "key": "ynJ5Ft4atkMkWeo2PAvFhF",
    "device_key": "ynJ5Ft4atkMkWeo2PAvFhF",
    "device_token": "<token>",
    "platform": "ios"
  }
}
```

失败示例：

```json
{"code":400,"message":"device key is empty","timestamp":1700000000}
```

> 例外：`GET /`、`GET /healthz` 返回纯文本 `"ok"`；`GET /:device_key/version` 返回 `{"version":"..."}`；`GET /info` 返回扁平 JSON；`GET /metrics` 返回 Prometheus 文本格式。

***

## 认证

服务有三层并存、互不覆盖的认证机制：

### 1. 无认证（默认）

`/push`、`/:device_key`（V1 兼容推送）、`/register`、`/mcp*` 默认不要求任何认证。`device_key` 本身就是凭证，需保证其私密性。未配置 Basic Auth 时，启动会在日志打印醒目 WARN 横幅提示。

### 2. Basic Auth（可选全局门禁）

通过 `--user`/`--password` 或 `BARK_SERVER_BASIC_AUTH_USER/PASSWORD` 开启。开启后，非白名单路径必须携带 `Authorization: Basic base64(user:password)`，否则返回 `418 I'm a teapot`。

**白名单路径**（豁免 Basic Auth）：

| 分类  | 路径                                                | 说明                                |
| --- | ------------------------------------------------- | --------------------------------- |
| 全局  | `/ping`、`/register`、`/healthz`、`/info`            | 探测/注册/基本信息                        |
| 设备级 | `/:device_key/version`                            | Gotify 设备探测                       |
| 设备级 | `/:device_key/message`、`/:device_key/message/:id` | Gotify 历史（仍需 client token）        |
| 设备级 | `/:device_key/stream`                             | Gotify WebSocket（仍需 client token） |
| 根   | `/`                                               | 中间件挂载于 `Use("/+")`，不匹配零段根路径       |

> 匹配按精确路径/子路径，禁止前缀模糊匹配（避免 `/messageevil` 类路径被放行）。

**非白名单路径**（开启 Basic Auth 后必须携带凭据）：`/push`、`/:device_key` 及其子路径（V1 兼容推送）、`/mcp`、`/mcp/:device_key`、`/version`、`/metrics`。

```sh
# user:password → base64 → 头值
curl -u admin:secret "http://127.0.0.1:18080/info"
curl -H "Authorization: Basic YWRtaW46c2VjcmV0" "http://127.0.0.1:18080/info"
```

### 3. Client Token（Gotify 设备级鉴权）

设备级 `/:device_key/message`、`/:device_key/stream` 必须携带 client token，否则返回 `401 unauthorized`。三种携带方式按优先级从高到低：

| 优先级   | 来源       | 示例                                    |
| ----- | -------- | ------------------------------------- |
| 1（最高） | query 参数 | `?token=<clientToken>`                |
| 2     | 请求头      | `X-Gotify-Key: <clientToken>`         |
| 3     | 请求头      | `Authorization: Bearer <clientToken>` |

同时携带时取第一个非空值。client token 生成方式见 [TOKENS.md](TOKENS.md)。

### 三者关系

- **Basic Auth 是包级"能否进入"门禁**（按白名单放行）。
- **Client Token 是接口内"你是谁"鉴权**（Basic Auth 放行后才校验）。
- `Authorization` 头只有一个，Basic 与 Bearer **不能同时放入**：带 `Authorization: Bearer <clientToken>` 会被 Basic 门禁直接拒绝（418）；过门禁请带 `Authorization: Basic <...>`，接口内鉴权改用 `?token=` 或 `X-Gotify-Key` 头（不占用 `Authorization`，与 Basic 可并存）。

***

## 限流

基于令牌桶，按客户端 IP 限流。通过 `--rate-limit-ip`/`BARK_SERVER_RATE_LIMIT_IP`（每秒请求数）、`--rate-limit-burst`（桶大小，默认等于 ip）、`--rate-limit-push`（是否对推送端点也限流）配置。

| 端点                              | 是否限流  | 触发条件                         |
| ------------------------------- | ----- | ---------------------------- |
| `/register`（GET/POST）           | 始终限流  | 配置 `--rate-limit-ip > 0` 即生效 |
| `/mcp`、`/mcp/:device_key`       | 始终限流  | 同上                           |
| `/push`、`/:device_key`（V1 兼容推送） | 默认不限流 | 仅当 `--rate-limit-push` 开启才限流 |

超限返回 `429 {"code":429,"message":"rate limit exceeded, retry later","timestamp":...}`。

***

## 推送

推送是核心接口。V2 走 `POST /push` JSON 请求体；V1 走 `GET/POST /:device_key[/:title...]/:body` URL 路径参数。服务端按 `Content-Type: application/json` 自动路由到 V2，否则 V1。

### POST /push（V2 单设备推送）

推送一条通知到 `device_key` 对应的所有有效平台（默认扇出，见 [多平台扇出](#多平台扇出)）。

**请求**

| 字段           | 类型                                       | 必填 | 说明                                                         |
| -------------- | ------------------------------------------ | ---- | ------------------------------------------------------------ |
| device\_key    | string                                     | 是\* | 目标设备 key（与 `device_keys` 二选一）                      |
| body           | string                                     | 否   | 通知正文（全空时自动填 `"Empty Message"`）                   |
| title          | string                                     | 否   | 通知标题（字体比正文大），为空时，鸿蒙端自动填 `"订阅通知"`、iOS 端自动填 `"Bark"` |
| subtitle       | string                                     | 否   | 通知副标题，设置了 `subtitle` 之后 `title` 和 `body` 必填    |
| platform       | string                                     | 否   | 收窄到指定平台：`ios` 或 `harmony`。省略则扇出到所有有效平台 |
| 其他 Push 字段 | 见 [Push 其它字段参考](#push-其它字段参考) | 否   | level/sound/badge/icon/url 等                                |

\* 单设备推送必填 `device_key`，批量推送改用 `device_keys`。

```sh
curl -X POST "http://127.0.0.1:18080/push" \
     -H 'Content-Type: application/json; charset=utf-8' \
     -d '{
  "device_key": "ynJ5Ft4atkMkWeo2PAvFhF",
  "title": "bleem",
  "body": "Test Bark Server",
  "badge": 1,
  "sound": "minuet",
  "icon": "https://day.app/assets/images/avatar.jpg",
  "group": "test",
  "url": "https://mritd.com"
}'
```

**响应（成功）**

```json
{"code":200,"message":"success","timestamp":1700000000}
```

**响应（失败）**

| HTTP 状态 | 触发条件                                          |
| ------- | --------------------------------------------- |
| 400     | `device_key` 为空 / 设备不存在 / 无有效 token / 请求体解析失败 |
| 500     | 推送失败（APNs/华为错误码非 2xx）                         |

```json
{"code":400,"message":"device key is empty","timestamp":1700000000}
```

### POST /push（V2 批量推送）

一次请求向多个 `device_key` 各推一条。每个设备独立推送，返回逐设备结果。

**请求**

| 字段           | 类型                                       | 必填 | 说明                           |
| -------------- | ------------------------------------------ | ---- | ------------------------------ |
| device\_keys   | string\[] 或逗号分隔字符串                 | 是   | 目标设备 key 列表              |
| 其他 Push 字段 | 见 [Push 其它字段参考](#push-其它字段参考) | 否   | 公共参数，对每个设备都推送一份 |

```sh
curl -X POST "http://127.0.0.1:18080/push" \
     -H 'Content-Type: application/json; charset=utf-8' \
     -d '{
  "title": "batch",
  "body": "Test Bark Server",
  "device_keys": ["ynJ5Ft4atkMkWeo2PAvFhF", "nysrshcqielvoxsa"]
}'
```

**响应**：HTTP 200，`data` 为数组，每项含 `code`、`device_key`，失败时额外带 `message`：

```json
{
  "code": 200,
  "message": "success",
  "timestamp": 1700000000,
  "data": [
    {"code": 200, "device_key": "ynJ5Ft4atkMkWeo2PAvFhF"},
    {"code": 410, "device_key": "nysrshcqielvoxsa", "message": "push failed: ..."}
  ]
}
```

> 批量上限由 `--max-batch-push-count` 控制（默认 `-1` 无限）。超限返回 `400 batch push count exceeds the maximum limit: N`。

### V1 兼容推送（路径参数）

老式 Bark 客户端走路径参数，段顺序固定。GET/POST 均支持：

| 路径形态                              | 说明                                       |
| ------------------------------------- | ------------------------------------------ |
| `/:device_key`                        | 仅 key，正文为空（自动填 "Empty Message"） |
| `/:device_key/:body`                  | key + 正文                                 |
| `/:device_key/:title/:body`           | key + 标题 + 正文                          |
| `/:device_key/:title/:subtitle/:body` | key + 标题 + 副标题 + 正文                 |

路径段会 `url.QueryUnescape` 解码，其余参数可走 query 或 form-data。

```sh
# 经典 Bark URL：GET /:device_key/:title/:body
curl "http://127.0.0.1:18080/ynJ5Ft4atkMkWeo2PAvFhF/%E6%A0%87%E9%A2%98/%E6%AD%A3%E6%96%87"

# POST 形式 + 额外 query 参数
curl -X POST "http://127.0.0.1:18080/ynJ5Ft4atkMkWeo2PAvFhF/hello?sound=minuet&group=test"
```

**响应**：同 [V2 单设备推送](#post-pushv2-单设备推送)。

### Push 其它字段参考

前面已提及的 `device_key`、`title`、`subtitle`、`body`，`device_keys` 不再重复

V2 请求体 / V1 query+form 共用的推送字段（小写键名）：

| 字段           | 类型       | iOS                                                          | HarmonyOS                                                    |
| -------------- | ---------- | ------------------------------------------------------------ | ------------------------------------------------------------ |
| id             | string     | 使用相同的ID值时，将更新对应推送的通知内容<br/>需 Bark v1.5.2, bark-server v2.2.5 以上，Json传参需使用字符串类型<br/>传 `id` 时监控流（`/message`）中同一 `device_key` + `extras.id` 的消息会被覆盖（保留原消息 ID），不传 `id` 则追加新消息 | integer 映射 `notification.notifyId`（int，范围 `[0, 2147483647]`），相同 `id` 的通知会互相覆盖；非数字 `id` 被忽略（由 Push Kit 自动生成标识） |
| level          | string     | APNs 优先级：`critical`/`active`/`timeSensitive`/`passive`   | -                                                            |
| volume         | string     | critical 通知铃声音量                                        | -                                                            |
| badge          | integer    | App 图标角标数，值为 `0` 时清除角标                          | 同 iOS                                                       |
| call           | string     | `1` 时铃声持续播放 30 秒                                     | -                                                            |
| autoCopy       | string     | `1` 时自动复制                                               | -                                                            |
| copy           | string     | 待复制的文本                                                 | -                                                            |
| sound          | string     | 铃声名（自动补 `.caf` 后缀），见 [Bark Sounds](https://github.com/Finb/Bark/tree/master/Sounds) | 铃声名与 Bark 一致，自动补 `.mp3` 后缀（已带 `.mp3`/`.wav`/`.mpeg` 后缀则保持不变，`.caf` 自动转 `.mp3`）；铃声文件需放在应用 `/resources/rawfile` 目录，且需在 AGC 申请「自定义铃声权益」，`category=MARKETING` 时自定义铃声无效 |
| soundDuration  | integer    | -                                                            | 通知铃声时长（单位秒），仅同时传了 `sound` 才生效，取值范围 `[1, 60]`（超出自动截断为 60），铃声不足该时长会循环播放；不传时铃声超过 30 秒截断 |
| icon           | string     | 图标 URL（iOS 15+）                                          | 华为会自动校验图片是否合规，必须是 HTTPS URL，支持图片格式为PNG、JPG、JPEG、BMP、WEBP，图片像素的总字节数不超过192KB，若超过则图片不展示 |
| image          | string     | 图片 URL（iOS 15+）                                          | -                                                            |
| group          | string     | 通知分组                                                     |                                                              |
| ciphertext     | string     | 加密推送的密文                                               | -                                                            |
| markdown       | string     | Markdown 正文，覆盖 `body`                                   |                                                              |
| isArchive      | string     | `1` 时由 App 归档                                            | -                                                            |
| ttl            | integer    | 归档消息存活秒数，过期自动删除                               | -                                                            |
| url            | string     | 点击通知跳转的 URL                                           | -                                                            |
| action         | string     | 传 "alert" 时，点击推送跳转到APP时会弹出操作弹窗             | 目前固定点击跳转应用首页                                     |
| delete         | string     | `1` 时静默推送（不展示，ContentAvailable）                   | -                                                            |
| foregroundShow | `string`   | -                                                            | 默认 `1`，应用在前后台都展示通知消息，其它值应用在前台时不通知 |
| inboxContent   | `string[]` | -                                                            | 多行消息（传了替换 `body`）：例<br />`"inboxContent": ["1. 通知栏消息样式", "2. 通知栏消息提醒方式和展示方式", "3. 通知栏消息语言本地化"]`；传该字段时自动携带 `style=3`（收件箱样式），无需单独传 `style` |
| data           | `string`   | -                                                            | 自定义数据载荷                                               |

> 表外字段原样透传为 APNs 自定义字段（`payload.custom`），key 转小写。

### 多平台扇出

同一 `device_key` 可同时绑定 iOS 与鸿蒙两条记录（数据库按 `(key, platform)` 唯一约束）。推送时默认**扇出到该 key 下所有有效平台**：

- 并发投递（`sync.WaitGroup`），**任一平台成功即返回 200**；
- 全部失败才返回 500，并带最后一次失败的错误码；
- 失效 token 按平台定向清理（`ClearDeviceTokenByKeyAndPlatform`），不会跨平台误清；
- 推送历史只记录一次（gotify 监控流），与平台数无关。

**收窄到指定平台**：在推送体里带 `"platform": "ios"` 或 `"platform": "harmony"`，仅推该平台记录。`platform` 是**收窄**而非覆盖——只选择投递哪些已绑定记录，不会改写存储的平台字段。

### 参数优先级

三个取值来源按优先级低 → 高覆盖：

1. **请求体（body）** — V2 JSON 最低
2. **URL 查询参数（query）** — V1/V2 通用
3. **URL 路径参数（path）** — V1 路径段最高

例：`POST /:device_key/:title/:body?sound=minuet` body `{"sound":"alarm"}` 最终 `sound=minuet`（query 覆盖 body）+ `title`/`body` 来自路径（最高）。

## 设备注册

### POST /register

注册或更新设备。同一 `(device_key, platform)` 重新注册会更新 token；同 key 不同 platform 会新增记录（不覆盖彼此）。

**请求体**（JSON / form-data）

| 字段            | 类型     | 必填 | 说明                              |
| ------------- | ------ | -- | ------------------------------- |
| device\_key   | string | 否  | 设备 key，为空时由服务端生成并返回             |
| device\_token | string | 是  | APNs/华为 Push Kit token，长度 ≤ 160 |
| platform      | string | 否  | `ios` 或 `harmony`，默认 `ios`      |

兼容旧字段：`key`（→ `device_key`）、`devicetoken`（→ `device_token`），新请求应使用新字段名。

```sh
curl -X POST "http://127.0.0.1:18080/register" \
     -H 'Content-Type: application/json' \
     -d '{
  "device_key": "my-device",
  "device_token": "<push_token>",
  "platform": "ios"
}'
```

**响应**：HTTP 200，`data` 含生成的 key 信息：

```json
{
  "code": 200,
  "message": "success",
  "timestamp": 1700000000,
  "data": {
    "key": "my-device",
    "device_key": "my-device",
    "device_token": "<push_token>",
    "platform": "ios"
  }
}
```

**错误**

| HTTP | 触发条件                                                                    |
| ---- | ----------------------------------------------------------------------- |
| 400  | `device_token` 为空 / 长度 > 160 / `platform` 非 `ios` \|`harmony` / 请求体解析失败 |
| 500  | 数据库写入失败                                                                 |

### GET /register（兼容旧客户端）

老式 Bark 客户端走 query 参数注册，参数解析从 query string 而非 body，注册逻辑与 POST 完全相同。

**Query 参数**

| 字段            | 类型     | 必填 | 说明                         |
| ------------- | ------ | -- | -------------------------- |
| key           | string | 否  | 设备 key（兼容字段）               |
| device\_key   | string | 否  | 设备 key（新字段）                |
| devicetoken   | string | 是  | 设备 token（兼容字段）             |
| device\_token | string | 是  | 设备 token（新字段）              |
| platform      | string | 否  | `ios` 或 `harmony`，默认 `ios` |

```sh
curl "http://127.0.0.1:18080/register?key=my-device&devicetoken=<token>&platform=ios"
```

**响应**：同 [POST /register](#post-register)。

### GET /register/:device\_key（校验存在）

校验 `device_key` 是否已注册。

```sh
curl "http://127.0.0.1:18080/register/my-device"
```

**响应**

| 情况     | HTTP | Body                                                           |
| ------ | ---- | -------------------------------------------------------------- |
| 存在     | 200  | `{"code":200,"message":"success","timestamp":...}`             |
| 不存在    | 400  | `{"code":400,"message":"<err>","timestamp":...}`               |
| key 为空 | 400  | `{"code":400,"message":"device key is empty","timestamp":...}` |

***

## Gotify 兼容监控（设备级）

设备级 Gotify 兼容接口，仅暴露 `/:device_key/<suffix>` 形态。详见 [GOTIFY\_COMPAT.md](GOTIFY_COMPAT.md)。

### GET /:device\_key/version

设备级探测，返回 gotify 兼容服务的版本。

```sh
curl "http://127.0.0.1:18080/my-device/version"
```

**响应**（非 CommonResp 格式）：

```json
{"version":"v0.4.0"}
```

> 服务未初始化时返回 `503 {"code":503,"message":"gotify compat not initialized",...}`。

### GET /:device\_key/message

查询该设备的历史推送消息（按设备隔离）。需 client token 鉴权。

**Query 参数**

| 字段    | 类型     | 默认  | 说明                      |
| ----- | ------ | --- | ----------------------- |
| limit | int    | 100 | 返回条数，min 1，max 200；`-1` 表示不限制，返回该设备全部现存消息（导出用） |
| since | uint64 | 0   | 返回 id < since 的消息（分页游标） |
| query | string | 空   | 关键词查找：对 title+body 做不区分大小写的子串匹配，与 limit/since 可组合；带 query 时响应 `paging` 额外返回 `total`（该设备命中总数，不受 limit 截断影响） |
| token | string | 必填  | client token（鉴权用，优先级最高） |

```sh
curl "http://127.0.0.1:18080/my-device/message?limit=100&since=0&token=<clientToken>"
# 或用头：curl -H "X-Gotify-Key: <clientToken>" "http://127.0.0.1:18080/my-device/message"
# 关键词查找（响应含 paging.total）：
curl "http://127.0.0.1:18080/my-device/message?query=cpu&limit=50&token=<clientToken>"
# 全量导出：
curl "http://127.0.0.1:18080/my-device/message?limit=-1&token=<clientToken>"
```

**响应**（gotify 分页信封，非 CommonResp）：

```json
{
  "paging": {"size": 2, "limit": 100, "since": 0},
  "messages": [
    {"id": 1, "device_key": "my-device", "title": "...", "body": "...", "...": "..."},
    {"id": 2, "device_key": "my-device", "title": "...", "body": "...", "...": "..."}
  ]
}
```

> 普通分页请求不返回 `total`（避免额外全量扫描）；仅带 `query` 的查找请求返回。`query` 与 `limit=-1` 可组合（导出全部命中，此时 `total == size`）。
>
> **`limit=-1` 为分块流式响应**（chunked transfer）：服务端边遍历边逐条下发，内存占用与消息总量无关，上调 `--gotify-max-messages` 也不会放大单次导出成本。响应仍是一个合法 JSON 信封，但 `messages` 数组在前、`paging` 补在末尾（JSON 解析无影响），且恒含 `total`；流中途异常时响应会正常收尾并在 `paging.error` 标记 `"stream truncated"`。
>
> `query` 关键词会出现在访问日志的 URL 里（与其它 query 参数同等处理，仅 token 被脱敏），公网部署建议全程 TLS。

**错误**：未带或错误 token → `401 {"code":401,"message":"unauthorized",...}`；服务未初始化 → `503`。

### DELETE /:device\_key/message

清空该设备的所有历史消息（不影响其他设备）。需 client token。

```sh
curl -X DELETE "http://127.0.0.1:18080/my-device/message?token=<clientToken>"
```

**响应**：`{"code":200,"message":"success","timestamp":...}`。

### DELETE /:device\_key/message/:id

删除该设备的单条消息。

| 路径参数         | 说明            |
| ------------ | ------------- |
| :device\_key | 设备 key        |
| :id          | 消息 id（uint64） |

```sh
curl -X DELETE "http://127.0.0.1:18080/my-device/message/42?token=<clientToken>"
```

**响应**

| 情况           | HTTP | Body                                                   |
| ------------ | ---- | ------------------------------------------------------ |
| 删除成功         | 200  | `success()`                                            |
| 消息不存在或不属于该设备 | 404  | `{"code":404,"message":"message not found",...}`       |
| id 非法        | 400  | `{"code":400,"message":"invalid message id: ...",...}` |
| 未授权          | 401  | `{"code":401,"message":"unauthorized",...}`            |

### GET /:device\_key/stream（WebSocket）

订阅该设备的实时推送流（仅该设备的消息）。需 client token（在 WebSocket 升级前校验，未授权返回 401 而非升级失败）。

```sh
# wscat 示例
wscat "ws://127.0.0.1:18080/my-device/stream?token=<clientToken>"
```

**协议**

- 升级为 WebSocket 后，服务端推送 bare gotify message JSON 帧（无事件/socketConnected 信封）。
- 客户端 ping：服务端回 pong 并重置 60s 读超时。
- 服务端每 45s 主动 ping（保活 NAT）。
- 读缓冲区上限 512 字节，60s 无读则断开。

**消息帧格式**：与 [GET /:device\_key/message](#get-device_keymessage) 返回的 message 对象同构。

***

## MCP 接口

暴露一个名为 `notify` 的 MCP 工具（Streamable HTTP，禁用 SSE 流以避免长连接）。详细参数与示例见 [MCP.md](MCP.md)。

### ALL /mcp

通用 MCP 端点，`device_key` 必须作为工具参数传入。

```sh
curl -X POST "http://127.0.0.1:18080/mcp" \
     -H 'Content-Type: application/json' \
     -H 'Accept: application/json, text/event-stream' \
     -d '{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "notify",
    "arguments": {
      "device_key": "my-device",
      "title": "MCP 推送",
      "body": "来自 AI 代理的通知",
      "sound": "minuet"
    }
  }
}'
```

### ALL /mcp/:device\_key

设备专属 MCP 端点，`device_key` 从 URL 路径预填，工具参数中可省略。

```sh
curl -X POST "http://127.0.0.1:18080/mcp/my-device" \
     -H 'Content-Type: application/json' \
     -H 'Accept: application/json, text/event-stream' \
     -d '{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "notify",
    "arguments": {
      "title": "MCP 推送",
      "body": "device_key 已从路径预填"
    }
  }
}'
```

**notify 工具参数**

| 参数          | 类型     | 必填                              | 说明                                            |
| ----------- | ------ | ------------------------------- | --------------------------------------------- |
| device\_key | string | `/mcp` 必填，`/mcp/:device_key` 不用 | 目标设备 key                                      |
| title       | string | 否                               | 通知标题                                          |
| subtitle    | string | 否                               | 通知副标题                                         |
| body        | string | 否                               | 通知正文                                          |
| markdown    | string | 否                               | Markdown 正文，覆盖 `body`                         |
| level       | enum   | 否                               | `critical`/`active`/`timeSensitive`/`passive` |
| volume      | number | 否                               | critical 通知音量，0-10，默认 5                       |
| badge       | number | 否                               | App 图标角标数                                     |
| call        | string | 否                               | `1` 时铃声持续 30 秒                                |
| sound       | string | 否                               | 铃声名（iOS 自动补 `.caf`，鸿蒙自动补 `.mp3`）               |
| soundDuration | number | 否                             | 鸿蒙通知铃声时长（秒），1-60，需配合 `sound` 使用               |
| icon        | string | 否                               | 图标 URL                                        |
| image       | string | 否                               | 图片 URL                                        |
| group       | string | 否                               | 通知分组                                          |
| isArchive   | string | 否                               | `1` 时归档                                       |
| ttl         | number | 否                               | 归档消息存活秒数                                      |
| url         | string | 否                               | 点击跳转 URL                                      |
| copy        | string | 否                               | 待复制文本                                         |

**响应**：标准 MCP `CallToolResult`，成功返回 `{"content":[{"type":"text","text":"Notification sent successfully"}]}`，失败返回 `{"isError":true,"content":[{"type":"text","text":"Failed to send notification: <err> (code <code>)"}]}`。

***

## 杂项

### GET /

存活探测，返回纯文本 `"ok"`。根路径不在 Basic Auth 中间件挂载范围（`Use("/+")` 不匹配零段），无需凭据。

```sh
curl "http://127.0.0.1:18080/"
# => ok
```

### GET /ping

健康探测，返回 CommonResp 格式的 pong。

```sh
curl "http://127.0.0.1:18080/ping"
```

```json
{"code":200,"message":"pong","timestamp":1700000000}
```

### GET /healthz

健康检查端点，返回纯文本 `"ok"`，与 `/ping` 功能等价。

```sh
curl "http://127.0.0.1:18080/healthz"
# => ok
```

### GET /version

返回服务版本（CommonResp 格式），供 Bark/Hotify 客户端在推送前校验服务端身份。

```sh
curl "http://127.0.0.1:18080/version"
```

```json
{"code":200,"message":"success","timestamp":1700000000,"data":{"version":"v0.4.0"}}
```

- `data.version` 为构建时经 `-ldflags` 注入的版本，未注入时为空字符串。
- **不在 Basic Auth 白名单内**：开启 Basic Auth 后需携带凭据，否则 `418`。
- 与 `/:device_key/version`（设备级 gotify 探测，白名单放行，返回 `{"version":"..."}`）和 `/info`（扁平 JSON，含完整构建信息）的区别：`/version` 是全局端点，仅返回 version，使用统一 `CommonResp` 结构。

### GET /info

返回服务版本/构建信息（扁平 JSON，非 CommonResp）。

```sh
curl "http://127.0.0.1:18080/info"
```

**匿名请求**（或 Basic Auth 未开启）：

```json
{"version":"v0.4.0","build":"...","arch":"linux/amd64","commit":"..."}
```

**带有效 Basic Auth 凭据**：额外返回 `devices`（当前设备总数）：

```json
{"version":"v0.4.0","build":"...","arch":"linux/amd64","commit":"...","devices":42}
```

| 字段      | 说明                                   |
| ------- | ------------------------------------ |
| version | 程序版本                                 |
| build   | 构建日期                                 |
| arch    | 系统/架构（`runtime.GOOS/runtime.GOARCH`） |
| commit  | 提交号                                  |
| devices | 设备总数（仅有效 Basic Auth 凭据时返回）           |

### GET /metrics

Prometheus 指标端点（文本格式）。

```sh
curl "http://127.0.0.1:18080/metrics"
```

主要指标：

| 指标                                                | 类型      | 说明                                           |
| ------------------------------------------------- | ------- | -------------------------------------------- |
| `timelynotify_http_requests_total{method,status}` | counter | HTTP 请求计数，status 为粗粒度分类（`2xx`/`4xx`/`5xx` 等） |
| `timelynotify_active_streams`                     | gauge   | 当前活跃的 `/stream` WebSocket 连接数                |
| `go_*`、`process_*`                                | —       | 标准 Go/进程收集器                                  |

> **不在 Basic Auth 白名单内**：开启 Basic Auth 后需携带凭据。指标服务未初始化时返回 `503 {"code":503,"message":"metrics not initialized",...}`。

***

## 多语言示例

### golang

```go
package main

import (
  "bytes"
  "fmt"
  "io/ioutil"
  "net/http"
)

func sendPush() {
  json := []byte(`{"body": "Test Bark Server","device_key": "nysrshcqielvoxsa","title": "bleem", "badge": 1, "icon": "https://day.app/assets/images/avatar.jpg", "group": "test", "url": "https://mritd.com","sound": "minuet"}`)
  body := bytes.NewBuffer(json)

  client := &http.Client{}
  req, err := http.NewRequest("POST", "http://127.0.0.1:18080/push", body)
  if err != nil {
    fmt.Println("Failure : ", err)
  }
  req.Header.Add("Content-Type", "application/json; charset=utf-8")

  resp, err := client.Do(req)
  if err != nil {
    fmt.Println("Failure : ", err)
  }
  defer resp.Body.Close()

  respBody, _ := ioutil.ReadAll(resp.Body)
  fmt.Println("response Status : ", resp.Status)
  fmt.Println("response Headers : ", resp.Header)
  fmt.Println("response Body : ", string(respBody))
}
```

### python

```python
# pip install requests
import requests
import json

def send_request():
    try:
        response = requests.post(
            url="http://127.0.0.1:18080/push",
            headers={"Content-Type": "application/json; charset=utf-8"},
            data=json.dumps({
                "body": "Test Bark Server",
                "device_key": "nysrshcqielvoxsa",
                "title": "bleem",
                "sound": "minuet",
                "badge": 1,
                "icon": "https://day.app/assets/images/avatar.jpg",
                "group": "test",
                "url": "https://mritd.com"
            })
        )
        print('Response HTTP Status Code: {status_code}'.format(
            status_code=response.status_code))
        print('Response HTTP Response Body: {content}'.format(
            content=response.content))
    except requests.exceptions.RequestException:
        print('HTTP Request failed')
```

### java

```java
import java.io.IOException;
import org.apache.http.client.fluent.*;
import org.apache.http.entity.ContentType;

public class SendRequest {
  public static void main(String[] args) { sendRequest(); }

  private static void sendRequest() {
    try {
      Content content = Request.Post("http://127.0.0.1:18080/push")
        .addHeader("Content-Type", "application/json; charset=utf-8")
        .bodyString("{\"body\": \"Test Bark Server\",\"device_key\": \"nysrshcqielvoxsa\",\"title\": \"bleem\",\"url\": \"https://mritd.com\", \"group\": \"test\",\"sound\": \"minuet\"}", ContentType.APPLICATION_JSON)
        .execute().returnContent();
      System.out.println(content);
    }
    catch (IOException e) { System.out.println(e); }
  }
}
```

### nodejs

```js
const http = require('http');

const httpOptions = {
    hostname: '127.0.0.1',
    port: '18080',
    path: '/push',
    method: 'POST',
    headers: {"Content-Type": "application/json; charset=utf-8"}
};
httpOptions.headers['User-Agent'] = 'node ' + process.version;

const request = http.request(httpOptions, (res) => {
    let responseBufs = [];
    let responseStr = '';

    res.on('data', (chunk) => {
        if (Buffer.isBuffer(chunk)) {
            responseBufs.push(chunk);
        } else {
            responseStr = responseStr + chunk;
        }
    }).on('end', () => {
        responseStr = responseBufs.length > 0 ?
            Buffer.concat(responseBufs).toString('utf8') : responseStr;
        console.log('STATUS:', res.statusCode);
        console.log('HEADERS:', JSON.stringify(res.headers));
        console.log('BODY:', responseStr);
    });
})
.setTimeout(0)
.on('error', (error) => { console.log('ERROR:', error); });

request.write(JSON.stringify({
    device_key: "nysrshcqielvoxsa",
    body: "Test Bark Server",
    title: "bleem",
    sound: "minuet",
    url: "https://mritd.com",
    group: "test"
}));
request.end();
```

### php

```php
$curl = curl_init();
curl_setopt_array($curl, [
    CURLOPT_URL => 'http://127.0.0.1:18080/push',
    CURLOPT_CUSTOMREQUEST => 'POST',
    CURLOPT_POSTFIELDS => '{
  "title": "bleem",
  "device_key": "nysrshcqielvoxsa",
  "body": "Test Bark Server",
  "badge": 1,
  "sound": "minuet",
  "icon": "https://day.app/assets/images/avatar.jpg",
  "group": "test",
  "url": "https://mritd.com"
}',
    CURLOPT_HTTPHEADER => [
        'Content-Type: application/json; charset=utf-8',
    ],
]);
$response = curl_exec($curl);
curl_close($curl);
echo $response;
```
