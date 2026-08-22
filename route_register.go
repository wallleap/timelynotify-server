package main

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mritd/logger"

	"github.com/wallleap/hotify-bark-server/database"
)

type DeviceInfo struct {
	DeviceKey   string `form:"device_key,omitempty" json:"device_key,omitempty" xml:"device_key,omitempty" query:"device_key,omitempty"`
	DeviceToken string `form:"device_token,omitempty" json:"device_token,omitempty" xml:"device_token,omitempty" query:"device_token,omitempty"`
	Platform    string `form:"platform,omitempty" json:"platform,omitempty" xml:"platform,omitempty" query:"platform,omitempty"`

	// compatible with old req
	OldDeviceKey   string `form:"key,omitempty" json:"key,omitempty" xml:"key,omitempty" query:"key,omitempty"`
	OldDeviceToken string `form:"devicetoken,omitempty" json:"devicetoken,omitempty" xml:"devicetoken,omitempty" query:"devicetoken,omitempty"`
}

func init() {
	registerRoute("register", func(router fiber.Router) {
		router.Post("/register", rateLimitMiddleware, func(c *fiber.Ctx) error { return doRegister(c, false) })
		router.Get("/register/:device_key", doRegisterCheck)
	})

	// compatible with old requests
	registerRouteWithWeight("register_compat", 100, func(router fiber.Router) {
		router.Get("/register", rateLimitMiddleware, func(c *fiber.Ctx) error { return doRegister(c, true) })
	})
}

func doRegister(c *fiber.Ctx, compat bool) error {
	var deviceInfo DeviceInfo
	if compat {
		if err := c.QueryParser(&deviceInfo); err != nil {
			logger.Warnf("[Register] query parse failed: %v", err)
			return c.Status(400).JSON(failed(400, "request bind failed1: %v", err))
		}
	} else {
		if err := c.BodyParser(&deviceInfo); err != nil {
			logger.Warnf("[Register] body parse failed: %v", err)
			return c.Status(400).JSON(failed(400, "request bind failed2: %v", err))
		}
	}

	if deviceInfo.DeviceKey == "" && deviceInfo.OldDeviceKey != "" {
		deviceInfo.DeviceKey = deviceInfo.OldDeviceKey
	}

	if deviceInfo.DeviceToken == "" {
		if deviceInfo.OldDeviceToken != "" {
			deviceInfo.DeviceToken = deviceInfo.OldDeviceToken
		} else {
			logger.Warnf("[Register] device token is empty")
			return c.Status(400).JSON(failed(400, "device token is empty"))
		}
	}

	if len(deviceInfo.DeviceToken) > 160 {
		logger.Warnf("[Register] device token too long: len=%d", len(deviceInfo.DeviceToken))
		return c.Status(400).JSON(failed(400, "device token is invalid"))
	}

	platform := "ios"
	if deviceInfo.Platform != "" {
		platform = deviceInfo.Platform
		if platform != "ios" && platform != "harmony" {
			logger.Warnf("[Register] invalid platform: %s", platform)
			return c.Status(400).JSON(failed(400, "invalid platform (must be 'ios' or 'harmony')"))
		}
	}

	logger.Infof("[Register] registering device: key=%s platform=%s compat=%v",
		deviceInfo.DeviceKey, platform, compat)

	dbInfo := &database.DeviceInfo{
		Key:      deviceInfo.DeviceKey,
		Token:    deviceInfo.DeviceToken,
		Platform: platform,
	}
	
	newKey, err := db.SaveDeviceInfo(dbInfo)
	if err != nil {
		logger.Errorf("[Register] failed: key=%s err=%v", deviceInfo.DeviceKey, err)
		return c.Status(500).JSON(failed(500, "device registration failed: %v", err))
	}
	deviceInfo.DeviceKey = newKey

	logger.Infof("[Register] success: key=%s platform=%s", newKey, platform)
	return c.Status(200).JSON(data(map[string]string{
		"key":          deviceInfo.DeviceKey,
		"device_key":   deviceInfo.DeviceKey,
		"device_token": deviceInfo.DeviceToken,
		"platform":     platform,
	}))
}

func doRegisterCheck(c *fiber.Ctx) error {
	deviceKey := c.Params("device_key")

	if deviceKey == "" {
		logger.Warnf("[Register] check: device key is empty")
		return c.Status(400).JSON(failed(400, "device key is empty"))
	}

	logger.Infof("[Register] checking device: key=%s", deviceKey)
	_, err := db.DeviceTokenByKey(deviceKey)
	if err != nil {
		logger.Infof("[Register] device not found: key=%s", deviceKey)
		return c.Status(400).JSON(failed(400, "%s", err.Error()))
	}
	logger.Infof("[Register] device exists: key=%s", deviceKey)
	return c.Status(200).JSON(success())
}
