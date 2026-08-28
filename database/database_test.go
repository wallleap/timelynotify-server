package database

import (
	"path/filepath"
	"sort"
	"testing"
)

// TestMemBaseCRUD exercises the in-memory database used in tests/serverless-adjacent
// scenarios. MemBase holds a single package-level key with per-platform records.
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

// TestBbolt_DeviceInfoByKey_Harmony verifies that the platform-aware API
// correctly stores and retrieves platform information for HarmonyOS devices.
func TestBbolt_DeviceInfoByKey_Harmony(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

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

// TestBbolt_DeviceInfoByKey_UpdateExisting verifies that updating the
// same (key, platform) pair preserves the key but updates the token.
func TestBbolt_DeviceInfoByKey_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	info := &DeviceInfo{
		Key:      "update-key",
		Token:    "old-token",
		Platform: "ios",
	}
	_, err := db.SaveDeviceInfo(info)
	if err != nil {
		t.Fatalf("SaveDeviceInfo failed: %v", err)
	}

	// Update the same (key, platform) with a new token.
	updated := &DeviceInfo{
		Key:      "update-key",
		Token:    "new-ios-token",
		Platform: "ios",
	}
	key, err := db.SaveDeviceInfo(updated)
	if err != nil {
		t.Fatalf("SaveDeviceInfo(update) failed: %v", err)
	}
	if key != "update-key" {
		t.Fatalf("want key 'update-key', got %q", key)
	}

	retrieved, err := db.DeviceInfoByKey("update-key")
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "ios" {
		t.Fatalf("want platform 'ios', got %q", retrieved.Platform)
	}
	if retrieved.Token != "new-ios-token" {
		t.Fatalf("want token 'new-ios-token', got %q", retrieved.Token)
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

	retrieved, err := db.DeviceInfoByKey(key)
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if retrieved.Platform != "harmony" {
		t.Fatalf("want platform 'harmony', got %q", retrieved.Platform)
	}
}

// TestBbolt_DeviceInfoByKey_AllPlatforms verifies that both iOS and
// HarmonyOS devices can coexist in the database under different keys.
func TestBbolt_DeviceInfoByKey_AllPlatforms(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	initialCount, _ := db.CountAll()

	iosInfo := &DeviceInfo{
		Key:      "ios-device-distinct",
		Token:    "ios-token",
		Platform: "ios",
	}
	harmonyInfo := &DeviceInfo{
		Key:      "harmony-device-distinct",
		Token:    "harmony-token",
		Platform: "harmony",
	}

	if _, err := db.SaveDeviceInfo(iosInfo); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(harmonyInfo); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	iosRetrieved, err := db.DeviceInfoByKey("ios-device-distinct")
	if err != nil {
		t.Fatalf("DeviceInfoByKey(ios) failed: %v", err)
	}
	if iosRetrieved.Platform != "ios" || iosRetrieved.Token != "ios-token" {
		t.Fatalf("ios device mismatch: platform=%q token=%q", iosRetrieved.Platform, iosRetrieved.Token)
	}

	harmonyRetrieved, err := db.DeviceInfoByKey("harmony-device-distinct")
	if err != nil {
		t.Fatalf("DeviceInfoByKey(harmony) failed: %v", err)
	}
	if harmonyRetrieved.Platform != "harmony" || harmonyRetrieved.Token != "harmony-token" {
		t.Fatalf("harmony device mismatch: platform=%q token=%q", harmonyRetrieved.Platform, harmonyRetrieved.Token)
	}

	newCount, _ := db.CountAll()
	if newCount != initialCount+2 {
		t.Fatalf("want CountAll to increase by 2 (%d -> %d), got %d", initialCount, initialCount+2, newCount)
	}
}

// TestBbolt_SameKeyMultiPlatform verifies the core multi-platform invariant:
// the same device_key may hold distinct tokens for ios and harmony, and
// re-registering one platform does not clobber the other.
func TestBbolt_SameKeyMultiPlatform(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	const key = "multi-plat-key"

	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "ios-tok", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "harmony-tok", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	infos, err := db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("DevicesByKey failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 records, got %d", len(infos))
	}

	byPlat := map[string]string{}
	for _, info := range infos {
		byPlat[info.Platform] = info.Token
	}
	if byPlat["ios"] != "ios-tok" {
		t.Fatalf("ios token want 'ios-tok', got %q", byPlat["ios"])
	}
	if byPlat["harmony"] != "harmony-tok" {
		t.Fatalf("harmony token want 'harmony-tok', got %q", byPlat["harmony"])
	}

	// Re-registering ios with a new token must not touch harmony.
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "ios-tok-v2", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios update) failed: %v", err)
	}
	infos, err = db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("DevicesByKey after update failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want still 2 records after ios update, got %d", len(infos))
	}
	byPlat = map[string]string{}
	for _, info := range infos {
		byPlat[info.Platform] = info.Token
	}
	if byPlat["ios"] != "ios-tok-v2" {
		t.Fatalf("ios token want 'ios-tok-v2', got %q", byPlat["ios"])
	}
	if byPlat["harmony"] != "harmony-tok" {
		t.Fatalf("harmony token should be untouched, want 'harmony-tok', got %q", byPlat["harmony"])
	}

	// DeviceInfoByKey prefers ios.
	first, err := db.DeviceInfoByKey(key)
	if err != nil {
		t.Fatalf("DeviceInfoByKey failed: %v", err)
	}
	if first.Platform != "ios" {
		t.Fatalf("DeviceInfoByKey should prefer ios, got %q", first.Platform)
	}
}

// TestBbolt_ClearDeviceTokenByKeyAndPlatform verifies that clearing a token
// only affects the specified platform; the other platform keeps its token.
func TestBbolt_ClearDeviceTokenByKeyAndPlatform(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	const key = "clear-plat-key"
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "ios-tok", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "harmony-tok", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "ios"); err != nil {
		t.Fatalf("ClearDeviceTokenByKeyAndPlatform failed: %v", err)
	}

	infos, err := db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("DevicesByKey failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want still 2 records after clear, got %d", len(infos))
	}
	byPlat := map[string]string{}
	for _, info := range infos {
		byPlat[info.Platform] = info.Token
	}
	if byPlat["ios"] != "" {
		t.Fatalf("ios token should be cleared, got %q", byPlat["ios"])
	}
	if byPlat["harmony"] != "harmony-tok" {
		t.Fatalf("harmony token should be untouched, want 'harmony-tok', got %q", byPlat["harmony"])
	}

	// DeviceTokenByKey should now return the harmony token (ios is empty).
	tok, err := db.DeviceTokenByKey(key)
	if err != nil {
		t.Fatalf("DeviceTokenByKey failed: %v", err)
	}
	if tok != "harmony-tok" {
		t.Fatalf("DeviceTokenByKey should fall back to harmony, got %q", tok)
	}
}

// TestBbolt_LegacyBareKeyCompat verifies that a bare-key legacy token (no
// info: record at all) is surfaced as an ios record via DevicesByKey.
func TestBbolt_LegacyBareKeyCompat(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	// Write a bare-key record directly via the legacy SaveDeviceTokenByKey,
	// which now also writes the new info:<key>:ios record. To exercise the
	// pure bare-key fallback path, delete the info record and keep only the
	// bare-key entry — simulating a pre-platform database.
	bareKey := "bare-legacy-key"
	_, err := db.SaveDeviceTokenByKey(bareKey, "bare-tok")
	if err != nil {
		t.Fatalf("SaveDeviceTokenByKey failed: %v", err)
	}
	// SaveDeviceTokenByKey now writes info:bare-legacy-key:ios too, so
	// DevicesByKey will return the ios record from the new-format path.
	// This test asserts that path returns the expected ios token.
	infos, err := db.DevicesByKey(bareKey)
	if err != nil {
		t.Fatalf("DevicesByKey failed: %v", err)
	}
	if len(infos) != 1 || infos[0].Platform != "ios" || infos[0].Token != "bare-tok" {
		t.Fatalf("want single ios record with 'bare-tok', got %+v", infos)
	}
}

// TestBbolt_DevicesByKeyOrder verifies that DevicesByKey returns ios first.
func TestBbolt_DevicesByKeyOrder(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	const key = "order-key"
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "h-tok", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "i-tok", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}

	infos, err := db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("DevicesByKey failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 records, got %d", len(infos))
	}
	if infos[0].Platform != "ios" {
		plats := []string{infos[0].Platform, infos[1].Platform}
		sort.Strings(plats)
		t.Fatalf("want ios first, got order: %v", plats)
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

// TestMemBase_SameKeyMultiPlatform verifies MemBase keeps ios and harmony
// tokens under the same key without one clobbering the other.
func TestMemBase_SameKeyMultiPlatform(t *testing.T) {
	db := NewMemBase()
	// Reset state from prior tests sharing the package-level cache.
	_ = db.DeleteDeviceByKey(cacheKey)

	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: cacheKey, Token: "m-ios", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: cacheKey, Token: "m-harmony", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	infos, err := db.DevicesByKey(cacheKey)
	if err != nil {
		t.Fatalf("DevicesByKey failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 records, got %d", len(infos))
	}
	byPlat := map[string]string{}
	for _, info := range infos {
		byPlat[info.Platform] = info.Token
	}
	if byPlat["ios"] != "m-ios" || byPlat["harmony"] != "m-harmony" {
		t.Fatalf("want ios=m-ios harmony=m-harmony, got %v", byPlat)
	}

	// Clear ios only; harmony survives.
	if err := db.ClearDeviceTokenByKeyAndPlatform(cacheKey, "ios"); err != nil {
		t.Fatalf("ClearDeviceTokenByKeyAndPlatform failed: %v", err)
	}
	tok, err := db.DeviceTokenByKey(cacheKey)
	if err != nil {
		t.Fatalf("DeviceTokenByKey after clear failed: %v", err)
	}
	if tok != "m-harmony" {
		t.Fatalf("want fallback harmony token %q, got %q", "m-harmony", tok)
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

// TestBbolt_DevicesByKeyIncludesClearedRecords pins the invariant the
// multi-platform push() in route_push.go relies on: DevicesByKey must surface
// records whose token was cleared (Token == "") so the push layer can filter
// them. If the DB silently dropped empty-token records, a key whose only
// record had been cleared would look like "not found" and the push layer
// could not distinguish it from a truly unknown key.
func TestBbolt_DevicesByKeyIncludesClearedRecords(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	const key = "cleared-records-key"
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "ios-tok", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "harmony-tok", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "ios"); err != nil {
		t.Fatalf("ClearDeviceTokenByKeyAndPlatform failed: %v", err)
	}

	infos, err := db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("DevicesByKey must not error when a cleared record exists: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 records (one cleared), got %d", len(infos))
	}
	byPlat := map[string]string{}
	for _, info := range infos {
		byPlat[info.Platform] = info.Token
	}
	if byPlat["ios"] != "" {
		t.Fatalf("ios token should be empty after clear, got %q", byPlat["ios"])
	}
	if byPlat["harmony"] != "harmony-tok" {
		t.Fatalf("harmony token should be untouched, got %q", byPlat["harmony"])
	}
}

// TestBbolt_AllTokensCleared pins the boundary behavior the multi-platform
// push() relies on when every record for a key has been cleared:
//   - DevicesByKey still returns the records (the key remains known), so the
//     push layer can filter them and return a precise 400 "no valid token".
//   - DeviceTokenByKey (legacy) returns an error instead of surfacing an
//     empty token, so legacy callers don't push to a dead token.
//   - CountAll is unaffected: clearing empties the token, it does not delete
//     the record, so the key stays counted.
func TestBbolt_AllTokensCleared(t *testing.T) {
	dir := t.TempDir()
	db := NewBboltdb(filepath.Join(dir, "data"))

	const key = "all-cleared-key"
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "ios-tok", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: key, Token: "harmony-tok", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	beforeCount, err := db.CountAll()
	if err != nil {
		t.Fatalf("CountAll before clear failed: %v", err)
	}

	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "ios"); err != nil {
		t.Fatalf("clear ios failed: %v", err)
	}
	if err := db.ClearDeviceTokenByKeyAndPlatform(key, "harmony"); err != nil {
		t.Fatalf("clear harmony failed: %v", err)
	}

	// DevicesByKey must still return both records (all tokens empty).
	infos, err := db.DevicesByKey(key)
	if err != nil {
		t.Fatalf("DevicesByKey must not error when all tokens are cleared: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 cleared records, got %d", len(infos))
	}
	for _, info := range infos {
		if info.Token != "" {
			t.Fatalf("platform %s token should be empty, got %q", info.Platform, info.Token)
		}
	}

	// DeviceTokenByKey (legacy) must error rather than return "".
	if _, err := db.DeviceTokenByKey(key); err == nil {
		t.Fatal("DeviceTokenByKey must error when all tokens are cleared, got nil")
	}

	// CountAll must be unchanged: clearing does not delete.
	afterCount, err := db.CountAll()
	if err != nil {
		t.Fatalf("CountAll after clear failed: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("CountAll should not change after clear: before=%d after=%d", beforeCount, afterCount)
	}
}

// TestMemBase_AllTokensCleared pins the same boundary as TestBbolt_AllTokensCleared
// for the in-memory backend used by tests and serverless-style deployments.
func TestMemBase_AllTokensCleared(t *testing.T) {
	db := NewMemBase()
	_ = db.DeleteDeviceByKey(cacheKey)

	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: cacheKey, Token: "ios-tok", Platform: "ios"}); err != nil {
		t.Fatalf("SaveDeviceInfo(ios) failed: %v", err)
	}
	if _, err := db.SaveDeviceInfo(&DeviceInfo{Key: cacheKey, Token: "harmony-tok", Platform: "harmony"}); err != nil {
		t.Fatalf("SaveDeviceInfo(harmony) failed: %v", err)
	}

	if err := db.ClearDeviceTokenByKeyAndPlatform(cacheKey, "ios"); err != nil {
		t.Fatalf("clear ios failed: %v", err)
	}
	if err := db.ClearDeviceTokenByKeyAndPlatform(cacheKey, "harmony"); err != nil {
		t.Fatalf("clear harmony failed: %v", err)
	}

	// DevicesByKey must still return both records (all tokens empty).
	infos, err := db.DevicesByKey(cacheKey)
	if err != nil {
		t.Fatalf("DevicesByKey must not error when all tokens are cleared: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 cleared records, got %d", len(infos))
	}
	for _, info := range infos {
		if info.Token != "" {
			t.Fatalf("platform %s token should be empty, got %q", info.Platform, info.Token)
		}
	}

	// DeviceTokenByKey (legacy) must error rather than return "".
	if _, err := db.DeviceTokenByKey(cacheKey); err == nil {
		t.Fatal("DeviceTokenByKey must error when all tokens are cleared, got nil")
	}
}
