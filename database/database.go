package database

// DeviceInfo holds the full record for a registered device.
// Platform distinguishes between iOS (APNs token) and HarmonyOS (Push Kit token).
type DeviceInfo struct {
	Key      string
	Token    string
	Platform string // "ios" or "harmony"
}

// Database defines all of the db operation
type Database interface {
	CountAll() (int, error) //Get db records count
	
	// Legacy API (iOS-only, kept for backward compatibility)
	DeviceTokenByKey(key string) (string, error)            // Get specified device's token
	SaveDeviceTokenByKey(key, token string) (string, error) // Create or update specified devices's token (default platform "ios")
	
	// New API (platform-aware)
	DeviceInfoByKey(key string) (*DeviceInfo, error)                          // Get specified device's full info
	SaveDeviceInfo(info *DeviceInfo) (string, error)                          // Create or update specified device's info
	
	DeleteDeviceByKey(key string) error //Delete specified device
	Close() error                       //Close the database
}
