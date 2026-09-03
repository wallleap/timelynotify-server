package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mritd/logger"
	"github.com/wallleap/timelynotify-server/internal/logging"
)

type contextKey string

const deviceKeyCtxKey contextKey = "device_key"
const ridCtxKey contextKey = "rid"

func init() {
	registerRoute("mcp", func(router fiber.Router) {
		mcpGenericStreamable := setupGenericMCPServer()
		mcpSpecificStreamable := setupSpecificMCPServer()

		// Basic endpoint - requires device_key in tool arguments
		router.All("/mcp", rateLimitMiddleware, func(c *fiber.Ctx) error {
			rid := ridFrom(c)
			return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := context.WithValue(r.Context(), ridCtxKey, rid)
				mcpGenericStreamable.ServeHTTP(w, r.WithContext(ctx))
			})(c)
		})

		// Device-specific endpoint - device_key is pre-filled from URL path
		router.All("/mcp/:device_key", rateLimitMiddleware, func(c *fiber.Ctx) error {
			deviceKey := c.Params("device_key")
			rid := ridFrom(c)
			return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := context.WithValue(r.Context(), deviceKeyCtxKey, deviceKey)
				ctx = context.WithValue(ctx, ridCtxKey, rid)
				mcpSpecificStreamable.ServeHTTP(w, r.WithContext(ctx))
			})(c)
		})
	})
}

func setupGenericMCPServer() *server.StreamableHTTPServer {
	s := server.NewMCPServer("TimelyNotify MCP Server", version,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	opts := getCommonToolOpts()
	opts = append(opts,
		mcp.WithString("device_key",
			mcp.Required(),
			mcp.Description("Device Key"),
		),
	)

	s.AddTool(mcp.NewTool("notify", opts...), notifyHandler)
	return server.NewStreamableHTTPServer(s,
		// Disable SSE streaming to avoid long-lived server->client connections on this deployment path.
		server.WithDisableStreaming(true),
	)
}

func setupSpecificMCPServer() *server.StreamableHTTPServer {
	s := server.NewMCPServer("TimelyNotify MCP Server (Specific)", version,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	s.AddTool(mcp.NewTool("notify", getCommonToolOpts()...), notifyHandler)
	return server.NewStreamableHTTPServer(s,
		// Disable SSE streaming to avoid long-lived server->client connections on this deployment path.
		server.WithDisableStreaming(true),
	)
}

func notifyHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		logger.Warnf("[MCP] invalid arguments format")
		return mcp.NewToolResultError("Invalid arguments format"), nil
	}

	var deviceKey string
	if val, ok := args["device_key"]; ok {
		if tmpDeviceKey, ok := val.(string); ok {
			deviceKey = tmpDeviceKey
		}
	}
	if val := ctx.Value(deviceKeyCtxKey); val != nil {
		if tmpDeviceKey, ok := val.(string); ok {
			deviceKey = tmpDeviceKey
		}
	}
	if len(deviceKey) == 0 {
		logger.Warnf("[MCP] device_key is required")
		return mcp.NewToolResultError("device_key is required"), nil
	}

	// Extract rid from context (injected by the fiber adaptor wrapper).
	// May be empty when called outside an HTTP request path (tests).
	rid, _ := ctx.Value(ridCtxKey).(string)

	args["device_key"] = deviceKey
	logger.Infof("[MCP] rid=%s notify: device_key=%s", rid, logging.MaskMiddle(deviceKey))

	code, err := push(rid, args)
	if err != nil {
		logger.Errorf("[MCP] rid=%s notify failed: device_key=%s code=%d err=%v",
			rid, logging.MaskMiddle(deviceKey), code, err)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to send notification: %v (code %d)", err, code)), nil
	}

	logger.Infof("[MCP] rid=%s notify success: device_key=%s code=%d",
		rid, logging.MaskMiddle(deviceKey), code)
	return mcp.NewToolResultText("Notification sent successfully"), nil
}

func getCommonToolOpts() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithDescription("Send a notification to a device via TimelyNotify"),
		mcp.WithString("title", mcp.Description("Notification title")),
		mcp.WithString("subtitle", mcp.Description("Notification subtitle")),
		mcp.WithString("body", mcp.Description("Notification content")),
		mcp.WithString("markdown", mcp.Description("Basic Markdown notification content. Overrides body.")),
		mcp.WithString("level",
			mcp.Description("Notification level"),
			mcp.Enum("critical", "active", "timeSensitive", "passive"),
		),
		mcp.WithNumber("volume",
			mcp.Description("Alert volume for important notification"),
			mcp.DefaultNumber(5),
			mcp.Max(10),
			mcp.Min(0),
		),
		mcp.WithNumber("badge", mcp.Description("Badge number")),
		mcp.WithString("call", mcp.Description("Set to '1' to repeat the notification ringtone")),
		mcp.WithString("sound", mcp.Description("Notification sound")),
		mcp.WithString("icon", mcp.Description("Notification icon URL")),
		mcp.WithString("image", mcp.Description("Notification image URL")),
		mcp.WithString("group", mcp.Description("Notification group")),
		mcp.WithString("isArchive", mcp.Description("Set to '1' to save the notification or any other value to skip saving")),
		mcp.WithNumber("ttl", mcp.Description("Time to live in seconds for archived messages; expired items are automatically deleted")),
		mcp.WithString("url", mcp.Description("Click action URL")),
		mcp.WithString("copy", mcp.Description("Text to copy on copy action")),
	}
}
