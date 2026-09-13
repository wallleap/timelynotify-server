package gotifycompat

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// DeletionKind mirrors the kind discriminator of the deletion log
// (gotify_message_deletion in SQL deployments; a dedicated bbolt bucket pair
// in this service).
type DeletionKind int

const (
	// DeletionSingle is the deletion of one message id (manual delete, TTL
	// expiry or capacity eviction).
	DeletionSingle DeletionKind = 1
	// DeletionPurge marks that a device's whole history was wiped; Ceiling is
	// the device's largest message id at wipe time.
	DeletionPurge DeletionKind = 2
	// DeletionReset is the retention sentinel inserted before old log rows
	// are dropped: every later event starts after an unrecoverable gap.
	DeletionReset DeletionKind = 3
)

const (
	// deletionPageLimit is the fixed page size of the deletion log; it is
	// intentionally independent of the message `limit` query parameter.
	deletionPageLimit = 500
	// deletionRetention is how long deletion events stay queryable. Older
	// rows are dropped by the maintenance sweep, which first inserts a
	// DeletionReset sentinel per affected device.
	deletionRetention = 30 * 24 * time.Hour
	// ttlSweepInterval is how often expired messages are collected.
	ttlSweepInterval = time.Minute
	// retentionSweepInterval is how often the deletion log is pruned.
	retentionSweepInterval = 24 * time.Hour
	// deletionBatchSize bounds how many tombstones are gathered per batch
	// during bulk eviction (TTL/capacity); all batches still commit in one
	// transaction so message removal and the log stay atomically visible.
	deletionBatchSize = 500
)

// DeletionsPage is one device-scoped page of the deletion log.
type DeletionsPage struct {
	// IDs lists the deleted message ids on this page (ascending).
	IDs []uint64 `json:"ids"`
	// Purges lists the purge ceilings on this page (ascending): the client
	// drops every locally cached message with id <= ceiling.
	Purges []uint64 `json:"purges"`
	// Cursor is the continuation point. While HasMore is true it equals the
	// last event id on this page (feed it back as ?deletedSince=); on the
	// final page it is the device's current maximum event id and should be
	// persisted for the next sync.
	Cursor uint64 `json:"cursor"`
	// HasMore reports that further events exist beyond this page.
	HasMore bool `json:"hasMore"`
	// Reset reports that the supplied cursor falls inside the retention gap:
	// deletion history cannot be reconstructed, so the client must clear its
	// local cache and re-seed from a fresh first page.
	Reset bool `json:"reset"`
}

// deletionRecord is one persisted tombstone row.
type deletionRecord struct {
	ID        uint64       `json:"id"`
	DeviceKey string       `json:"d"`
	Kind      DeletionKind `json:"k"`
	MessageID uint64       `json:"m,omitempty"`
	Ceiling   uint64       `json:"c,omitempty"`
	CreatedAt int64        `json:"t"`
}

// ValidateMessageListParams enforces the mutual-exclusion rules of the
// incremental-sync parameters on GET /:device_key/message:
//
//   - after cannot be combined with since or query;
//   - after and deletedSince cannot be used with the limit=-1 export.
//
// deletedSince may be combined with since and query.
func ValidateMessageListParams(afterPresent, sincePresent, queryPresent, deletedSincePresent bool, limit int) error {
	if afterPresent && sincePresent {
		return errors.New("after and since cannot be used together")
	}
	if afterPresent && queryPresent {
		return errors.New("after and query cannot be used together")
	}
	if afterPresent && limit == -1 {
		return errors.New("after is not allowed with limit=-1")
	}
	if deletedSincePresent && limit == -1 {
		return errors.New("deletedSince is not allowed with limit=-1")
	}
	return nil
}

// ParseCursor parses an after/deletedSince query value. An empty raw string
// means the parameter is absent. Non-numeric, negative and overflowing values
// are rejected so the handler can answer 400.
func ParseCursor(name, raw string) (value uint64, present bool, err error) {
	if raw == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, true, errors.New("invalid " + name + " parameter: " + strconv.Quote(raw))
	}
	return v, true, nil
}

// ttlFromExtras extracts a positive ttl (seconds) from the extras map,
// accepting JSON numbers (float64 over the wire), integers and numeric
// strings under a case-insensitive "ttl" key. Non-positive or unparsable
// values are ignored.
func ttlFromExtras(extras map[string]interface{}) (int64, bool) {
	for k, v := range extras {
		if !strings.EqualFold(k, "ttl") {
			continue
		}
		switch x := v.(type) {
		case float64:
			if x > 0 {
				return int64(x), true
			}
		case int:
			if x > 0 {
				return int64(x), true
			}
		case int64:
			if x > 0 {
				return x, true
			}
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}
