# Gotify-Compatible Monitoring Interface

bark-server 对外提供一组与 [Gotify](https://gotify.net) 协议兼容的接口，让 [hotify-bridge](https://github.com/sakura-lolipop/hotify-bridge) 可以

- 像监测 Gotify 一样监测 bark——bark 每收到一次推送，就把它持久化为一条 gotify 风格的消息，并实时推送给订阅者
- 同时可以获取、删除消息

> iOS / HarmonyOS 侧投递成败**不影响**这条监测流（`gotifyPublish` 在路由到具体推送通道前即执行）

## 接口

每个 `device_key` 可单独访问自己的历史与实时流。**认证使用 client token**，token 读取优先级：`?token=` → `X-Gotify-Key` 头 → `Authorization: Bearer`。设备级接口只透传该 `device_key` 自己产生的消息，其它设备的消息不回。

| Method | Path | 认证 | 说明 |
|---|---|---|---|
| GET | `/<device_key>/version` | 无 | 设备级探测，返回服务版本号 |
| GET | `/<device_key>/message?token=<clientToken>&limit=10&since=<id>` | `token` | 该设备的历史消息（其余参数语义同全局） |
| DELETE | `/<device_key>/message?token=<clientToken>` | `token` | 清空该设备的历史消息（其它设备保留） |
| DELETE | `/<device_key>/message/<id>?token=<clientToken>` | `token` | 删除该设备下指定 id；不属于该设备或不存在返回 404 |
| GET | `/<device_key>/stream?token=<clientToken>` | `token` | WebSocket，实时推送该设备的裸消息帧 |

设备级路径为**静态段**（`/version`、`/message`、`/stream`），优先于旧版 `GET /:device_key/:body` 兼容推送，不会与 `/<device_key>` 单段推送冲突。全局 gotify 接口已移除，仅保留设备级路径。

消息帧 / `messages[]` 元素格式（与 Gotify 一致）：

```json
{
  "id": 1,
  "appid": 1,
  "title": "Hello",
  "message": "World",
  "priority": 2,
  "extras": {"device_key": "xxx", "level": "critical"},
  "date": "2026-08-05T20:52:46.987289+08:00"
}
```

- 所有消息归入单一虚拟应用 `appid=1`。
- `date` 为 RFC3339Nano 字符串，桥按不透明字符串透传。
- `priority` 由 bark 的 `level` 映射：`critical`/`timeSensitive`→2、`active`→1、其余→0。
- token 读取优先级：`?token=` → `X-Gotify-Key` 头 → `Authorization: Bearer`（与 Gotify 相同）。
  **推荐用 header 传递**（`X-Gotify-Key` 或 `Authorization: Bearer`）：token 不进入 URL，也就不会出现在
  代理/网关与访问日志里（本服务端访问日志只记路径，不记 query）。生产部署务必启用 TLS
  （`--cert`/`--key` 或反向代理），否则任何 token 传递方式在网络层都是明文。
- 未授权访问设备级 `/<device_key>/message`、`/<device_key>/stream` 返回 `401`（WebSocket 在握手阶段返回 401）。

## 客户端 token

- 通过环境变量 `BARK_SERVER_GOTIFY_CLIENT_TOKEN` 或启动参数 `--gotify-client-token` 预置
  （**强烈推荐**，可重复部署）。**只以 SHA-256 哈希存储**，不做明文持久化——即使
  `gotify.db` 泄露，也不存在可直接使用的凭证；同时避免自动生成路径把明文 token 写进数据文件。
- 不设置时自动生成：以哈希持久化到 `<data>/gotify.db`（0600），**首次生成时打印一次**，
  之后重启保持稳定；明文仅用于首次展示，丢失后请改用环境变量预置或删除该文件重新生成。
- 数据目录不可写时退化为内存存储（不持久化），hotify-bridge 靠 id 倒退信号兜底重启场景。
- **查看自动生成的 token（Docker）**：首次启动（数据目录为空/新卷）时运行
  `docker logs -f timelynotify-server`（容器名按你的 `--name` 或 compose 服务名调整），
  可以看到 `INFO ... Generated client token (set bridge gotify_token to this): <token>`；
  该行**只在首次生成时打印一次**，之后重启打印的是持久化位置提示（见上）。想随时拿到
  token，请预置（推荐），或停服删除 `<data>/gotify.db` 后重新生成。

## 接入 hotify-bridge

在桥的 `bridge_config.yaml` 中配置（或环境变量 `GOTIFY_HTTP_URL` / `GOTIFY_CLIENT_TOKEN`）：

```yaml
gotify_url: http://<bark-host>:18080/<device_key>
gotify_token: <上面拿到的 client token>
```

桥即可像监测 Gotify 一样订阅 bark 的 `/<device_key>/stream` 并回补历史。

## 行为与运维说明

- 推送即发布：`push()` 解析到 `device_token` 后即写入消息并广播，**不等待** APNs 或华为推送结果。
- **batch 推送会为每个设备各发布一条消息**（每条一次 `push()`），对应每条设备级投递。
- 消息保留最近 **1000** 条（`<data>/gotify.db`），超出自动裁剪；桥断线回补最多覆盖最新 100 条。
- 消息 ID 单调递增（bbolt `NextSequence`），重启不倒退；若存储被重置，桥按 id 倒退信号自动重置水位。
- 设备级 `/<device_key>/message`、`/<device_key>/stream`、`/<device_key>/version`
  已加入基础认证白名单（它们走自己的 token 认证/无需认证），开启 `--user/--password` 时不受影响。
- 兼容路由说明：`/version`、`/message`、`/stream` 为静态路径段（设备级路径 `/<device_key>/version` 等基于这些段），优先于旧版 `GET /:device_key`
  兼容推送；若某个设备 key 恰好叫 `message`/`stream`/`version`，其旧的 GET 兼容推送会命中本接口
  并返回 `401`，请改用 `POST /push` 或换设备 key。全局 gotify 接口（`/version`、`/message`、`/stream`）已移除。
- WebSocket 心跳：服务器 45s 发一次 ping；客户端 ping（桥每 20s）会刷新读超时（60s），
  静默失效的连接会被回收。WebSocket 默认放行所有 Origin（桥不发 Origin）。
- 平台路由：推送消息会按设备注册时的 `platform` 字段自动路由到 APNs（iOS）或华为 Push Kit（HarmonyOS）。监控流不区分平台，所有推送都会进入统一的消息流。
