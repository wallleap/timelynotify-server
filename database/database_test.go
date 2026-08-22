package database

import (
	"path/filepath"
	"testing"
)

// TestMemBaseCRUD exercises the in-memory database used in tests/serverless-adjacent
// scenarios. MemBase holds a single package-level key/token pair keyed by the
// non-empty package cacheKey.
func TestMemBaseCRUD(t *testing.T) {
	db := NewMemBase()

	// save a token under the known key
	key, err := db.SaveDeviceTokenByKey(cacheKey, "token-v1")
	if err != nil {
		t.Fatalf("SaveDeviceTokenByKey failed: %v", err)
	}
	if key != cacheKey {
		t.Fatalf("want returned key %q, got %q", cacheKey, key)
	}

	tok, err := db.DeviceTokenByKey(cacheKey)
	if err != nil {
		t.Fatalf("DeviceTokenByKey failed: %v", err)
	}
	if tok != "token-v1" {
		t.Fatalf("want token %q, got %q", "token-v1", tok)
	}

	if n, _ := db.CountAll(); n != 1 {
		t.Fatalf("want CountAll=1, got %d", n)
	}

	// Updating the token should replace it, not append.
	if _, err := db.SaveDeviceTokenByKey(cacheKey, "token-v2"); err != nil {
		t.Fatalf("SaveDeviceTokenByKey(update) failed: %v", err)
	}
	tok, _ = db.DeviceTokenByKey(cacheKey)
	if tok != "token-v2" {
		t.Fatalf("want updated token %q, got %q", "token-v2", tok)
	}

	// Delete clears the token.
	if err := db.DeleteDeviceByKey(cacheKey); err != nil {
		t.Fatalf("DeleteDeviceByKey failed: %v", err)
	}
	if _, err := db.DeviceTokenByKey(cacheKey); err == nil {
		t.Fatal("want error after delete, got nil")
	}
}

func TestMemBaseErrors(t *testing.T) {
	db := NewMemBase()
	// Unknown key must error.
	if _, err := db.DeviceTokenByKey("nope"); err == nil {
		t.Fatal("want error for unknown key, got nil")
	}
	// Deleting an unknown key errors.
	if err := db.DeleteDeviceByKey("nope"); err == nil {
		t.Fatal("want error deleting unknown key, got nil")
	}
}

func TestEnvBaseCRUD(t *testing.T) {
	t.Setenv("BARK_KEY", "env-key")
	t.Setenv("BARK_DEVICE_TOKEN", "env-token")

	db := NewEnvBase()
	tok, err := db.DeviceTokenByKey("env-key")
	if err != nil {
		t.Fatalf("DeviceTokenByKey failed: %v", err)
	}
	if tok != "env-token" {
		t.Fatalf("want token %q, got %q", "env-token", tok)
	}

	// EnvBase mirrors the configured key/token and rejects anything else.
	if _, err := db.DeviceTokenByKey("other"); err == nil {
		t.Fatal("want error for mismatched key, got nil")
	}

	saved, err := db.SaveDeviceTokenByKey("env-key", "env-token")
	if err != nil || saved != "env-key" {
		t.Fatalf("SaveDeviceTokenByKey want (env-key,nil), got (%q,%v)", saved, err)
	}

	// EnvBase does not support deletes.
	if err := db.DeleteDeviceByKey("env-key"); err == nil {
		t.Fatal("want error from DeleteDeviceByKey, got nil")
	}
}

// TestBboltCRUD validates the default on-disk database against a scratch dir.
// Bbolt is a process-wide singleton here, so this is a single combined flow.
func TestBboltCRUD(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	key, err := db.SaveDeviceTokenByKey("", "token-device-1")
	if err != nil {
		t.Fatalf("SaveDeviceTokenByKey failed: %v", err)
	}
	if key == "" {
		t.Fatal("expected newly generated key for empty-key registration")
	}
	generated := key

	// SaveDeviceTokenByKey on an existing key keeps the key.
	key2, err := db.SaveDeviceTokenByKey(generated, "token-device-2")
	if err != nil {
		t.Fatalf("SaveDeviceTokenByKey(update) failed: %v", err)
	}
	if key2 != generated {
		t.Fatalf("want key preserved %q, got %q", generated, key2)
	}

	tok, err := db.DeviceTokenByKey(generated)
	if err != nil {
		t.Fatalf("DeviceTokenByKey failed: %v", err)
	}
	if tok != "token-device-2" {
		t.Fatalf("want updated token %q, got %q", "token-device-2", tok)
	}

	if n, _ := db.CountAll(); n != 1 {
		t.Fatalf("want CountAll=1, got %d", n)
	}

	if err := db.DeleteDeviceByKey(generated); err != nil {
		t.Fatalf("DeleteDeviceByKey failed: %v", err)
	}
	if _, err := db.DeviceTokenByKey(generated); err == nil {
		t.Fatal("want error after delete, got nil")
	}
}

func TestBboltErrors(t *testing.T) {
	db := NewBboltdb(t.TempDir())
	if _, err := db.DeviceTokenByKey("does-not-exist"); err == nil {
		t.Fatal("want error for missing key, got nil")
	}
}

// TestBboltHonorsProvidedKey guards the "restore a known key" semantics: passing
// a non-empty key must always be preserved (matching MySQL), never silently
// replaced by a newly generated key, even when the key is not already present.
func TestBboltHonorsProvidedKey(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	const provided = "restore-me-key"

	key, err := db.SaveDeviceTokenByKey(provided, "token-v1")
	if err != nil {
		t.Fatalf("SaveDeviceTokenByKey failed: %v", err)
	}
	if key != provided {
		t.Fatalf("want key preserved %q, got %q", provided, key)
	}

	tok, err := db.DeviceTokenByKey(provided)
	if err != nil {
		t.Fatalf("DeviceTokenByKey failed: %v", err)
	}
	if tok != "token-v1" {
		t.Fatalf("want token %q, got %q", "token-v1", tok)
	}
}

// TestBbolt_DeviceInfoByKey_Harmony verifies that the new DeviceInfo API
// correctly stores and retrieves platform information for HarmonyOS devices.
func TestBbolt_DeviceInfoByKey_Harmony(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	// Save a HarmonyOS device
	info := &DeviceInfo{
		Key:      "harmony-key-001",
		Token:    "HARMONY_TOKEN_12345",
		Platform: "harmony",
	}

	key, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}
	if key != "harmony-key-001" {
		t.Fatalf("want key 'harmony-key-001', got %q", key)
	}

	// Retrieve and verify
	retrieved, err := db.DeviceInfoByKey("harmony-key-001")
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "harmony" {
		t.Fatalf("want platform 'harmony', got %q", retrieved.Platform)
	}
	if retrieved.Token != "HARMONY_TOKEN_12345" {
		t.Fatalf("want token 'HARMONY_TOKEN_12345', got %q", retrieved.Token)
	}
	if retrieved.Key != "harmony-key-001" {
		t.Fatalf("want key 'harmony-key-001', got %q", retrieved.Key)
	}
}

// TestBbolt_DeviceInfoByKey_DefaultPlatform verifies that devices saved
// without an explicit platform default to "ios".
func TestBbolt_DeviceInfoByKey_DefaultPlatform(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	info := &DeviceInfo{
		Key:   "ios-default-key",
		Token: "IOS_TOKEN_67890",
	}

	key, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}
	if key != "ios-default-key" {
		t.Fatalf("want key 'ios-default-key', got %q", key)
	}

	retrieved, err := db.DeviceInfoByKey("ios-default-key")
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "ios" {
		t.Fatalf("want default platform 'ios', got %q", retrieved.Platform)
	}
	if retrieved.Token != "IOS_TOKEN_67890" {
		t.Fatalf("want token 'IOS_TOKEN_67890', got %q", retrieved.Token)
	}
}

// TestBbolt_DeviceInfoByKey_LegacyCompatibility verifies that the new
// DeviceInfoByKey method falls back to legacy token storage for devices
// that were registered before the platform-aware API was introduced.
func TestBbolt_DeviceInfoByKey_LegacyCompatibility(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	// Save using the legacy API (no platform info)
	_, err := db.SaveDeviceTokenByKey("legacy-key", "legacy-token")
	if err != nil {
		t.Fatalf("SaveDeviceTokenByKey failed: %v", err)
	}

	// Retrieve using new API - should fall back to legacy and default to "ios"
	retrieved, err := db.DeviceInfoByKey("legacy-key")
	if err != nil {
		t.Fatalf("DeviceInfoByKey should fall back to legacy, got error: %v", err)
	}
	if retrieved.Platform != "ios" {
		t.Fatalf("want legacy fallback platform 'ios', got %q", retrieved.Platform)
	}
	if retrieved.Token != "legacy-token" {
		t.Fatalf("want token 'legacy-token', got %q", retrieved.Token)
	}
	if retrieved.Key != "legacy-key" {
		t.Fatalf("want key 'legacy-key', got %q", retrieved.Key)
	}
}

// TestBbolt_DeviceInfoByKey_NotFound verifies that requesting a
// non-existent device returns an error.
func TestBbolt_DeviceInfoByKey_NotFound(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	_, err := db.DeviceInfoByKey("non-existent-key")
	if err == nil {
		t.Fatal("want error for non-existent key, got nil")
	}
}

// TestBbolt_DeviceInfoByKey_UpdateExisting verifies that updating an
// existing device's info preserves the key but updates token and platform.
func TestBbolt_DeviceInfoByKey_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	// Save initial info
	info := &DeviceInfo{
		Key:      "update-key",
		Token:    "old-token",
		Platform: "ios",
	}
	_, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}

	// Update to HarmonyOS with new token
	updated := &DeviceInfo{
		Key:      "update-key",
		Token:    "new-harmony-token",
		Platform: "harmony",
	}
	key, err := db.SaveDeviceInfo(updated)
	if err != nil {
		t.Fatalf("SaveDeviceInfo(update) failed: %v", err)
	}
	if key != "update-key" {
		t.Fatalf("want key 'update-key', got %q", key)
	}

	// Verify the update
	retrieved, err := db.DeviceInfoByKey("update-key")
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "harmony" {
		t.Fatalf("want platform 'harmony', got %q", retrieved.Platform)
	}
	if retrieved.Token != "new-harmony-token" {
		t.Fatalf("want token 'new-harmony-token', got %q", retrieved.Token)
	}
}

// TestBbolt_DeviceInfoByKey_AutoGenerateKey verifies that SaveDeviceInfo
// with an empty key auto-generates a new key.
func TestBbolt_DeviceInfoByKey_AutoGenerateKey(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	info := &DeviceInfo{
		Token:    "auto-key-token",
		Platform: "harmony",
	}

	key, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}
	if key == "" {
		t.Fatal("want auto-generated key, got empty string")
	}

	// Verify it's retrievable
	retrieved, err := db.DeviceInfoByKey(key)
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "harmony" {
		t.Fatalf("want platform 'harmony', got %q", retrieved.Platform)
	}
}

// TestBbolt_DeviceInfoByKey_AllPlatforms verifies that both iOS and
// HarmonyOS devices can coexist in the database.
func TestBbolt_DeviceInfoByKey_AllPlatforms(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	// Get initial count
	initialCount, _ := db.CountAll()

	iosInfo := &DeviceInfo{
		Key:      "ios-device",
		Token:    "ios-token",
		Platform: "ios",
	}
	harmonyInfo := &DeviceInfo{
		Key:      "harmony-device",
		Token:    "harmony-token",
		Platform: "harmony",
	}

	_, err := db.SaveDeviceInfo(iosInfo)
	if err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	_, err = db.SaveDeviceInfo(harmonyInfo)
	if err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	// Retrieve both
	iosRetrieved, err := db.DeviceInfoByKey("ios-device")
	if err != nil {
		t.Fatalf("DeviceInfoByKey(ios) failed: %v", err)
	}
	if iosRetrieved.Platform != "ios" || iosRetrieved.Token != "ios-token" {
		t.Fatalf("ios device mismatch: platform=%q token=%q", iosRetrieved.Platform, iosRetrieved.Token)
	}

	harmonyRetrieved, err := db.DeviceInfoByKey("harmony-device")
	if err != nil {
		t.Fatalf("DeviceInfoByKey(harmony) failed: %v", err)
	}
	if harmonyRetrieved.Platform != "harmony" || harmonyRetrieved.Token != "harmony-token" {
		t.Fatalf("harmony device mismatch: platform=%q token=%q", harmonyRetrieved.Platform, harmonyRetrieved.Token)
	}

	// Count should have increased by 2
	newCount, _ := db.CountAll()
	if newCount != initialCount+2 {
		t.Fatalf("want CountAll to increase by 2 (%d -> %d), got %d", initialCount, initialCount+2, newCount)
	}
}

// TestMemBase_DeviceInfoByKey_Harmony verifies that MemBase supports the
// new platform-aware API for HarmonyOS devices.
func TestMemBase_DeviceInfoByKey_Harmony(t *testing.T) {
	db := NewMemBase()

	info := &DeviceInfo{
		Key:      cacheKey,
		Token:    "MEM_HARMONY_TOKEN",
		Platform: "harmony",
	}

	key, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}
	if key != cacheKey {
		t.Fatalf("want key %q, got %q", cacheKey, key)
	}

	retrieved, err := db.DeviceInfoByKey(cacheKey)
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "harmony" {
		t.Fatalf("want platform 'harmony', got %q", retrieved.Platform)
	}
}

// TestMemBase_DeviceInfoByKey_NotFound verifies that MemBase returns an
// error for unknown keys via the new API.
func TestMemBase_DeviceInfoByKey_NotFound(t *testing.T) {
	db := NewMemBase()
	_, err := db.DeviceInfoByKey("unknown-key")
	if err == nil {
		t.Fatal("want error for unknown key, got nil")
	}
}

// TestEnvBase_DeviceInfoByKey verifies that EnvBase supports the
// new DeviceInfo API (with platform defaulting to "ios").
func TestEnvBase_DeviceInfoByKey(t *testing.T) {
	t.Setenv("BARK_KEY", "env-test-key")
	t.Setenv("BARK_DEVICE_TOKEN", "env-test-token")

	db := NewEnvBase()

	info := &DeviceInfo{
		Key:   "env-test-key",
		Token: "env-test-token",
	}

	key, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}
	if key != "env-test-key" {
		t.Fatalf("want key 'env-test-key', got %q", key)
	}

	retrieved, err := db.DeviceInfoByKey("env-test-key")
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "ios" {
		t.Fatalf("want platform 'ios', got %q", retrieved.Platform)
	}
	if retrieved.Token != "env-test-token" {
		t.Fatalf("want token 'env-test-token', got %q", retrieved.Token)
	}
}
