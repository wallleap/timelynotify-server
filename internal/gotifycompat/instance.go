package gotifycompat

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

var keyInstanceID = []byte("instanceID")

// newInstanceID creates a UUIDv4 without adding a dependency.
func newInstanceID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate instance id: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(id[0:4]), hex.EncodeToString(id[4:6]),
		hex.EncodeToString(id[6:8]), hex.EncodeToString(id[8:10]),
		hex.EncodeToString(id[10:16])), nil
}

// loadOrCreateInstanceID assigns a stable namespace to both new and legacy
// databases. The caller runs this in the database initialization transaction.
func loadOrCreateInstanceID(tx *bolt.Tx) (string, error) {
	meta := tx.Bucket([]byte(bucketMeta))
	if existing := meta.Get(keyInstanceID); len(existing) > 0 {
		return string(existing), nil
	}
	id, err := newInstanceID()
	if err != nil {
		return "", err
	}
	if err := meta.Put(keyInstanceID, []byte(id)); err != nil {
		return "", fmt.Errorf("persist instance id: %w", err)
	}
	return id, nil
}
