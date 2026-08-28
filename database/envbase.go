package database

import (
	"fmt"
	"os"
)

type EnvBase struct {
}

func NewEnvBase() Database {
	return &EnvBase{}
}

// EnvBase mirrors a single device configured via BARK_KEY / BARK_DEVICE_TOKEN
// environment variables. It is a read-mostly adapter for serverless-style
// deployments: the device is always treated as iOS, and writes are accepted
// only when they match the configured token.

func (d *EnvBase) CountAll() (int, error) {
	if os.Getenv("BARK_KEY") != "" {
		return 1, nil
	}
	return 0, nil
}

// DeviceTokenByKey returns the configured token when the key matches.
func (d *EnvBase) DeviceTokenByKey(key string) (string, error) {
	if key == os.Getenv("BARK_KEY") {
		return os.Getenv("BARK_DEVICE_TOKEN"), nil
	}
	return "", fmt.Errorf("key not found")
}

// SaveDeviceTokenByKey accepts the legacy upsert only when the token matches
// the configured one (serverless deployments are read-mostly).
func (d *EnvBase) SaveDeviceTokenByKey(key, token string) (string, error) {
	return d.SaveDeviceInfo(&DeviceInfo{Key: key, Token: token, Platform: "ios"})
}

// DevicesByKey returns a single iOS record for the configured key.
func (d *EnvBase) DevicesByKey(key string) ([]*DeviceInfo, error) {
	if key == os.Getenv("BARK_KEY") {
		return []*DeviceInfo{{
			Key:      key,
			Token:    os.Getenv("BARK_DEVICE_TOKEN"),
			Platform: "ios",
		}}, nil
	}
	return nil, fmt.Errorf("key not found")
}

// DeviceInfoByKey returns the single iOS record for the configured key.
// Deprecated: use DevicesByKey for multi-platform fan-out.
func (d *EnvBase) DeviceInfoByKey(key string) (*DeviceInfo, error) {
	infos, err := d.DevicesByKey(key)
	if err != nil {
		return nil, err
	}
	return infos[0], nil
}

// SaveDeviceInfo accepts the upsert only when the token matches the
// configured one. Platform is normalized to "ios" (EnvBase is iOS-only).
func (d *EnvBase) SaveDeviceInfo(info *DeviceInfo) (string, error) {
	if info.Token == os.Getenv("BARK_DEVICE_TOKEN") {
		return os.Getenv("BARK_KEY"), nil
	}
	return "", fmt.Errorf("device token is invalid")
}

// ClearDeviceTokenByKeyAndPlatform is a no-op for EnvBase: the token is
// sourced from the environment and cannot be cleared at runtime.
func (d *EnvBase) ClearDeviceTokenByKeyAndPlatform(key, platform string) error {
	return nil
}

func (d *EnvBase) DeleteDeviceByKey(key string) error {
	return fmt.Errorf("not supported")
}

func (d *EnvBase) Close() error {
	return nil
}
