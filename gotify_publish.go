package main

import (
	"strings"

	"github.com/wallleap/timelynotify-server/apns"
	"github.com/mritd/logger"
)

// gotifyPublish mirrors a resolved push into the gotify-compatible
// monitoring stream that hotify-bridge consumes. It is invoked from push()
// right after the device token is resolved, so iOS delivery success/failure
// never affects the monitoring feed (Huawei-side forwarding still happens).
func gotifyPublish(msg *apns.PushMessage) {
	if gotifyService == nil {
		return
	}

	extras := make(map[string]interface{}, len(msg.ExtParams)+2)
	for k, v := range msg.ExtParams {
		extras[k] = v
	}
	if msg.Subtitle != "" {
		extras["subtitle"] = msg.Subtitle
	}
	extras["device_key"] = msg.DeviceKey

	title := msg.Title
	if title == "" {
	title = "TimelyNotify"
	}

	if err := gotifyService.Publish(title, msg.Body, gotifyPriority(extras), extras); err != nil {
		logger.Errorf("gotify publish failed: %v", err)
	}
}

// gotifyPriority maps the "level" parameter onto gotify's 0-2 scale.
func gotifyPriority(extras map[string]interface{}) int {
	lvl, _ := extras["level"].(string)
	switch strings.ToLower(lvl) {
	case "critical", "timeSensitive":
		return 2
	case "active":
		return 1
	default:
		return 0
	}
}
