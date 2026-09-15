# MCP

Bark supports the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) via HTTP Streamable, allowing AI agents (like Claude Desktop, Cherry Studio or n8n) to send notifications directly through Bark.

The MCP interface is **platform-agnostic** — it works identically for both iOS (APNs) and HarmonyOS (Huawei Push Kit) devices. When a `device_key` has records on both platforms, the server **fans out** the notification to every bound platform (both devices receive it); the request succeeds as long as any one platform delivery succeeds. To narrow delivery to one platform, pass `"platform": "ios"` or `"platform": "harmony"` in the tool arguments.

## Endpoints

| Endpoint           | Description                                                                                                   |
| ------------------ | ------------------------------------------------------------------------------------------------------------- |
| `/mcp`             | Generic MCP endpoint. Requires `device_key` to be provided in the tool arguments.                             |
| `/mcp/:device_key` | Device-specific MCP endpoint. The `device_key` is fixed by the URL, and the AI agent doesn't need to know it. |

## Examples

Cherry Studio:  

```json
{
  "mcpServers": {
    "bark": {
      "type": "streamableHttp",
      "url": "https://<your-host>/mcp/<device_key>"
    }
  }
}
```

VS Code:  

```js
{
  "servers": {
    "bark": {
      "type": "http",
      "url": "https://<your-host>/mcp/<device_key>"
    }
  }
}
```

Claude Code:  

```sh
claude mcp add timelynotify --transport http https://<your-host>/mcp/<device_key>
```  

or  

```js
{
  "mcpServers": {
    "bark": {
      "type": "http",
      "url": "https://<your-host>/mcp/<device_key>"
    }
  }
}
```

> Note: Replace `<your-host>` and `<device_key>` with this TimelyNotify Server deployment and the target device key. If `--url-prefix` is configured, include it before `/mcp`; use `/mcp` without a device key when the client should pass `device_key` as a tool argument.
