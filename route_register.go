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
			return c.Status(400).JSON(failed(400, "request bind failed1: %v", err))
		}
	} else {
		if err := c.BodyParser(&deviceInfo); err != nil {
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
			return c.Status(400).JSON(failed(400, "device token is empty"))
		}
	}

	// DeviceToken length is variable, but should not be too long.
	if len(deviceInfo.DeviceToken) > 160 {
		return c.Status(400).JSON(failed(400, "device token is invalid"))
	}

	// Validate platform if provided
	platform := "ios"
	if deviceInfo.Platform != "" {
		platform = deviceInfo.Platform
		if platform != "ios" && platform != "harmony" {
			return c.Status(400).JSON(failed(400, "invalid platform (must be 'ios' or 'harmony')"))
		}
	}

	// Save device info using new API (supports platform)
	dbInfo := &database.DeviceInfo{
		Key:      deviceInfo.DeviceKey,
		Token:    deviceInfo.DeviceToken,
		Platform: platform,
	}
	
	newKey, err := db.SaveDeviceInfo(dbInfo)
	if err != nil {
		logger.Errorf("device registration failed: %v", err)
		return c.Status(500).JSON(failed(500, "device registration failed: %v", err))
	}
	deviceInfo.DeviceKey = newKey

	return c.Status(200).JSON(data(map[string]string{
		// compatible with old resp
		"key":          deviceInfo.DeviceKey,
		"device_key":   deviceInfo.DeviceKey,
		"device_token": deviceInfo.DeviceToken,
		"platform":     platform,
	}))
}

func doRegisterCheck(c *fiber.Ctx) error {
	deviceKey := c.Params("device_key")

	if deviceKey == "" {
		return c.Status(400).JSON(failed(400, "device key is empty"))
	}

	_, err := db.DeviceTokenByKey(deviceKey)
	if err != nil {
		return c.Status(400).JSON(failed(400, "%s", err.Error()))
	}
	return c.Status(200).JSON(success())
}
