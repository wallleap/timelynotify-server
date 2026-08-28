package gotifycompat

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketMessages)); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists([]byte(bucketMeta))
		return err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &bboltStore{db: db, max: max}, nil
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

		count := metaUint64(mb, keyCount) + 1
		if count > uint64(s.max) {
			c := b.Cursor()
			if k, _ := c.First(); k != nil {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
			count--
		}
		return mb.Put(keyCount, uint64Bytes(count))
	})
	return id, err
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
	var existed bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMessages))
		mb := tx.Bucket([]byte(bucketMeta))
		k := uint64Bytes(id)
		if b.Get(k) == nil {
			return nil // not exists
		}
		existed = true
		if err := b.Delete(k); err != nil {
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

func (s *bboltStore) DeleteByDevice(device string, id uint64) (bool, error) {
	if device == "" {
		return s.Delete(id)
	}
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
		if m.SourceDevice() != device {
			return nil // belongs to another device
		}
		existed = true
		if err := b.Delete(k); err != nil {
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
// device=="").
func (s *bboltStore) DeleteAllByDevice(device string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		// Delete keys in place: recreating the bucket would reset its
		// NextSequence counter, breaking ID monotonicity. Keep the bucket so
		// IDs keep increasing across a bulk delete (never reused).
		b := tx.Bucket([]byte(bucketMessages))
		// Collect matching keys first: deleting while walking the cursor
		// shifts positions and would skip entries.
		var keys [][]byte
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if device != "" {
				var m Message
				if err := json.Unmarshal(b.Get(k), &m); err != nil {
					return err
				}
				if m.SourceDevice() != device {
					continue
				}
			}
			keys = append(keys, append([]byte{}, k...))
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		count := metaUint64(tx.Bucket([]byte(bucketMeta)), keyCount)
		if uint64(len(keys)) > count {
			return tx.Bucket([]byte(bucketMeta)).Put(keyCount, uint64Bytes(0))
		}
		return tx.Bucket([]byte(bucketMeta)).Put(keyCount, uint64Bytes(count-uint64(len(keys))))
	})
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
// process; the bridge tolerates an ID reset after a restart.
type memoryStore struct {
	mu    sync.Mutex
	seq   uint64
	max   int
	msgs  map[uint64]Message
	order []uint64
}

func newMemoryStore(max int) *memoryStore {
	return &memoryStore{max: max, msgs: make(map[uint64]Message)}
}

func (s *memoryStore) Close() error { return nil }

func (s *memoryStore) Add(m *Message) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := s.seq
	m.ID = id
	s.msgs[id] = *m
	s.order = append(s.order, id)
	if len(s.order) > s.max {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.msgs, old)
	}
	return id, nil
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
	return s.deleteLocked(id, ""), nil
}

// deleteLocked removes the message when it exists and (when device != "")
// belongs to that device.
func (s *memoryStore) deleteLocked(id uint64, device string) bool {
	m, ok := s.msgs[id]
	if !ok {
		return false
	}
	if device != "" && m.SourceDevice() != device {
		return false
	}
	delete(s.msgs, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return true
}

func (s *memoryStore) DeleteByDevice(device string, id uint64) (bool, error) {
	if device == "" {
		return s.Delete(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(id, device), nil
}

func (s *memoryStore) DeleteAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = make(map[uint64]Message)
	s.order = nil
	// seq intentionally keeps increasing so IDs stay monotonic across clears.
	return nil
}

func (s *memoryStore) DeleteAllByDevice(device string) error {
	if device == "" {
		return s.DeleteAll()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, m := range s.msgs {
		if m.SourceDevice() == device {
			s.deleteLocked(id, device)
		}
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
