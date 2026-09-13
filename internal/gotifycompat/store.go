package gotifycompat

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Store persists messages and the client-token material for the
// gotify-compatible monitoring interface.
type Store interface {
	// Add persists a message, assigns a monotonic ID and returns it.
	Add(m *Message) (uint64, error)
	// UpsertByExtraID finds the first message (newest-first) whose extras.id
	// matches extraID AND belongs to device, overwrites its content in place
	// (preserving the bbolt ID), and returns (id, true, nil). If no match is
	// found it creates a new message like Add and returns (id, false, nil).
	// When extraID is empty it delegates to Add (no overwrite semantics).
	UpsertByExtraID(device, extraID string, m *Message) (uint64, bool, error)
	// Recent returns up to limit messages with ID < since, ordered by ID
	// descending (newest first). since==0 disables the filter.
	Recent(limit int, since uint64) ([]Message, error)
	// RecentByDevice is Recent filtered to a single device; device=="" returns
	// everything (same as Recent). limit<0 disables the cap (returns the full
	// device history); limit==0 returns an empty list.
	RecentByDevice(device string, limit int, since uint64) ([]Message, error)
	// SearchByDevice walks the device's full history matching keyword as a
	// case-insensitive substring of title or body (empty keyword matches all),
	// returning up to limit matches newest-first (limit<0 → all) together with
	// the total number of matches for the device, counted independently of
	// limit. since==0 disables the ID filter.
	SearchByDevice(device string, keyword string, limit int, since uint64) ([]Message, int, error)
	// ForEachByDevice walks the device's full history once (newest-first),
	// invoking fn for every message matching the device and keyword
	// (case-insensitive substring of title+body; empty keyword matches all)
	// with ID < since. It returns the total number of matches visited; an
	// error from fn aborts the walk and is propagated. Designed for streaming
	// exports: fn runs per message so the caller never holds the full set.
	ForEachByDevice(device string, keyword string, since uint64, fn func(Message) error) (int, error)
	// AfterByDevice returns up to limit messages of the device with ID > after,
	// ordered ascending (oldest first) so clients can replay incrementally.
	// hasMore reports whether newer messages remain for another page.
	// limit<=0 returns an empty page.
	AfterByDevice(device string, after uint64, limit int) (messages []Message, hasMore bool, err error)
	// RecentPageByDevice is RecentByDevice plus a hasMore flag reporting
	// whether older messages remain behind the returned page (ID < since
	// walk direction). limit<=0 returns an empty page.
	RecentPageByDevice(device string, limit int, since uint64) (messages []Message, hasMore bool, err error)
	// Delete removes the message with the given ID; the bool reports whether
	// it existed. The ID sequence is never reused.
	Delete(id uint64) (bool, error)
	// DeleteByDevice removes the message only when it belongs to the given
	// device; the bool reports whether such a message existed.
	DeleteByDevice(device string, id uint64) (bool, error)
	// DeleteAll removes every stored message.
	DeleteAll() error
	// DeleteAllByDevice removes every message that belongs to the device.
	DeleteAllByDevice(device string) error
	// DeletionsByDevice returns the device's deletion-log events with cursor
	// id > since (ascending, at most deletionPageLimit projected events).
	// since==0 is the first-sync baseline: empty arrays with the current
	// cursor. A cursor older than the device's oldest retained row yields a
	// reset page.
	DeletionsByDevice(device string, since uint64) (DeletionsPage, error)
	// ExpireMessages removes every message whose ttl deadline is not later
	// than now, recording a kind=1 tombstone per removed message in the same
	// transaction. It returns the number of removed messages.
	ExpireMessages(now time.Time) (int, error)
	// CleanupDeletions drops deletion-log rows older than the 30-day
	// retention window, inserting a kind=3 reset sentinel per affected
	// device before removing its rows. It returns the number of dropped rows.
	CleanupDeletions(now time.Time) (int, error)

	TokenHash() ([]byte, error)
	SetTokenHash(h []byte) error
	AutoToken() (string, error)
	SetAutoToken(t string) error

	Close() error
}

// bbolt-backed store persisted under <data>/gotify.db.
type bboltStore struct {
	db  *bolt.DB
	max int
}

const (
	bucketMessages = "messages"
	bucketMeta     = "meta"
	// bucketDeviceMessages is the (device_key, id) secondary index over
	// messages: key = length-prefixed device + big-endian id. It backs the
	// high-frequency ?after= incremental queries.
	bucketDeviceMessages = "dev_msgs"
	// bucketTTL maps message id -> unix-second expiry deadline.
	bucketTTL = "ttl"
	// bucketDeletions stores tombstone rows keyed by a global monotonic
	// cursor (the bucket sequence).
	bucketDeletions = "deletions"
	// bucketDeletionDevice indexes (device_key, cursor) for device-scoped
	// log reads (idx_del_dev_id equivalent).
	bucketDeletionDevice = "del_dev"
	// bucketDeletionMsg enforces at most one kind=1 row per
	// (device_key, message_id) (idx_del_dev_msg unique equivalent).
	bucketDeletionMsg = "del_dev_msg"
)

var (
	keyCount     = []byte("count")
	keyTokenHash = []byte("tokenHash")
	keyAutoToken = []byte("autoToken")
)

func openBboltStore(path string, max int) (*bboltStore, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{
			bucketMessages,
			bucketMeta,
			bucketDeviceMessages,
			bucketTTL,
			bucketDeletions,
			bucketDeletionDevice,
			bucketDeletionMsg,
		} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return migrateIndexes(tx)
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &bboltStore{db: db, max: max}, nil
}

// migrateIndexes backfills the (device_key,id) message index and the ttl
// deadline index for databases created before incremental sync existed. It
// runs inside the open transaction and is a no-op once the index is populated.
// TTL deadlines are reconstructed best-effort from the stored RFC3339 Date and
// the "ttl" extra; unparsable rows are simply skipped.
func migrateIndexes(tx *bolt.Tx) error {
	msgs := tx.Bucket([]byte(bucketMessages))
	idx := tx.Bucket([]byte(bucketDeviceMessages))
	ttls := tx.Bucket([]byte(bucketTTL))

	// Fast path: index already maintained (or an empty fresh database).
	if idx.Stats().KeyN != 0 || msgs.Stats().KeyN == 0 {
		return nil
	}
	c := msgs.Cursor()
	for k, raw := c.First(); k != nil; k, raw = c.Next() {
		id := bytesUint64(k)
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			continue // unreadable legacy rows cannot be indexed; leave in place
		}
		m.ID = id
		if err := idx.Put(deviceScopedKey(m.SourceDevice(), id), nil); err != nil {
			return err
		}
		if secs, ok := ttlFromExtras(m.Extras); ok && m.Date != "" {
			if published, err := time.Parse(time.RFC3339Nano, m.Date); err == nil {
				if err := ttls.Put(uint64Bytes(id), uint64Bytes(uint64(published.Unix()+secs))); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// devicePrefix encodes a device key as a 2-byte big-endian length followed by
// the raw key, so it can serve as a collision-proof bbolt key prefix.
func devicePrefix(device string) []byte {
	b := make([]byte, 2+len(device))
	binary.BigEndian.PutUint16(b, uint16(len(device)))
	copy(b[2:], device)
	return b
}

// deviceScopedKey builds a (device_key, uint64 id) composite key. The same
// encoding is used for the message index and the deletion indexes; only the
// bucket distinguishes them.
func deviceScopedKey(device string, id uint64) []byte {
	k := make([]byte, 2+len(device)+8)
	binary.BigEndian.PutUint16(k, uint16(len(device)))
	copy(k[2:], device)
	binary.BigEndian.PutUint64(k[2+len(device):], id)
	return k
}

// scopedID reads the trailing uint64 of a composite key produced by
// deviceScopedKey.
func scopedID(k []byte, prefixLen int) uint64 {
	if len(k) != prefixLen+8 {
		return 0
	}
	return bytesUint64(k[prefixLen:])
}

// addDeletionTx appends one tombstone row inside tx and maintains both
// secondary indexes. kind=1 rows are de-duplicated per (device, message_id)
// (INSERT OR IGNORE): the existing cursor is returned without a new row.
// CreatedAt defaults to now when zero.
func addDeletionTx(tx *bolt.Tx, rec deletionRecord) (uint64, error) {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	b := tx.Bucket([]byte(bucketDeletions))
	if rec.Kind == DeletionSingle {
		uniq := tx.Bucket([]byte(bucketDeletionMsg))
		uk := deviceScopedKey(rec.DeviceKey, rec.MessageID)
		if existing := uniq.Get(uk); len(existing) == 8 {
			return bytesUint64(existing), nil
		}
		id, err := b.NextSequence()
		if err != nil {
			return 0, err
		}
		return id, putDeletionTx(tx, rec, id, uk)
	}
	id, err := b.NextSequence()
	if err != nil {
		return 0, err
	}
	return id, putDeletionTx(tx, rec, id, nil)
}

// putDeletionTx writes the row body and indexes for an already-assigned id.
func putDeletionTx(tx *bolt.Tx, rec deletionRecord, id uint64, uniqueKey []byte) error {
	rec.ID = id
	raw, err := json.Marshal(&rec)
	if err != nil {
		return err
	}
	if err := tx.Bucket([]byte(bucketDeletions)).Put(uint64Bytes(id), raw); err != nil {
		return err
	}
	if err := tx.Bucket([]byte(bucketDeletionDevice)).Put(deviceScopedKey(rec.DeviceKey, id), nil); err != nil {
		return err
	}
	if uniqueKey != nil {
		if err := tx.Bucket([]byte(bucketDeletionMsg)).Put(uniqueKey, uint64Bytes(id)); err != nil {
			return err
		}
	}
	return nil
}

func (s *bboltStore) Close() error {
	return s.db.Close()
}

func uint64Bytes(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func bytesUint64(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (s *bboltStore) Add(m *Message) (uint64, error) {
	var id uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		mb := tx.Bucket([]byte(bucketMeta))

		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		id = seq
		m.ID = id

		v, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := b.Put(uint64Bytes(id), v); err != nil {
			return err
		}
		if err := indexMessagePut(tx, m); err != nil {
			return err
		}

		count := metaUint64(mb, keyCount) + 1
		if count > uint64(s.max) {
			evicted, err := evictOldestLocked(tx)
			if err != nil {
				return err
			}
			if evicted != nil {
				count--
			}
		}
		return mb.Put(keyCount, uint64Bytes(count))
	})
	return id, err
}

// indexMessagePut maintains the (device_key,id) secondary index and the ttl
// deadline index for a newly written message.
func indexMessagePut(tx *bolt.Tx, m *Message) error {
	if err := tx.Bucket([]byte(bucketDeviceMessages)).
		Put(deviceScopedKey(m.SourceDevice(), m.ID), nil); err != nil {
		return err
	}
	if m.ExpiresAt > 0 {
		if err := tx.Bucket([]byte(bucketTTL)).
			Put(uint64Bytes(m.ID), uint64Bytes(uint64(m.ExpiresAt))); err != nil {
			return err
		}
	}
	return nil
}

// indexMessageRemove drops the secondary index and ttl deadline entries of a
// deleted message.
func indexMessageRemove(tx *bolt.Tx, device string, id uint64) error {
	if err := tx.Bucket([]byte(bucketDeviceMessages)).
		Delete(deviceScopedKey(device, id)); err != nil {
		return err
	}
	return tx.Bucket([]byte(bucketTTL)).Delete(uint64Bytes(id))
}

// evictOldestLocked removes the globally oldest message inside an open
// write transaction and records its kind=1 tombstone in the same transaction.
// It returns the removed message (nil when the bucket was empty).
func evictOldestLocked(tx *bolt.Tx) (*Message, error) {
	b := tx.Bucket([]byte(bucketMessages))
	k, raw := b.Cursor().First()
	if k == nil {
		return nil, nil
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode evicted message %d: %w", bytesUint64(k), err)
	}
	m.ID = bytesUint64(k)
	if err := b.Delete(k); err != nil {
		return nil, err
	}
	if err := indexMessageRemove(tx, m.SourceDevice(), m.ID); err != nil {
		return nil, err
	}
	if _, err := addDeletionTx(tx, deletionRecord{
		DeviceKey: m.SourceDevice(),
		Kind:      DeletionSingle,
		MessageID: m.ID,
	}); err != nil {
		return nil, err
	}
	return &m, nil
}

// extraIDOf extracts the "id" field from a message's extras as a string.
// V2 JSON numeric ids arrive as float64 in extras; fmt.Sprint normalizes
// both string and numeric forms so the match is type-agnostic.
func extraIDOf(m *Message) string {
	if m.Extras == nil {
		return ""
	}
	v, ok := m.Extras["id"]
	if !ok {
		return ""
	}
	return fmt.Sprint(v)
}

func (s *bboltStore) UpsertByExtraID(device, extraID string, m *Message) (uint64, bool, error) {
	if extraID == "" || device == "" {
		id, err := s.Add(m)
		return id, false, err
	}
	var matchID uint64
	var found bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		mb := tx.Bucket([]byte(bucketMeta))
		c := b.Cursor()
		// Scan newest-first for an existing message matching device + extras.id.
		_, _ = c.Seek(uint64Bytes(^uint64(0)))
		k, v := c.Prev()
		for k != nil {
			var msg Message
			if err := json.Unmarshal(v, &msg); err == nil {
				msg.ID = bytesUint64(k)
				if msg.SourceDevice() == device && extraIDOf(&msg) == extraID {
					matchID = msg.ID
					found = true
					break
				}
			}
			k, v = c.Prev()
		}
		if found {
			// Overwrite the existing message content, preserving the bbolt ID
			// so stream subscribers and /message readers see an update, not a
			// new entry. The (device,id) index is unchanged; a fresh ttl
			// refreshes the deadline while an absent one keeps the old one.
			m.ID = matchID
			data, err := json.Marshal(m)
			if err != nil {
				return err
			}
			if err := b.Put(uint64Bytes(matchID), data); err != nil {
				return err
			}
			if m.ExpiresAt > 0 {
				if err := tx.Bucket([]byte(bucketTTL)).
					Put(uint64Bytes(matchID), uint64Bytes(uint64(m.ExpiresAt))); err != nil {
					return err
				}
			}
			return nil
		}
		// Not found: create a new message (same logic as Add).
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		m.ID = seq
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := b.Put(uint64Bytes(seq), data); err != nil {
			return err
		}
		if err := indexMessagePut(tx, m); err != nil {
			return err
		}
		count := metaUint64(mb, keyCount) + 1
		if count > uint64(s.max) {
			evicted, err := evictOldestLocked(tx)
			if err != nil {
				return err
			}
			if evicted != nil {
				count--
			}
		}
		return mb.Put(keyCount, uint64Bytes(count))
	})
	if found {
		return matchID, true, err
	}
	return m.ID, false, err
}

func (s *bboltStore) Recent(limit int, since uint64) ([]Message, error) {
	return s.RecentByDevice("", limit, since)
}

func (s *bboltStore) RecentByDevice(device string, limit int, since uint64) ([]Message, error) {
	if limit == 0 {
		return []Message{}, nil
	}
	capHint := limit
	if capHint < 0 || capHint > 512 {
		capHint = 64
	}
	out := make([]Message, 0, capHint)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		c := b.Cursor()
		// Position just past the last key, then step back onto the newest.
		_, _ = c.Seek(uint64Bytes(^uint64(0)))
		k, _ := c.Prev()
		for (limit < 0 || len(out) < limit) && k != nil {
			id := bytesUint64(k)
			if since != 0 && id >= since {
				k, _ = c.Prev()
				continue
			}
			var m Message
			if err := json.Unmarshal(b.Get(k), &m); err != nil {
				return err
			}
			m.ID = id
			if device == "" || m.SourceDevice() == device {
				out = append(out, m)
			}
			k, _ = c.Prev()
		}
		return nil
	})
	return out, err
}

// RecentPageByDevice walks the newest-first history like RecentByDevice but
// fetches one extra row to report whether older messages remain behind the
// page (the legacy since-pagination direction).
func (s *bboltStore) RecentPageByDevice(device string, limit int, since uint64) ([]Message, bool, error) {
	if limit <= 0 {
		return []Message{}, false, nil
	}
	out := make([]Message, 0, min(limit+1, 201))
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		c := b.Cursor()
		_, _ = c.Seek(uint64Bytes(^uint64(0)))
		k, _ := c.Prev()
		for len(out) < limit+1 && k != nil {
			id := bytesUint64(k)
			if since != 0 && id >= since {
				k, _ = c.Prev()
				continue
			}
			var m Message
			if err := json.Unmarshal(b.Get(k), &m); err != nil {
				return err
			}
			m.ID = id
			if device == "" || m.SourceDevice() == device {
				out = append(out, m)
			}
			k, _ = c.Prev()
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// AfterByDevice returns the device's messages with ID > after in ascending
// id order. It reads the (device_key,id) secondary index directly, so its
// cost is proportional to the page rather than the full history.
func (s *bboltStore) AfterByDevice(device string, after uint64, limit int) ([]Message, bool, error) {
	if limit <= 0 {
		return []Message{}, false, nil
	}
	out := make([]Message, 0, min(limit+1, 201))
	err := s.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketDeviceMessages))
		msgs := tx.Bucket([]byte(bucketMessages))
		prefix := devicePrefix(device)
		c := idx.Cursor()
		// Seek onto or just after (prefix, after); ids equal to after are
		// skipped (strictly greater-than semantics).
		seek := make([]byte, len(prefix)+8)
		copy(seek, prefix)
		binary.BigEndian.PutUint64(seek[len(prefix):], after)
		k, _ := c.Seek(seek)
		for len(out) < limit+1 && k != nil && bytes.HasPrefix(k, prefix) {
			id := scopedID(k, len(prefix))
			k, _ = c.Next()
			if id <= after {
				continue
			}
			raw := msgs.Get(uint64Bytes(id))
			if raw == nil {
				continue // index stale (defensive); message already gone
			}
			var m Message
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			m.ID = id
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// DeletionsByDevice implements the deletion-log query; see Store interface.
func (s *bboltStore) DeletionsByDevice(device string, since uint64) (DeletionsPage, error) {
	page := DeletionsPage{IDs: []uint64{}, Purges: []uint64{}}
	err := s.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketDeletionDevice))
		rows := tx.Bucket([]byte(bucketDeletions))
		prefix := devicePrefix(device)
		cur := idx.Cursor()

		// Oldest row of the device.
		firstK, _ := cur.Seek(prefix)
		if !bytes.HasPrefix(firstK, prefix) {
			return nil // device has never had a deletion event
		}
		minID := scopedID(firstK, len(prefix))

		// Newest row of the device: seek past the prefix range and step back.
		upper := make([]byte, len(prefix)+8)
		copy(upper, prefix)
		for i := len(prefix); i < len(upper); i++ {
			upper[i] = 0xff
		}
		lastK, _ := cur.Seek(upper)
		lastK, _ = cur.Prev()
		if !bytes.HasPrefix(lastK, prefix) {
			lastK = firstK
		}
		page.Cursor = scopedID(lastK, len(prefix))

		// First-sync baseline: expose only the current cursor.
		if since == 0 {
			return nil
		}
		// Cursor inside the retention gap: history cannot be reconstructed.
		if since < minID {
			page.Reset = true
			return nil
		}

		seek := make([]byte, len(prefix)+8)
		copy(seek, prefix)
		binary.BigEndian.PutUint64(seek[len(prefix):], since)
		k, _ := cur.Seek(seek)

		projected := 0
		var lastIncluded uint64
		for k != nil && bytes.HasPrefix(k, prefix) {
			id := scopedID(k, len(prefix))
			k, _ = cur.Next()
			if id <= since {
				continue
			}
			raw := rows.Get(uint64Bytes(id))
			if raw == nil {
				continue
			}
			var rec deletionRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			// Reset sentinels are gap markers, not client-applied events;
			// they do not consume the page budget.
			if rec.Kind == DeletionReset {
				continue
			}
			if projected == deletionPageLimit {
				page.HasMore = true
				page.Cursor = lastIncluded
				return nil
			}
			switch rec.Kind {
			case DeletionSingle:
				page.IDs = append(page.IDs, rec.MessageID)
			case DeletionPurge:
				page.Purges = append(page.Purges, rec.Ceiling)
			}
			projected++
			lastIncluded = id
		}
		return nil
	})
	return page, err
}

// messageMatches reports whether the message matches the already-lowercased
// keyword as a substring of title or body; an empty keyword matches all.
func messageMatches(m *Message, lowerKeyword string) bool {
	if lowerKeyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(m.Title), lowerKeyword) ||
		strings.Contains(strings.ToLower(m.Message), lowerKeyword)
}

func (s *bboltStore) SearchByDevice(device string, keyword string, limit int, since uint64) ([]Message, int, error) {
	kw := strings.ToLower(keyword)
	capHint := limit
	if capHint < 0 || capHint > 512 {
		capHint = 64
	}
	out := make([]Message, 0, capHint)
	total := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		c := b.Cursor()
		_, _ = c.Seek(uint64Bytes(^uint64(0)))
		k, _ := c.Prev()
		for k != nil {
			id := bytesUint64(k)
			if since != 0 && id >= since {
				k, _ = c.Prev()
				continue
			}
			var m Message
			if err := json.Unmarshal(b.Get(k), &m); err != nil {
				return err
			}
			m.ID = id
			if device != "" && m.SourceDevice() != device {
				k, _ = c.Prev()
				continue
			}
			if !messageMatches(&m, kw) {
				k, _ = c.Prev()
				continue
			}
			total++
			if limit < 0 || len(out) < limit {
				out = append(out, m)
			}
			k, _ = c.Prev()
		}
		return nil
	})
	return out, total, err
}

func (s *bboltStore) ForEachByDevice(device string, keyword string, since uint64, fn func(Message) error) (int, error) {
	kw := strings.ToLower(keyword)
	total := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		c := b.Cursor()
		_, _ = c.Seek(uint64Bytes(^uint64(0)))
		k, _ := c.Prev()
		for k != nil {
			id := bytesUint64(k)
			if since != 0 && id >= since {
				k, _ = c.Prev()
				continue
			}
			var m Message
			if err := json.Unmarshal(b.Get(k), &m); err != nil {
				return err
			}
			m.ID = id
			if device != "" && m.SourceDevice() != device {
				k, _ = c.Prev()
				continue
			}
			if !messageMatches(&m, kw) {
				k, _ = c.Prev()
				continue
			}
			total++
			if err := fn(m); err != nil {
				return err
			}
			k, _ = c.Prev()
		}
		return nil
	})
	return total, err
}

func (s *bboltStore) Delete(id uint64) (bool, error) {
	return s.deleteScoped("", id)
}

func (s *bboltStore) DeleteByDevice(device string, id uint64) (bool, error) {
	return s.deleteScoped(device, id)
}

// deleteScoped removes one message (optionally requiring device ownership);
// on success the removal, secondary-index cleanup and the kind=1 tombstone all
// commit in one transaction.
func (s *bboltStore) deleteScoped(device string, id uint64) (bool, error) {
	var existed bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		mb := tx.Bucket([]byte(bucketMeta))
		k := uint64Bytes(id)
		raw := b.Get(k)
		if raw == nil {
			return nil // not exists
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if device != "" && m.SourceDevice() != device {
			return nil // belongs to another device
		}
		existed = true
		if err := b.Delete(k); err != nil {
			return err
		}
		if err := indexMessageRemove(tx, m.SourceDevice(), id); err != nil {
			return err
		}
		if _, err := addDeletionTx(tx, deletionRecord{
			DeviceKey: m.SourceDevice(),
			Kind:      DeletionSingle,
			MessageID: id,
		}); err != nil {
			return err
		}
		count := metaUint64(mb, keyCount)
		if count > 0 {
			count--
		}
		return mb.Put(keyCount, uint64Bytes(count))
	})
	return existed, err
}

func (s *bboltStore) DeleteAll() error {
	return s.DeleteAllByDevice("")
}

// DeleteAllByDevice removes every message matching the device (or all when
// device==""). One kind=2 purge row per affected device is written in the
// same transaction, carrying the device's max id as ceiling.
func (s *bboltStore) DeleteAllByDevice(device string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		// Delete keys in place: recreating the bucket would reset its
		// NextSequence counter, breaking ID monotonicity. Keep the bucket so
		// IDs keep increasing across a bulk delete (never reused).
		b := tx.Bucket([]byte(bucketMessages))
		// Collect matching keys first: deleting while walking the cursor
		// shifts positions and would skip entries.
		type removed struct {
			id     uint64
			device string
		}
		var removedMsgs []removed
		ceiling := map[string]uint64{}
		c := b.Cursor()
		for k, raw := c.First(); k != nil; k, raw = c.Next() {
			var m Message
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			m.ID = bytesUint64(k)
			if device != "" && m.SourceDevice() != device {
				continue
			}
			dev := m.SourceDevice()
			removedMsgs = append(removedMsgs, removed{id: m.ID, device: dev})
			if m.ID > ceiling[dev] {
				ceiling[dev] = m.ID
			}
		}
		if len(removedMsgs) == 0 {
			return nil
		}
		for _, r := range removedMsgs {
			if err := b.Delete(uint64Bytes(r.id)); err != nil {
				return err
			}
			if err := indexMessageRemove(tx, r.device, r.id); err != nil {
				return err
			}
		}
		// One purge row per affected device (deterministic order for tests).
		for _, dev := range sortedCeilingDevices(ceiling) {
			if _, err := addDeletionTx(tx, deletionRecord{
				DeviceKey: dev,
				Kind:      DeletionPurge,
				Ceiling:   ceiling[dev],
			}); err != nil {
				return err
			}
		}
		count := metaUint64(tx.Bucket([]byte(bucketMeta)), keyCount)
		if uint64(len(removedMsgs)) > count {
			return tx.Bucket([]byte(bucketMeta)).Put(keyCount, uint64Bytes(0))
		}
		return tx.Bucket([]byte(bucketMeta)).Put(keyCount, uint64Bytes(count-uint64(len(removedMsgs))))
	})
}

// sortedCeilingDevices returns the distinct devices of a purge map in
// ascending order so purge rows are written deterministically.
func sortedCeilingDevices(m map[string]uint64) []string {
	devs := make([]string, 0, len(m))
	for dev := range m {
		devs = append(devs, dev)
	}
	sort.Strings(devs)
	return devs
}

// ExpireMessages removes every message whose ttl deadline has elapsed.
// Candidate ids are collected first (bbolt forbids writes during ForEach);
// message removal, index/ttl cleanup and the kind=1 tombstones commit in one
// transaction, batched per deletionBatchSize ids to bound intermediate state.
func (s *bboltStore) ExpireMessages(now time.Time) (int, error) {
	deadline := uint64(now.Unix())
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		ttlb := tx.Bucket([]byte(bucketTTL))
		var due []uint64
		if err := ttlb.ForEach(func(k, v []byte) error {
			if len(v) == 8 && bytesUint64(v) <= deadline {
				due = append(due, bytesUint64(k))
			}
			return nil
		}); err != nil {
			return err
		}
		b := tx.Bucket([]byte(bucketMessages))
		mb := tx.Bucket([]byte(bucketMeta))
		count := metaUint64(mb, keyCount)
		for start := 0; start < len(due); start += deletionBatchSize {
			end := min(start+deletionBatchSize, len(due))
			for _, id := range due[start:end] {
				// The deadline index always goes away (it would be an orphan
				// if the message was removed through another path).
				if err := ttlb.Delete(uint64Bytes(id)); err != nil {
					return err
				}
				raw := b.Get(uint64Bytes(id))
				if raw == nil {
					continue
				}
				var m Message
				if err := json.Unmarshal(raw, &m); err != nil {
					return err
				}
				if err := b.Delete(uint64Bytes(id)); err != nil {
					return err
				}
				if err := tx.Bucket([]byte(bucketDeviceMessages)).
					Delete(deviceScopedKey(m.SourceDevice(), id)); err != nil {
					return err
				}
				if _, err := addDeletionTx(tx, deletionRecord{
					DeviceKey: m.SourceDevice(),
					Kind:      DeletionSingle,
					MessageID: id,
				}); err != nil {
					return err
				}
				removed++
				if count > 0 {
					count--
				}
			}
		}
		return mb.Put(keyCount, uint64Bytes(count))
	})
	return removed, err
}

// CleanupDeletions drops deletion rows older than deletionRetention. For each
// affected device a kind=3 reset sentinel is inserted first, so the device's
// oldest surviving row always marks the unrecoverable gap.
func (s *bboltStore) CleanupDeletions(now time.Time) (int, error) {
	cutoff := now.Add(-deletionRetention).Unix()
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		rows := tx.Bucket([]byte(bucketDeletions))
		devIdx := tx.Bucket([]byte(bucketDeletionDevice))
		uniq := tx.Bucket([]byte(bucketDeletionMsg))

		// Cursor order equals created_at order (ids are time-monotonic), so
		// the walk stops at the first fresh row.
		var old []deletionRecord
		c := rows.Cursor()
		for k, raw := c.First(); k != nil; k, raw = c.Next() {
			var rec deletionRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			rec.ID = bytesUint64(k)
			if rec.CreatedAt >= cutoff {
				break
			}
			old = append(old, rec)
		}
		if len(old) == 0 {
			return nil
		}
		affected := map[string]struct{}{}
		for _, rec := range old {
			affected[rec.DeviceKey] = struct{}{}
		}
		// Insert the reset sentinel before dropping old rows.
		devs := make([]string, 0, len(affected))
		for dev := range affected {
			devs = append(devs, dev)
		}
		sort.Strings(devs)
		for _, dev := range devs {
			if _, err := addDeletionTx(tx, deletionRecord{
				DeviceKey: dev,
				Kind:      DeletionReset,
				CreatedAt: now.Unix(),
			}); err != nil {
				return err
			}
		}
		for _, rec := range old {
			k := uint64Bytes(rec.ID)
			if err := rows.Delete(k); err != nil {
				return err
			}
			if err := devIdx.Delete(deviceScopedKey(rec.DeviceKey, rec.ID)); err != nil {
				return err
			}
			if rec.Kind == DeletionSingle {
				if err := uniq.Delete(deviceScopedKey(rec.DeviceKey, rec.MessageID)); err != nil {
					return err
				}
			}
			removed++
		}
		return nil
	})
	return removed, err
}

func (s *bboltStore) TokenHash() ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketMeta)).Get(keyTokenHash)
		out = append(out, v...)
		return nil
	})
	return out, err
}

func (s *bboltStore) SetTokenHash(h []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketMeta)).Put(keyTokenHash, h)
	})
}

func (s *bboltStore) AutoToken() (string, error) {
	var out string
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketMeta)).Get(keyAutoToken)
		out = string(v)
		return nil
	})
	return out, err
}

func (s *bboltStore) SetAutoToken(t string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketMeta)).Put(keyAutoToken, []byte(t))
	})
}

func metaUint64(b *bolt.Bucket, key []byte) uint64 {
	v := b.Get(key)
	if len(v) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

// memoryStore is the degraded (non-persistent) fallback used when the data
// directory is unavailable. IDs remain monotonic for the lifetime of the
// process; the bridge tolerates an ID reset after a restart. It mirrors the
// bbolt store's secondary state (ttl deadlines, deletion log).
type memoryStore struct {
	mu      sync.Mutex
	seq     uint64
	max     int
	msgs    map[uint64]Message
	order   []uint64
	expires map[uint64]int64
	delSeq  uint64
	dels    []deletionRecord
	delMsg  map[string]map[uint64]uint64 // device -> message id -> cursor
}

func newMemoryStore(max int) *memoryStore {
	return &memoryStore{
		max:     max,
		msgs:    make(map[uint64]Message),
		expires: make(map[uint64]int64),
		delMsg:  make(map[string]map[uint64]uint64),
	}
}

func (s *memoryStore) Close() error { return nil }

// addDeletionLocked appends a tombstone row under the lock, de-duplicating
// kind=1 rows per (device, message id). It returns the cursor of the row
// (existing or new).
func (s *memoryStore) addDeletionLocked(rec deletionRecord) uint64 {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	if rec.Kind == DeletionSingle {
		if set := s.delMsg[rec.DeviceKey]; set != nil {
			if existing, ok := set[rec.MessageID]; ok {
				return existing
			}
		}
	}
	s.delSeq++
	rec.ID = s.delSeq
	s.dels = append(s.dels, rec)
	if rec.Kind == DeletionSingle {
		set := s.delMsg[rec.DeviceKey]
		if set == nil {
			set = make(map[uint64]uint64)
			s.delMsg[rec.DeviceKey] = set
		}
		set[rec.MessageID] = rec.ID
	}
	return rec.ID
}

// removeLocked deletes the message, its order entry and its ttl deadline.
func (s *memoryStore) removeLocked(id uint64) {
	delete(s.msgs, id)
	delete(s.expires, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// evictOldestLocked drops the globally oldest message and records its
// kind=1 tombstone.
func (s *memoryStore) evictOldestLocked() {
	if len(s.order) == 0 {
		return
	}
	old := s.order[0]
	m := s.msgs[old]
	dev := m.SourceDevice()
	s.removeLocked(old)
	s.addDeletionLocked(deletionRecord{
		DeviceKey: dev,
		Kind:      DeletionSingle,
		MessageID: old,
	})
}

func (s *memoryStore) Add(m *Message) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := s.seq
	m.ID = id
	s.msgs[id] = *m
	s.order = append(s.order, id)
	if m.ExpiresAt > 0 {
		s.expires[id] = m.ExpiresAt
	}
	if len(s.order) > s.max {
		s.evictOldestLocked()
	}
	return id, nil
}

func (s *memoryStore) UpsertByExtraID(device, extraID string, m *Message) (uint64, bool, error) {
	if extraID == "" || device == "" {
		id, err := s.Add(m)
		return id, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Scan newest-first for an existing message matching device + extras.id.
	for i := len(s.order) - 1; i >= 0; i-- {
		id := s.order[i]
		msg := s.msgs[id]
		if msg.SourceDevice() == device && extraIDOf(&msg) == extraID {
			m.ID = id
			s.msgs[id] = *m
			if m.ExpiresAt > 0 {
				s.expires[id] = m.ExpiresAt
			}
			return id, true, nil
		}
	}
	// Not found: create a new message (same logic as Add).
	s.seq++
	id := s.seq
	m.ID = id
	s.msgs[id] = *m
	s.order = append(s.order, id)
	if m.ExpiresAt > 0 {
		s.expires[id] = m.ExpiresAt
	}
	if len(s.order) > s.max {
		s.evictOldestLocked()
	}
	return id, false, nil
}

// RecentPageByDevice mirrors bbolt's newest-first paging plus hasMore.
func (s *memoryStore) RecentPageByDevice(device string, limit int, since uint64) ([]Message, bool, error) {
	if limit <= 0 {
		return []Message{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, 0, min(limit+1, 201))
	for i := len(s.order) - 1; i >= 0 && len(out) < limit+1; i-- {
		id := s.order[i]
		if since != 0 && id >= since {
			continue
		}
		m := s.msgs[id]
		if device == "" || m.SourceDevice() == device {
			out = append(out, m)
		}
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// AfterByDevice returns the device's messages with ID > after in ascending
// order plus a hasMore flag.
func (s *memoryStore) AfterByDevice(device string, after uint64, limit int) ([]Message, bool, error) {
	if limit <= 0 {
		return []Message{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, 0, min(limit+1, 201))
	for _, id := range s.order {
		if id <= after {
			continue
		}
		m := s.msgs[id]
		if device != "" && m.SourceDevice() != device {
			continue
		}
		out = append(out, m)
		if len(out) == limit+1 {
			break
		}
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// DeletionsByDevice mirrors the bbolt log query semantics.
func (s *memoryStore) DeletionsByDevice(device string, since uint64) (DeletionsPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := DeletionsPage{IDs: []uint64{}, Purges: []uint64{}}
	var minID, maxID uint64
	for _, r := range s.dels {
		if r.DeviceKey != device {
			continue
		}
		if minID == 0 || r.ID < minID {
			minID = r.ID
		}
		if r.ID > maxID {
			maxID = r.ID
		}
	}
	if maxID == 0 {
		return page, nil // device has never had an event
	}
	page.Cursor = maxID
	if since == 0 {
		return page, nil
	}
	if since < minID {
		page.Reset = true
		return page, nil
	}
	projected := 0
	var lastIncluded uint64
	for _, r := range s.dels { // insertion order = ascending cursor
		if r.DeviceKey != device || r.ID <= since {
			continue
		}
		if r.Kind == DeletionReset {
			continue
		}
		if projected == deletionPageLimit {
			page.HasMore = true
			page.Cursor = lastIncluded
			return page, nil
		}
		switch r.Kind {
		case DeletionSingle:
			page.IDs = append(page.IDs, r.MessageID)
		case DeletionPurge:
			page.Purges = append(page.Purges, r.Ceiling)
		}
		projected++
		lastIncluded = r.ID
	}
	return page, nil
}

// ExpireMessages removes all messages with an elapsed ttl deadline and
// records their kind=1 tombstones.
func (s *memoryStore) ExpireMessages(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline := now.Unix()
	var due []uint64
	for _, id := range s.order {
		if t, ok := s.expires[id]; ok && t <= deadline {
			due = append(due, id)
		}
	}
	removed := 0
	for _, id := range due {
		m, ok := s.msgs[id]
		if !ok {
			delete(s.expires, id) // orphan deadline index
			continue
		}
		dev := m.SourceDevice()
		s.removeLocked(id)
		s.addDeletionLocked(deletionRecord{
			DeviceKey: dev,
			Kind:      DeletionSingle,
			MessageID: id,
		})
		removed++
	}
	return removed, nil
}

// CleanupDeletions drops retention-expired log rows and inserts a kind=3
// reset sentinel per affected device first.
func (s *memoryStore) CleanupDeletions(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-deletionRetention).Unix()
	affected := map[string]struct{}{}
	fresh := make([]deletionRecord, 0, len(s.dels))
	removed := 0
	for _, r := range s.dels {
		if r.CreatedAt < cutoff {
			affected[r.DeviceKey] = struct{}{}
			if r.Kind == DeletionSingle {
				delete(s.delMsg[r.DeviceKey], r.MessageID)
			}
			removed++
			continue
		}
		fresh = append(fresh, r)
	}
	if removed == 0 {
		return 0, nil
	}
	s.dels = fresh
	for _, dev := range sortedCeilingDevices(ceilingSet(affected)) {
		s.addDeletionLocked(deletionRecord{
			DeviceKey: dev,
			Kind:      DeletionReset,
			CreatedAt: now.Unix(),
		})
	}
	return removed, nil
}

// ceilingSet adapts an "affected devices" set to the sortedCeilingDevices
// helper (ceiling value unused here).
func ceilingSet(set map[string]struct{}) map[string]uint64 {
	m := make(map[string]uint64, len(set))
	for dev := range set {
		m[dev] = 0
	}
	return m
}

func (s *memoryStore) Recent(limit int, since uint64) ([]Message, error) {
	return s.RecentByDevice("", limit, since)
}

func (s *memoryStore) RecentByDevice(device string, limit int, since uint64) ([]Message, error) {
	if limit == 0 {
		return []Message{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	capHint := limit
	if capHint < 0 || capHint > 512 {
		capHint = 64
	}
	out := make([]Message, 0, capHint)
	for i := len(s.order) - 1; i >= 0 && (limit < 0 || len(out) < limit); i-- {
		id := s.order[i]
		if since != 0 && id >= since {
			continue
		}
		m := s.msgs[id]
		if device == "" || m.SourceDevice() == device {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *memoryStore) SearchByDevice(device string, keyword string, limit int, since uint64) ([]Message, int, error) {
	kw := strings.ToLower(keyword)
	s.mu.Lock()
	defer s.mu.Unlock()
	capHint := limit
	if capHint < 0 || capHint > 512 {
		capHint = 64
	}
	out := make([]Message, 0, capHint)
	total := 0
	for i := len(s.order) - 1; i >= 0; i-- {
		id := s.order[i]
		if since != 0 && id >= since {
			continue
		}
		m := s.msgs[id]
		if device != "" && m.SourceDevice() != device {
			continue
		}
		if !messageMatches(&m, kw) {
			continue
		}
		total++
		if limit < 0 || len(out) < limit {
			out = append(out, m)
		}
	}
	return out, total, nil
}

func (s *memoryStore) ForEachByDevice(device string, keyword string, since uint64, fn func(Message) error) (int, error) {
	kw := strings.ToLower(keyword)
	// Snapshot candidate ids under the lock, then run fn outside it so a slow
	// streaming consumer never blocks concurrent Publish/Add.
	s.mu.Lock()
	var ids []uint64
	for i := len(s.order) - 1; i >= 0; i-- {
		id := s.order[i]
		if since != 0 && id >= since {
			continue
		}
		ids = append(ids, id)
	}
	s.mu.Unlock()

	total := 0
	for _, id := range ids {
		s.mu.Lock()
		m, ok := s.msgs[id]
		s.mu.Unlock()
		if !ok {
			continue // evicted concurrently
		}
		if device != "" && m.SourceDevice() != device {
			continue
		}
		if !messageMatches(&m, kw) {
			continue
		}
		total++
		if err := fn(m); err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *memoryStore) Delete(id uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteScopedLocked(id, ""), nil
}

// deleteScopedLocked removes the message when it exists and (when device !=
// "") belongs to that device, and records its kind=1 tombstone.
func (s *memoryStore) deleteScopedLocked(id uint64, device string) bool {
	m, ok := s.msgs[id]
	if !ok {
		return false
	}
	if device != "" && m.SourceDevice() != device {
		return false
	}
	dev := m.SourceDevice()
	s.removeLocked(id)
	s.addDeletionLocked(deletionRecord{
		DeviceKey: dev,
		Kind:      DeletionSingle,
		MessageID: id,
	})
	return true
}

func (s *memoryStore) DeleteByDevice(device string, id uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteScopedLocked(id, device), nil
}

func (s *memoryStore) DeleteAll() error {
	return s.DeleteAllByDevice("")
}

// DeleteAllByDevice wipes matching messages and writes one kind=2 purge row
// per affected device with its max id as ceiling.
func (s *memoryStore) DeleteAllByDevice(device string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ceiling := map[string]uint64{}
	var toRemove []uint64
	for id, m := range s.msgs {
		if device != "" && m.SourceDevice() != device {
			continue
		}
		dev := m.SourceDevice()
		toRemove = append(toRemove, id)
		if id > ceiling[dev] {
			ceiling[dev] = id
		}
	}
	if len(toRemove) == 0 {
		return nil
	}
	for _, id := range toRemove {
		s.removeLocked(id)
	}
	// seq intentionally keeps increasing so IDs stay monotonic across clears.
	for _, dev := range sortedCeilingDevices(ceiling) {
		s.addDeletionLocked(deletionRecord{
			DeviceKey: dev,
			Kind:      DeletionPurge,
			Ceiling:   ceiling[dev],
		})
	}
	return nil
}

func (s *memoryStore) TokenHash() ([]byte, error) {
	return nil, nil
}

func (s *memoryStore) SetTokenHash(h []byte) error {
	return fmt.Errorf("in-memory store does not persist tokens")
}

func (s *memoryStore) AutoToken() (string, error) {
	return "", nil
}

func (s *memoryStore) SetAutoToken(t string) error {
	return fmt.Errorf("in-memory store does not persist tokens")
}

func openStore(dataDir string, max int) (Store, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("empty data dir")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return openBboltStore(filepath.Join(dataDir, "gotify.db"), max)
}
