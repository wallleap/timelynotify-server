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
	infoPrefix = "info:" // -> info:<key>:<platform> (new) or info:<key> (legacy)
)

// platformSuffixes are the recognized platform identifiers that may appear
// after the final ':' of an info-key. Anything else is treated as a legacy
// info:<key> record whose platform defaults to "ios".
var platformSuffixes = []string{":ios", ":harmony"}

// infoStorageKey returns the canonical bbolt key for a (key, platform) pair.
func infoStorageKey(key, platform string) string {
	return infoPrefix + key + ":" + platform
}

// parseInfoKey splits a bbolt info-key into (key, platform, ok).
// ok is false when the key is not an info record. Legacy "info:<key>" records
// (no recognized platform suffix) return platform="ios" and ok=true.
func parseInfoKey(bucketKey string) (key, platform string, ok bool) {
	if !strings.HasPrefix(bucketKey, infoPrefix) {
		return "", "", false
	}
	rest := bucketKey[len(infoPrefix):]
	for _, suf := range platformSuffixes {
		if strings.HasSuffix(rest, suf) {
			return rest[:len(rest)-len(suf)], suf[1:], true
		}
	}
	// Legacy info:<key> with no platform suffix — default to ios.
	return rest, "ios", true
}

func NewBboltdb(dataDir string) Database {
	bboltSetup(dataDir)

	return &BboltDB{}
}

// CountAll counts unique (key, platform) pairs. Legacy info:<key> records
// (no platform suffix) are counted as (key, "ios") and deduplicated against
// any new-format info:<key>:ios record for the same key.
func (d *BboltDB) CountAll() (int, error) {
	seen := make(map[string]struct{})
	err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return fmt.Errorf("bucket not found")
		}
		return bucket.ForEach(func(k, v []byte) error {
			key, platform, ok := parseInfoKey(string(k))
			if !ok {
				return nil
			}
			seen[key+"\x00"+platform] = struct{}{}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	return len(seen), nil
}

// Close close the db file
func (d *BboltDB) Close() error {
	return db.Close()
}

// DeviceTokenByKey returns any non-empty token bound to the key, preferring
// "ios" when the key has records on multiple platforms. It is the legacy
// existence-check API.
func (d *BboltDB) DeviceTokenByKey(key string) (string, error) {
	var token string
	err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return fmt.Errorf("bucket not found")
		}

		// Prefer the new ios record.
		if bs := bucket.Get([]byte(infoStorageKey(key, "ios"))); bs != nil {
			var info DeviceInfo
			if err := json.Unmarshal(bs, &info); err == nil && info.Token != "" {
				token = info.Token
				return nil
			}
		}
		// Fall back to any other platform record.
		for _, p := range platformSuffixes {
			plat := p[1:]
			if plat == "ios" {
				continue
			}
			if bs := bucket.Get([]byte(infoStorageKey(key, plat))); bs != nil {
				var info DeviceInfo
				if err := json.Unmarshal(bs, &info); err == nil && info.Token != "" {
					token = info.Token
					return nil
				}
			}
		}
		// Legacy info:<key> record.
		if bs := bucket.Get([]byte(infoPrefix + key)); bs != nil {
			var info DeviceInfo
			if err := json.Unmarshal(bs, &info); err == nil && info.Token != "" {
				token = info.Token
				return nil
			}
		}
		// Legacy bare-key token record.
		if bs := bucket.Get([]byte(key)); bs != nil {
			t := string(bs)
			if len(t) == 0 {
				return fmt.Errorf("device token invalid")
			}
			token = t
			return nil
		}
		return fmt.Errorf("failed to get [%s] device token from database", key)
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// SaveDeviceTokenByKey is the legacy upsert (defaults to platform "ios").
func (d *BboltDB) SaveDeviceTokenByKey(key, deviceToken string) (string, error) {
	return d.SaveDeviceInfo(&DeviceInfo{
		Key:      key,
		Token:    deviceToken,
		Platform: "ios",
	})
}

// DevicesByKey returns every (key, platform) record bound to the key.
// Legacy info:<key> and bare-key records are surfaced as "ios" entries when
// no new-format ios record exists for the same key.
func (d *BboltDB) DevicesByKey(key string) ([]*DeviceInfo, error) {
	var infos []*DeviceInfo
	seen := make(map[string]struct{})
	err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return fmt.Errorf("bucket not found")
		}

		// New-format records: info:<key>:<platform>
		for _, p := range platformSuffixes {
			plat := p[1:]
			bs := bucket.Get([]byte(infoStorageKey(key, plat)))
			if bs == nil {
				continue
			}
			var info DeviceInfo
			if err := json.Unmarshal(bs, &info); err != nil {
				return fmt.Errorf("failed to unmarshal device info for key [%s] platform [%s]: %v", key, plat, err)
			}
			infos = append(infos, &info)
			seen[plat] = struct{}{}
		}

		// Legacy "info:<key>" record (pre-multi-platform schema, no platform
		// suffix in the bucket key). Surface it only when its platform
		// (defaulting to "ios" when the JSON body omits it) has not already
		// been delivered by a new-format record above; otherwise a stale
		// legacy record whose body carries Platform="harmony" would be
		// appended as a second harmony entry alongside the new-format
		// "info:<key>:harmony" record and the multi-platform fan-out would
		// push the same device twice.
		if bs := bucket.Get([]byte(infoPrefix + key)); bs != nil {
			var info DeviceInfo
			if err := json.Unmarshal(bs, &info); err == nil {
				info.Key = key
				if info.Platform == "" {
					info.Platform = "ios"
				}
				if _, dup := seen[info.Platform]; !dup {
					infos = append(infos, &info)
					seen[info.Platform] = struct{}{}
				}
			}
		}

		// Bare-key legacy token only if still no ios entry.
		if _, ok := seen["ios"]; !ok {
			if bs := bucket.Get([]byte(key)); bs != nil {
				if len(bs) == 0 {
					return fmt.Errorf("device token invalid")
				}
				infos = append(infos, &DeviceInfo{Key: key, Token: string(bs), Platform: "ios"})
				seen["ios"] = struct{}{}
			}
		}

		if len(infos) == 0 {
			return fmt.Errorf("failed to get [%s] device info from database", key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return infos, nil
}

// DeviceInfoByKey returns the first record for the key, preferring "ios".
// Deprecated: use DevicesByKey for multi-platform fan-out.
func (d *BboltDB) DeviceInfoByKey(key string) (*DeviceInfo, error) {
	infos, err := d.DevicesByKey(key)
	if err != nil {
		return nil, err
	}
	// Prefer ios.
	for _, info := range infos {
		if info.Platform == "ios" {
			return info, nil
		}
	}
	return infos[0], nil
}

// SaveDeviceInfo upserts by (key, platform). The same key may hold distinct
// tokens for distinct platforms; re-registering the same (key, platform)
// updates the token, while a different platform adds a new record without
// touching the others.
func (d *BboltDB) SaveDeviceInfo(info *DeviceInfo) (string, error) {
	if info.Platform == "" {
		info.Platform = "ios"
	}
	if info.Key == "" {
		info.Key = shortuuid.New()
	}

	err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))

		data, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("failed to marshal device info: %v", err)
		}
		if err := bucket.Put([]byte(infoStorageKey(info.Key, info.Platform)), data); err != nil {
			return err
		}

		// Remove any legacy "info:<key>" record (pre-multi-platform schema)
		// so a re-registration on any platform cannot leave a stale legacy
		// entry that DevicesByKey would double-count alongside the
		// new-format "info:<key>:<platform>" record just written above.
		_ = bucket.Delete([]byte(infoPrefix + info.Key))

		// For iOS, also maintain the legacy bare-key token record so old
		// deployments that only read "<key>" keep working.
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

// ClearDeviceTokenByKeyAndPlatform empties the token of the given (key,
// platform) pair. The record itself is kept so the key remains known and
// other platforms are untouched.
func (d *BboltDB) ClearDeviceTokenByKeyAndPlatform(key, platform string) error {
	if platform == "" {
		platform = "ios"
	}
	return db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))

		bs := bucket.Get([]byte(infoStorageKey(key, platform)))
		if bs == nil {
			// Nothing to clear; not an error — the key may simply never have
			// had this platform registered.
			return nil
		}
		var info DeviceInfo
		if err := json.Unmarshal(bs, &info); err != nil {
			return fmt.Errorf("failed to unmarshal device info for clear: %v", err)
		}
		info.Token = ""
		data, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("failed to marshal cleared device info: %v", err)
		}
		if err := bucket.Put([]byte(infoStorageKey(key, platform)), data); err != nil {
			return err
		}
		// Keep legacy bare-key in sync for ios.
		if platform == "ios" {
			_ = bucket.Put([]byte(key), []byte(""))
		}
		return nil
	})
}

// DeleteDeviceByKey deletes every (key, platform) record for the key,
// including legacy bare-key and info:<key> forms.
func (d *BboltDB) DeleteDeviceByKey(key string) error {
	return db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		for _, p := range platformSuffixes {
			plat := p[1:]
			if err := bucket.Delete([]byte(infoStorageKey(key, plat))); err != nil {
				return err
			}
		}
		_ = bucket.Delete([]byte(infoPrefix + key))
		return bucket.Delete([]byte(key))
	})
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
