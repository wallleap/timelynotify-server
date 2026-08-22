package database

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lithammer/shortuuid/v3"
	"github.com/mritd/logger"
	"go.etcd.io/bbolt"
)

// BboltDB implement Database interface with ETCD's bbolt
type BboltDB struct {
}

var dbOnce sync.Once
var db *bbolt.DB

const (
	bucketName = "device"
)

func NewBboltdb(dataDir string) Database {
	bboltSetup(dataDir)

	return &BboltDB{}
}

// CountAll Fetch records count
func (d *BboltDB) CountAll() (int, error) {
	var keypairCount int
	err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return fmt.Errorf("bucket not found")
		}

		// Count info: prefixed entries which are the canonical representation.
		// Legacy entries (non-info: keys) are not counted to avoid double-counting
		// devices that were registered via both the old and new APIs.
		keypairCount = 0
		bucket.ForEach(func(k, v []byte) error {
			keyStr := string(k)
			if strings.HasPrefix(keyStr, "info:") {
				keypairCount++
			}
			return nil
		})
		return nil
	})

	if err != nil {
		return 0, err
	}

	return keypairCount, nil
}

// Close close the db file
func (d *BboltDB) Close() error {
	return db.Close()
}

// DeviceTokenByKey get device token of specified key (Legacy)
func (d *BboltDB) DeviceTokenByKey(key string) (string, error) {
	var token string
	err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		
		// First, try the new format and return token if found
		if bs := bucket.Get([]byte("info:" + key)); bs != nil {
			var info DeviceInfo
			if err := json.Unmarshal(bs, &info); err == nil && info.Token != "" {
				token = info.Token
				return nil
			}
		}
		
		// Fall back to legacy format
		if bs := bucket.Get([]byte(key)); bs != nil {
			token = string(bs)
			if len(token) == 0 {
				return fmt.Errorf("device token invalid")
			}
			return nil
		}
		
		return fmt.Errorf("failed to get [%s] device token from database", key)
	})
	if err != nil {
		return "", err
	}

	return token, nil
}

// SaveDeviceToken create or update device token of specified key (Legacy)
func (d *BboltDB) SaveDeviceTokenByKey(key, deviceToken string) (string, error) {
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))

		// A provided key is always honored (matching MySQL): a caller restoring
		// a known device key keeps that exact key, even if the record is not
		// currently present. Only an empty key is treated as a new registration.
		if key == "" {
			// Generate a new UUID as the deviceKey when a new device register
			key = shortuuid.New()
		}

		// update the deviceToken in legacy format
		if err := bucket.Put([]byte(key), []byte(deviceToken)); err != nil {
			return err
		}
		
		// Also update the new JSON format (defaulting to ios platform)
		info := DeviceInfo{
			Key:      key,
			Token:    deviceToken,
			Platform: "ios",
		}
		data, _ := json.Marshal(info)
		return bucket.Put([]byte("info:"+key), data)
	})

	if err != nil {
		return "", err
	}

	return key, nil
}

// DeviceInfoByKey get device info of specified key
func (d *BboltDB) DeviceInfoByKey(key string) (*DeviceInfo, error) {
	var info DeviceInfo
	err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		
		// Try to get the new JSON format first
		if bs := bucket.Get([]byte("info:" + key)); bs != nil {
			if err := json.Unmarshal(bs, &info); err != nil {
				return fmt.Errorf("failed to unmarshal device info for key [%s]: %v", key, err)
			}
			return nil
		}
		
		// Fall back to legacy token format
		if bs := bucket.Get([]byte(key)); bs != nil {
			if len(bs) == 0 {
				return fmt.Errorf("device token invalid")
			}
			info.Key = key
			info.Token = string(bs)
			info.Platform = "ios" // Legacy default
			return nil
		}
		
		return fmt.Errorf("failed to get [%s] device token from database", key)
	})
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// SaveDeviceInfo create or update device info
func (d *BboltDB) SaveDeviceInfo(info *DeviceInfo) (string, error) {
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		
		// Ensure platform is set
		if info.Platform == "" {
			info.Platform = "ios"
		}
		
		// Determine key
		if info.Key == "" {
			info.Key = shortuuid.New()
		}

		// Store as JSON
		data, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("failed to marshal device info: %v", err)
		}
		
		// Save to new bucket prefix
		if err := bucket.Put([]byte("info:"+info.Key), data); err != nil {
			return err
		}
		
		// Also update legacy format for backwards compatibility
		// (if it's an iOS device, update the old key)
		if info.Platform == "ios" {
			if err := bucket.Put([]byte(info.Key), []byte(info.Token)); err != nil {
				return err
			}
		}
		
		return nil
	})
	if err != nil {
		return "", err
	}
	return info.Key, nil
}

// DeleteDeviceByKey delete device of specified key
func (d *BboltDB) DeleteDeviceByKey(key string) error {
	err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		
		// Delete both formats
		if err := bucket.Delete([]byte(key)); err != nil {
			return err
		}
		return bucket.Delete([]byte("info:" + key))
	})
	return err
}

// bboltSetup setup the bbolt database
func bboltSetup(dataDir string) {
	dbOnce.Do(func() {
		logger.Infof("init database [%s]...", dataDir)
		if _, err := os.Stat(dataDir); os.IsNotExist(err) {
			if err = os.MkdirAll(dataDir, 0755); err != nil {
				logger.Fatalf("failed to create database storage dir(%s): %v", dataDir, err)
			}
		} else if err != nil {
			logger.Fatalf("failed to open database storage dir(%s): %v", dataDir, err)
		}

		bboltDB, err := bbolt.Open(filepath.Join(dataDir, "bark.db"), 0600, nil)
		if err != nil {
			logger.Fatalf("failed to create database file(%s): %v", filepath.Join(dataDir, "bark.db"), err)
		}
		err = bboltDB.Update(func(tx *bbolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
			return err
		})
		if err != nil {
			logger.Fatalf("failed to create database bucket: %v", err)
		}
		db = bboltDB
	})
}
