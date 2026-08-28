package database

// DeviceInfo holds the full record for a registered device.
// Platform distinguishes between iOS (APNs token) and HarmonyOS (Push Kit token).
//
// A single device_key may have multiple DeviceInfo records, one per platform,
// so that pushing a key fans out to all bound platforms.
type DeviceInfo struct {
	Key      string
	Token    string
	Platform string // "ios" or "harmony"
}

// Database defines all of the db operation
type Database interface {
	CountAll() (int, error) //Get db records count

	// Legacy API (kept for backward compatibility and existence checks).
	// DeviceTokenByKey returns any non-empty token bound to the key (prefers
	// "ios" when multiple platforms exist). It is intended for existence
	// checks and legacy callers; new code should use DevicesByKey.
	DeviceTokenByKey(key string) (string, error)            // Get any token for the key
	SaveDeviceTokenByKey(key, token string) (string, error) // Legacy upsert, defaults to platform "ios"

	// Platform-aware API.
	//
	// DevicesByKey returns every (key, platform) record bound to the key.
	// A key without any record returns an error.
	DevicesByKey(key string) ([]*DeviceInfo, error)
	// DeviceInfoByKey returns the first record for the key (prefers "ios").
	// Deprecated: use DevicesByKey for multi-platform fan-out; this is kept
	// for callers that only need a single record (e.g. legacy checks).
	DeviceInfoByKey(key string) (*DeviceInfo, error)
	// SaveDeviceInfo upserts by (key, platform): the same key can hold
	// distinct tokens for distinct platforms. Re-registering the same
	// (key, platform) updates the token; re-registering the same key with
	// a different platform adds a new record without touching the others.
	SaveDeviceInfo(info *DeviceInfo) (string, error)
	// ClearDeviceTokenByKeyAndPlatform empties the token of the given
	// (key, platform) pair (used when a push reports the token invalid).
	// The record itself is kept so the key remains known and other
	// platforms are untouched. This is the targeted, multi-platform-safe
	// clear. The legacy SaveDeviceTokenByKey(key, "") only upserts the
	// ios record to an empty token (its platform default is "ios") and
	// does NOT touch other platforms — it must not be used to clear a
	// harmony token, or to "clear all platforms" (it can't).
	ClearDeviceTokenByKeyAndPlatform(key, platform string) error

	DeleteDeviceByKey(key string) error //Delete specified device (all platforms)
	Close() error                       //Close the database
}
