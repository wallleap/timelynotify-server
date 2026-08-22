package database

import (
	"fmt"
	"strings"
)

var (
	cacheKey         = "MemoryBaseKey"
	cacheDeviceToken = ""
	cachePlatform    = ""
)

type MemBase struct {
}

func NewMemBase() Database {
	return &MemBase{}
}

func (d *MemBase) CountAll() (int, error) {
	return 1, nil
}

func (d *MemBase) DeviceTokenByKey(key string) (string, error) {
	if cacheKey == key && cacheDeviceToken != "" {
		return cacheDeviceToken, nil
	}
	return "nil", fmt.Errorf("key not found")
}

func (d *MemBase) SaveDeviceTokenByKey(key, token string) (string, error) {
	if key != "" && key != cacheKey {
		return "", fmt.Errorf("key not found")
	}
	// Deep copy prevents Fiber memory overwrite bugs.
	cacheDeviceToken = strings.Clone(token)
	cachePlatform = "ios" // Legacy tokens are always for iOS
	return key, nil
}

func (d *MemBase) DeleteDeviceByKey(key string) error {
	if key != "" && key != cacheKey {
		return fmt.Errorf("key not found")
	}
	cacheDeviceToken = ""
	cachePlatform = ""
	return nil
}

// DeviceInfoByKey returns the device info for the given key
func (d *MemBase) DeviceInfoByKey(key string) (*DeviceInfo, error) {
	if cacheKey == key && cacheDeviceToken != "" {
		platform := cachePlatform
		if platform == "" {
			platform = "ios" // Default for backward compatibility
		}
		return &DeviceInfo{Key: key, Token: cacheDeviceToken, Platform: platform}, nil
	}
	return nil, fmt.Errorf("key not found")
}

// SaveDeviceInfo saves the device info
func (d *MemBase) SaveDeviceInfo(info *DeviceInfo) (string, error) {
	if info.Key != "" && info.Key != cacheKey {
		return "", fmt.Errorf("key not found")
	}
	cacheDeviceToken = strings.Clone(info.Token)
	cachePlatform = info.Platform
	if info.Key == "" {
		return cacheKey, nil
	}
	return info.Key, nil
}

func (d *MemBase) Close() error {
	return nil
}
