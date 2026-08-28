package database

import (
	"fmt"
	"strings"
	"sync"
)

var (
	cacheKey = "MemoryBaseKey"
)

// cacheByPlatform holds the per-platform DeviceInfo for the single key MemBase
// serves. The mutex guards concurrent register/clear paths that tests may run
// in parallel.
var (
	cacheMu         sync.RWMutex
	cacheByPlatform = make(map[string]*DeviceInfo)
)

type MemBase struct {
}

func NewMemBase() Database {
	return &MemBase{}
}

func (d *MemBase) CountAll() (int, error) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return len(cacheByPlatform), nil
}

// DeviceTokenByKey returns any non-empty token for the key, preferring ios.
func (d *MemBase) DeviceTokenByKey(key string) (string, error) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if key != "" && key != cacheKey {
		return "", fmt.Errorf("key not found")
	}
	// Prefer ios.
	if info, ok := cacheByPlatform["ios"]; ok && info.Token != "" {
		return info.Token, nil
	}
	for _, info := range cacheByPlatform {
		if info.Token != "" {
			return info.Token, nil
		}
	}
	return "", fmt.Errorf("key not found")
}

// SaveDeviceTokenByKey is the legacy upsert (defaults to platform "ios").
func (d *MemBase) SaveDeviceTokenByKey(key, token string) (string, error) {
	return d.SaveDeviceInfo(&DeviceInfo{
		Key:      key,
		Token:    token,
		Platform: "ios",
	})
}

// DevicesByKey returns every (key, platform) record for the key.
func (d *MemBase) DevicesByKey(key string) ([]*DeviceInfo, error) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if key != "" && key != cacheKey {
		return nil, fmt.Errorf("key not found")
	}
	if len(cacheByPlatform) == 0 {
		return nil, fmt.Errorf("key not found")
	}
	// Stable order: ios first, then the rest alphabetically.
	infos := make([]*DeviceInfo, 0, len(cacheByPlatform))
	if info, ok := cacheByPlatform["ios"]; ok {
		infos = append(infos, info)
	}
	for plat, info := range cacheByPlatform {
		if plat == "ios" {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// DeviceInfoByKey returns the first record for the key, preferring "ios".
// Deprecated: use DevicesByKey for multi-platform fan-out.
func (d *MemBase) DeviceInfoByKey(key string) (*DeviceInfo, error) {
	infos, err := d.DevicesByKey(key)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info.Platform == "ios" {
			return info, nil
		}
	}
	return infos[0], nil
}

// SaveDeviceInfo upserts by (key, platform). The same key may hold distinct
// tokens for distinct platforms.
func (d *MemBase) SaveDeviceInfo(info *DeviceInfo) (string, error) {
	if info.Platform == "" {
		info.Platform = "ios"
	}
	if info.Key == "" {
		info.Key = cacheKey
	}
	if info.Key != cacheKey {
		return "", fmt.Errorf("key not found")
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	// Deep copy token to prevent Fiber memory reuse bugs.
	stored := &DeviceInfo{
		Key:      info.Key,
		Token:    strings.Clone(info.Token),
		Platform: info.Platform,
	}
	cacheByPlatform[info.Platform] = stored
	return info.Key, nil
}

// ClearDeviceTokenByKeyAndPlatform empties the token of the given (key,
// platform) pair, keeping the record so the key stays known.
func (d *MemBase) ClearDeviceTokenByKeyAndPlatform(key, platform string) error {
	if platform == "" {
		platform = "ios"
	}
	if key != "" && key != cacheKey {
		return fmt.Errorf("key not found")
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	info, ok := cacheByPlatform[platform]
	if !ok {
		return nil
	}
	info.Token = ""
	return nil
}

func (d *MemBase) DeleteDeviceByKey(key string) error {
	if key != "" && key != cacheKey {
		return fmt.Errorf("key not found")
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for k := range cacheByPlatform {
		delete(cacheByPlatform, k)
	}
	return nil
}

func (d *MemBase) Close() error {
	return nil
}
