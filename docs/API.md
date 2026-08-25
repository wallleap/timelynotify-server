# API

兼容 V1 推送 API，V2 更改为 RESTful API

> 地址中的 `18080` 是容器对外映射端口（`-p 18080:8080`）。若直接运行二进制（默认监听 `0.0.0.0:8080`），请使用 `8080`。

- [API](#api)
  - [Push](#push)
  - [curl](#curl)
  - [HarmonyOS Push (curl)](#harmonyos-push-curl)
  - [Batch push (curl)](#batch-push-curl)
    - [golang](#golang)
    - [python](#python)
    - [java](#java)
    - [nodejs](#nodejs)
    - [php](#php)
  - [参数说明与优先级](#参数说明与优先级)
  - [V1 兼容推送（URL 路径参数）](#v1-兼容推送url-路径参数)
  - [MCP 工具参数](#mcp-工具参数)
  - [响应格式](#响应格式)
  - [认证](#认证)
  - [新增接口（本 fork）](#新增接口本-fork)
  - [Misc](#misc)
  - [Ping](#ping)
  - [Healthz](#healthz)
  - [Info](#info)
  - [Metrics](#metrics)

## Push

| Field | Type | Description |
| ----- | ---- | ----------- |
| id (optional) | string | Notification collapse id (APNs `apns-collapse-id`) |
| title (optional) | string | Notification title (font size would be larger than the body) |
| subtitle (optional) | string | Notification subtitle |
| body | string | Notification content |
| device_key | string | The key for each device |
| device_keys (optional) | array | Used for batch pushing |
| platform (optional) | string | Override the device's registered platform: `ios` or `harmony` |
| level (optional) | string | `'critical'`, `'active'`, `'timeSensitive'`, `'passive'` |
| volume (optional) | string | The ringtone volume for critical alert notification. |
| badge (optional) | integer | The number displayed next to App icon ([Apple Developer](https://developer.apple.com/documentation/usernotifications/unnotificationcontent/1649864-badge)) |
| call (optional) | string | Must be `1`, The ringtone will continue to play for 30 seconds |
| autoCopy (optional) | string | Must be `1` |
| copy (optional) | string | The value to be copied |
| sound (optional) | string | Value from [here](https://github.com/Finb/Bark/tree/master/Sounds)， and custom ringtones are also available |
| icon (optional) | string | An url to the icon, available only on iOS 15 or later |
| image (optional) | string | An url to the image, available only on iOS 15 or later |
| group (optional) | string | The group of the notification |
| ciphertext (optional) | string | The ciphertext of encrypted push notifications |
| markdown (optional) | string | Markdown 内容，覆盖 `body`（Body 留空可只用 markdown） |
| isArchive (optional) | string | Value must be `1`. Whether or not should be archived by the app |
| ttl (optional) | integer | Time to live for archived messages, in seconds. Expired archived messages are deleted automatically |
| url (optional) | string | Url that will jump when click notification |
| action (optional) | string | Set to "none", tap notifications do nothing |
| delete (optional) | string | Must be `1`. Silent push: delivers a background push and is not shown on screen |

> **Platform routing**: When a device is registered with `platform: harmony`, the server automatically routes push requests through the Huawei Push Kit API. You can also override the platform per-request by including `"platform": "harmony"` or `"platform": "ios"` in the push body. The `level` field is mapped to Huawei `click_action` as follows: when `level` is not specified, the default is `launch` (full-screen notification); `critical`/`timeSensitive` → `launch`; `active` → `banner`; all other values (e.g. `passive`) → `page`.

### curl

```sh
curl -X "POST" "http://127.0.0.1:18080/push" \
     -H 'Content-Type: application/json; charset=utf-8' \
     -d $'{
  "body": "Test Bark Server",
  "device_key": "ynJ5Ft4atkMkWeo2PAvFhF",
  "title": "bleem",
  "badge": 1,
  "sound": "minuet",
  "icon": "https://day.app/assets/images/avatar.jpg",
  "group": "test",
  "url": "https://mritd.com"
}'
```

### HarmonyOS Push (curl)

推送鸿蒙设备与 iOS 设备使用完全相同的 API。服务端会根据设备注册时的 `platform` 字段自动选择通道，无需客户端额外指定。

```sh
# 1. 先注册鸿蒙设备
curl -X POST "http://127.0.0.1:18080/register" \
     -H 'Content-Type: application/json' \
     -d '{
  "device_key": "my-harmony-device",
  "device_token": "<harmony_push_token>",
  "platform": "harmony"
}'

# 2. 发送推送（与 iOS 推送格式完全相同）
curl -X POST "http://127.0.0.1:18080/push" \
     -H 'Content-Type: application/json' \
     -d '{
  "device_key": "my-harmony-device",
  "title": "鸿蒙测试通知",
  "body": "这是一条鸿蒙推送测试消息",
  "level": "active"
}'
```

**鸿蒙特有字段**：`level` 字段映射到华为 `click_action`：

| Bark level | 华为 click_action | 说明 |
| ----- | ---- | ----------- |
| 不指定（默认） | `launch` | 全屏通知，需用户立即处理 |
| `critical` / `timeSensitive` | `launch` | 全屏通知，需用户立即处理 |
| `active` | `banner` | 横幅通知 |
| 其他（如 `passive`） | `page` | 普通通知 |

### Batch push (curl)

批量推送：`device_keys` 传数组（或逗号分隔字符串）。每个设备各推送一条，返回逐设备的推送结果。

```bash
curl -X "POST" "http://127.0.0.1:18080/push" \
     -H 'Content-Type: application/json; charset=utf-8' \
     -d $'{
  "title": "batch",
  "body": "Test Bark Server",
  "device_keys": ["ynJ5Ft4atkMkWeo2PAvFhF", "nysrshcqielvoxsa"]
}'
```

响应（批量时为 `data` 数组）：

```json
{
  "code": 200,
  "message": "success",
  "timestamp": 1700000000,
  "data": [
    {"code": 200, "device_key": "ynJ5Ft4atkMkWei2PAvFhF"},
    {"code": 410, "device_key": "nysrshcqielvoxsa", "message": "push failed: ..."}
  ]
}
```

### golang

```go
package main

import (
  "fmt"
  "io/ioutil"
  "net/http"
  "bytes"
)

func sendPush() {
  // push (POST http://127.0.0.1:18080/push)

  json := []byte(`{"body": "Test Bark Server","device_key": "nysrshcqielvoxsa","title": "bleem", "badge": 1, "icon": "https://day.app/assets/images/avatar.jpg", "group": "test", "url": "https://mritd.com","sound": "minuet"}`)
  body := bytes.NewBuffer(json)

  // Create client
  client := &http.Client{}

  // Create request
  req, err := http.NewRequest("POST", "http://127.0.0.1:18080/push", body)
  if err != nil {
    fmt.Println("Failure : ", err)
  }

  // Headers
  req.Header.Add("Content-Type", "application/json; charset=utf-8")

  // Fetch Request
  resp, err := client.Do(req)

  if err != nil {
    fmt.Println("Failure : ", err)
  }

  // Read Response Body
  respBody, _ := ioutil.ReadAll(resp.Body)

  // Display Results
  fmt.Println("response Status : ", resp.Status)
  fmt.Println("response Headers : ", resp.Header)
  fmt.Println("response Body : ", string(respBody))
}
```

### python

```python
# Install the Python Requests library:
# `pip install requests`

import requests
import json

def send_request():
    # push
    # POST http://127.0.0.1:18080/push

    try:
        response = requests.post(
            url="http://127.0.0.1:18080/push",
            headers={
                "Content-Type": "application/json; charset=utf-8",
            },
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

public class SendRequest
{
  public static void main(String[] args) {
    sendRequest();
  }
  
  private static void sendRequest() {
    
    // push (POST )
    
    try {
      
      // Create request
      Content content = Request.Post("http://127.0.0.1:18080/push")
      
      // Add headers
      .addHeader("Content-Type", "application/json; charset=utf-8")
      
      // Add body
      .bodyString("{\"body\": \"Test Bark Server\",\"device_key\": \"nysrshcqielvoxsa\",\"title\": \"bleem\",\"url\": \"https://mritd.com\", \"group\": \"test\",\"sound\": \"minuet\"}", ContentType.APPLICATION_JSON)
      
      // Fetch request and return content
      .execute().returnContent();
      
      // Print content
      System.out.println(content);
    }
    catch (IOException e) { System.out.println(e); }
  }
}
```

### nodejs

```node
// request push 
(function(callback) {
    'use strict';
        
    const httpTransport = require('http');
    const responseEncoding = 'utf8';
    const httpOptions = {
        hostname: '127.0.0.1',
        port: '18080',
        path: '/push',
        method: 'POST',
        headers: {"Content-Type":"application/json; charset=utf-8"}
    };
    httpOptions.headers['User-Agent'] = 'node ' + process.version;
 
    // Using Basic Auth {"username":"","password":""}
    // Paw Store Cookies option is not supported

    const request = httpTransport.request(httpOptions, (res) => {
        let responseBufs = [];
        let responseStr = '';
        
        res.on('data', (chunk) => {
            if (Buffer.isBuffer(chunk)) {
                responseBufs.push(chunk);
            }
            else {
                responseStr = responseStr + chunk;            
            }
        }).on('end', () => {
            responseStr = responseBufs.length > 0 ? 
                Buffer.concat(responseBufs).toString(responseEncoding) : responseStr;
            
            callback(null, res.statusCode, res.headers, responseStr);
        });
        
    })
    .setTimeout(0)
    .on('error', (error) => {
        callback(error);
    });
    request.write("{\"device_key\":\"nysrshcqielvoxsa\",\"body\":\"Test Bark Server\",\"title\":\"bleem\",\"sound\":\"minuet\",\"url\":\"https://mritd.com\", \"group\":\"test\"}")
    request.end();
    

})((error, statusCode, headers, body) => {
    console.log('ERROR:', error); 
    console.log('STATUS:', statusCode);
    console.log('HEADERS:', JSON.stringify(headers));
    console.log('BODY:', body);
});
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

## 参数说明与优先级

- `device_keys` 省略时为单设备推送（此时必须提供 `device_key`，否则 `400 device key is empty`）。
- 除上表字段外，其余字段会原样透传为 APNs 自定义字段（`payload.custom`），key 转小写。
- 3 个取值来源按优先级覆盖（低→高）：**请求体（body） → URL 查询参数（query） → URL 路径**。

### V1 兼容推送（URL 路径参数）

老式 Bark 客户端走路径参数，参数通过 URL 路径段传入，段顺序固定：
`/:device_key`、`/:device_key/:body`、`/:device_key/:title/:body`、`/:device_key/:title/:subtitle/:body`
（GET / POST 均支持）。路径中的 `title`/`subtitle`/`body` 会 `QueryUnescape` 解码。

```sh
# 经典 Bark URL 形态：GET /:device_key/:title/:body
curl "http://127.0.0.1:18080/ynJ5Ft4atkMkWeo2PAvFhF/%E6%A0%87%E9%A2%98/%E6%AD%A3%E6%96%87"
```

### MCP 工具参数

`/mcp` 与 `/mcp/:device_key` 暴露一个名为 `notify` 的 MCP 工具，函数参数与 Push 字段一致：
`title`/`subtitle`/`body`/`markdown`/`level`/`volume`/`badge`/`call`/`sound`/`icon`/`image`/`group`/`isArchive`/`ttl`/`url`/`copy`，其中 `/mcp` 需要工具参数里的 `device_key`，`/mcp/:device_key` 不用（从路径预填）。详情见 [MCP.md](MCP.md)。

## 响应格式

所有接口统一返回 `CommonResp`：

| Field | Type | Description |
| ----- | ---- | ----------- |
| code | int | 业务码（成功 `200`） |
| message | string | 提示信息（成功 `"success"`，失败为错误描述） |
| data (optional) | json | 附加数据（如批量推送结果、注册返回的 key） |
| timestamp | int | unix 秒级时间戳 |

- 成功单设备推送：`{"code":200,"message":"success","timestamp":...}`
- 失败：HTTP 状态码与 `code` 一致，如 `400`/`410`/`404` 等。

## 认证

本服务两套并存（互不冲突，可同时开启）：

### 客户端 token（Gotify 兼容接口）

设备级 `/:device_key/message`、`/:device_key/stream` 用客户端 token 鉴权，未带或错误时返回 `401`。三种携带方式按优先级从高到低：

1. query 参数：`?token=<clientToken>`
2. 请求头：`X-Gotify-Key: <clientToken>`
3. 请求头：`Authorization: Bearer <clientToken>`

```sh
curl "http://127.0.0.1:18080/<device_key>/message?token=<clientToken>"
curl -H "X-Gotify-Key: <clientToken>" "http://127.0.0.1:18080/<device_key>/message"
curl -H "Authorization: Bearer <clientToken>" "http://127.0.0.1:18080/<device_key>/message"
```

同时携带时按上述顺序取第一个非空值。token 生成方式见 [TOKENS.md](TOKENS.md)。

### Basic Auth（全局防护）

- `/push`、`/:device_key`（V1 兼容推送）、`/mcp*` **默认无独立认证**——`device_key` 本身就是凭证，需保证其私密性。
- 启用 Basic Auth（`--user` / `--password` 或 `BARK_SERVER_BASIC_AUTH_USER/PASSWORD`）后，**非白名单** 路径必须携带 `Authorization: Basic base64(user:password)`，否则返回 `418`。

**白名单路径**（Basic Auth 豁免，仍走各自原有认证）：

| 分类 | 路径 |
| ----- | ---- |
| 全局 | `/ping`、`/register`、`/healthz`、`/info` |
| 设备级 | `/:device_key/version`、`/:device_key/message`、`/:device_key/message/:id`（DELETE）、`/:device_key/stream` |

其中设备级 `/:device_key/message`、`/:device_key/stream` 仍需客户端 token 鉴权（见上）；`/info` 无凭据返回基础信息、带有效 Basic Auth 才返回设备数。根路径 `/` 因 BasicAuth 中间件挂载于 `Use("/+")`（不匹配零段根路径），无凭据亦返回 `"ok"`。

**非白名单路径**（Basic Auth 开启时必须携带凭据）：`/push`、`/:device_key`（V1 兼容推送）、`/:device_key/:body` 等路径形态、`/mcp*`、`/metrics`。

- 建议在 `Authorization` 头中携带，避免 query 明文泄露；推送参数仍可经 query 传递。

```sh
# user: password -> base64 -> 头值
curl -u admin:secret "http://127.0.0.1:18080/info"
curl -H "Authorization: Basic YWRtaW46c2VjcmV0" "http://127.0.0.1:18080/info"
```

### 两者的优先级/关系

- **Basic Auth 是包级的"能否进入"门禁**（按路径白名单放行，`/:device_key/message`、`/:device_key/stream`、`/info` 等豁免）；**客户端 token 是接口内的"你是谁"鉴权**（放行后才校验 token）。
- 同一请求可同时带两种头：`Authorization: Basic <...>` 通过 Basic Auth 门禁，`Authorization: Bearer <...>` 或 query 提供客户端 token——两者互不覆盖。
- Basic Auth 白名单见 [README](../README.md)。

## 新增接口（本 fork 新增）

除上游 V2 `/push` 外，本 fork 额外提供：

| Method | Path | 认证 | 说明 |
| ----- | ---- | ---- | ----------- |
| GET | `/` | 无 | 存活探测，返回 `"ok"` |
| GET | `/version` | 无（Basic Auth 开启时需凭据） | 全局版本探测，以 CommonResp 格式返回 `data.version`，供客户端校验服务端身份 |
| POST | `/register` | 无 | 设备注册（body：`device_key`(可选)/`device_token`/`platform`(可选, `ios` 或 `harmony`, 默认 `ios`)），返回 `device_key`。详见 [TUTORIAL.md](TUTORIAL.md) |
| GET | `/register` | 无 | 设备注册（兼容旧 query 参数：`key`/`devicetoken`） |
| GET | `/register/:device_key` | 无 | 校验 device_key 是否存在 |
| GET | `/:device_key/version` | 无 | 设备级探测 |
| GET | `/:device_key/message` | 客户端 token | Gotify 兼容历史消息查询，参数 `limit`(默认100/上限200)、`since`(id < since) |
| DELETE | `/:device_key/message` / `/:device_key/message/:id` | 客户端 token | Gotify 兼容设备级删除历史 |
| GET | `/:device_key/stream` | 客户端 token | Gotify 兼容设备级 WebSocket 订阅 |
| ALL | `/mcp` / `/mcp/:device_key` | 无 | MCP 接口（streamable HTTP），供 AI 代理调用 `notify` 推送 |

Gotify 兼容与 MCP 接口的详细参数与示例见 [GOTIFY_COMPAT.md](GOTIFY_COMPAT.md) 与 [MCP.md](MCP.md)。

## Misc

### Ping

```sh
curl "http://127.0.0.1:18080/ping"
```

响应：`{"code":200,"message":"pong","timestamp":...}`

### Healthz

```sh
curl "http://127.0.0.1:18080/healthz"
```

响应：`"ok"`（纯文本）。与 `/ping` 功能等价，供健康检查使用。

### Version

以标准 `CommonResp` 格式返回服务版本，供 Bark/Hotify 客户端在推送前校验服务端身份：

```sh
curl "http://127.0.0.1:18080/version"
```

```json
{"code":200,"message":"success","timestamp":...,"data":{"version":"v0.4.0"}}
```

- `data.version` 为构建时经 `-ldflags` 注入的程序版本，未注入时为空字符串。
- `/version` **不在 Basic Auth 白名单内**（白名单仅 `/ping` `/register` `/healthz` `/info` + 设备级 gotify 路径）：开启 Basic Auth 后需携带凭据，否则返回 `418`。
- 与 `/:device_key/version`（设备级 gotify 探测，白名单放行）和 `/info`（返回 version/build/arch/commit 等完整构建信息，扁平 JSON 格式）的区别：`/version` 是全局端点，仅返回 version 字段，使用统一 `CommonResp` 结构，便于客户端用同一套解析逻辑校验。

### Info

返回服务版本/构建信息：

```sh
curl "http://127.0.0.1:18080/info"
```

```json
{"version":"v0.4.0","build":"...","arch":"linux/amd64","commit":"..."}
```

- 字段：`version`（程序版本）、`build`（构建日期）、`arch`（系统/架构）、`commit`（提交号）。
- `/info` 在 Basic Auth 白名单内可直接访问；**带有效 Basic Auth 凭据**时额外返回 `devices`（当前设备总数），匿名请求不含该字段。

### Metrics

Prometheus 指标端点（免 Basic Auth 时需要显式放行；开启 Basic Auth 后非白名单路径会 418，见 [README](../README.md) 白名单说明）：

```sh
curl "http://127.0.0.1:18080/metrics"
```

- `timelynotify_http_requests_total{method,status}`：HTTP 请求计数（status 为粗粒度分类 `2xx/4xx/5xx` 等）。
- `timelynotify_active_streams`：当前活跃的 `/stream` WebSocket 连接数。
- 标准 Go/进程收集器（`go_*`、`process_*`）。
