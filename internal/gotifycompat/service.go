package gotifycompat

import (
	"fmt"
	"sync"
	"time"

	"github.com/mritd/logger"
)

// Config controls the gotify-compatible monitoring interface.
type Config struct {
	DataDir     string // where gotify.db lives; empty/unusable falls back to memory
	ClientToken string // optional pre-seeded client token; empty → auto-generated & persisted
	Version     string // value served on GET /version; empty → "dev"
	MaxMessages int    // retained message count; <=0 → defaultMaxMessages
}

const (
	defaultMaxMessages = 1000
	defaultAppID       = 1
)

// TokenSource describes where the client token came from; it drives what the
// startup log may safely reveal.
type TokenSource int

const (
	// TokenSourceOperator: operator-supplied (env var / flag); never logged.
	TokenSourceOperator TokenSource = iota
	// TokenSourceGenerated: auto-generated in this process; logged once.
	TokenSourceGenerated
	// TokenSourcePersisted: loaded from the store; not re-logged (would leak it
	// into every restart's logs).
	TokenSourcePersisted
)

// Service ties together token auth, message persistence and the WebSocket hub.
type Service struct {
	store       Store
	hub         *Hub
	tokenHash   []byte
	autoToken   string
	version     string
	tokenSource TokenSource

	stopCh    chan struct{}
	closeOnce sync.Once
}

// Init creates the service. It never fails outright for storage reasons:
// persistence is best-effort and degrades to an in-memory store when the data
// directory cannot be used.
func Init(cfg Config) (*Service, error) {
	max := cfg.MaxMessages
	if max <= 0 {
		max = defaultMaxMessages
	}

	store, err := openStore(cfg.DataDir, max)
	if err != nil {
		logger.Errorf("gotify compat: data store unavailable (%v); falling back to in-memory", err)
		store = newMemoryStore(max)
	}

	version := cfg.Version
	if version == "" {
		version = "latest"
	}

	svc := &Service{
		store:   store,
		hub:     newHub(),
		version: version,
		stopCh:  make(chan struct{}),
	}
	svc.startMaintenance()

	if cfg.ClientToken != "" {
		hash := hashToken(cfg.ClientToken)
		// best-effort persistence: an unusable store (degraded in-memory) must
		// never take down the monitoring interface.
		if err := store.SetTokenHash(hash); err != nil {
			logger.Warnf("monitor: could not persist token hash: %v", err)
		}
		// Clear any stale plaintext from a previously auto-generated token now
		// that the operator token takes over (validation only ever uses the
		// hash, but the file should not carry a dead secret).
		if ptErr := store.SetAutoToken(""); ptErr != nil {
			logger.Warnf("monitor: could not clear stale auto client token: %v", ptErr)
		}
		svc.tokenHash = hash
		svc.tokenSource = TokenSourceOperator
		// Operator-supplied token: never logged.
		return svc, nil
	}

	// Auto-generated token: prefer the persisted one for stability across restarts.
	hash, err := store.TokenHash()
	if err != nil {
		// Treat as missing and regenerate below: persistence is best-effort,
		// so a read failure must not take the interface down either.
		logger.Warnf("monitor: could not load token hash: %v", err)
	}
	if len(hash) == 0 {
		tok, err := generateToken()
		if err != nil {
			return nil, fmt.Errorf("generate token: %w", err)
		}
		h := hashToken(tok)
		// best-effort persistence of the plaintext for recovery & stability.
		// A failure (e.g. degraded in-memory store) is never fatal: the token
		// then lives for this process only and is re-generated (and re-logged)
		// on the next start — this also fixes the "first boot logs nothing"
		// symptom when the data dir is unusable.
		if err := store.SetTokenHash(h); err != nil {
			logger.Warnf("monitor: could not persist token hash: %v", err)
		}
		if ptErr := store.SetAutoToken(tok); ptErr != nil {
			logger.Warnf("monitor: could not persist auto client token: %v", ptErr)
		}
		hash = h
		svc.autoToken = tok
		svc.tokenSource = TokenSourceGenerated
	} else {
		svc.tokenSource = TokenSourcePersisted
	}
	svc.tokenHash = hash
	return svc, nil
}

// ValidateToken reports whether the supplied token matches the stored hash.
func (s *Service) ValidateToken(token string) bool {
	return tokensEqual(hashToken(token), s.tokenHash)
}

// ClientToken returns the plaintext token only when it was auto-generated in
// this process's lifetime (so it can be logged exactly once). It returns ""
// for operator-supplied or pre-existing tokens.
func (s *Service) ClientToken() string {
	return s.autoToken
}

// TokenSource reports where the client token came from.
func (s *Service) TokenSource() TokenSource {
	return s.tokenSource
}

// Version returns the value served on GET /version.
func (s *Service) Version() string {
	return s.version
}

// MessagesByDevice returns up to limit stored messages for a single device
// newest-first, optionally filtered to ID < since. device=="" returns all
// messages (same as the removed Messages).
func (s *Service) MessagesByDevice(device string, limit int, since uint64) ([]Message, error) {
	return s.store.RecentByDevice(device, limit, since)
}

// SearchMessagesByDevice returns the device's messages matching keyword
// (case-insensitive substring of title+body) newest-first, capped at limit
// (limit<0 → all), together with the total number of matches for the device.
func (s *Service) SearchMessagesByDevice(device, keyword string, limit int, since uint64) ([]Message, int, error) {
	return s.store.SearchByDevice(device, keyword, limit, since)
}

// ForEachMessageByDevice streams the device's matching messages (newest-first)
// through fn one at a time — see Store.ForEachByDevice.
func (s *Service) ForEachMessageByDevice(device, keyword string, since uint64, fn func(Message) error) (int, error) {
	return s.store.ForEachByDevice(device, keyword, since, fn)
}

// DeleteMessageByDevice removes the message only when it belongs to device;
// the bool reports whether such a message existed.
func (s *Service) DeleteMessageByDevice(device string, id uint64) (bool, error) {
	return s.store.DeleteByDevice(device, id)
}

// DeleteMessageByExtraID removes the device's message carrying the given
// extras.id (if any) and always appends a DeletionByExtraID log entry so
// already-synced clients drop their local copy on the next sync. The bool
// reports whether a stored message was removed.
func (s *Service) DeleteMessageByExtraID(device, extraID string) (bool, error) {
	return s.store.DeleteByExtraID(device, extraID)
}

// DeleteAllMessagesByDevice removes every stored message belonging to device
// (device=="" removes everything).
func (s *Service) DeleteAllMessagesByDevice(device string) error {
	return s.store.DeleteAllByDevice(device)
}

// SubscribeByDevice registers a live consumer filtered to a single device
// (device=="" disables the filter, same as the removed Subscribe).
func (s *Service) SubscribeByDevice(device string) (<-chan Message, func()) {
	return s.hub.SubscribeByDevice(device)
}

// SubscriberCount returns the number of live /stream subscribers.
func (s *Service) SubscriberCount() int {
	return s.hub.SubscriberCount()
}

// Publish persists a message and fan-outs it to all subscribers. Persistence
// errors are returned to the caller, which must never block or alter the
// originating push.
func (s *Service) Publish(title, body string, priority int, extras map[string]interface{}) error {
	if extras == nil {
		extras = map[string]interface{}{}
	}
	m := Message{
		AppID:     defaultAppID,
		Title:     title,
		Message:   body,
		Priority:  priority,
		Extras:    extras,
		Date:      time.Now().Format(time.RFC3339Nano),
		DeviceKey: deviceOf(extras),
	}
	// A positive ttl extra (seconds) schedules the message for expiry; the
	// absolute deadline is persisted in the store's ttl index.
	if ttl, ok := ttlFromExtras(extras); ok {
		m.ExpiresAt = time.Now().Unix() + ttl
	}
	// When extras carries an "id" field, overwrite the existing message for
	// the same device + extras.id (if any) instead of appending a new entry.
	// This lets callers push "updates" to a logical message by reusing the id.
	var id uint64
	var err error
	if eid := extraIDOf(&m); eid != "" && m.DeviceKey != "" {
		id, _, err = s.store.UpsertByExtraID(m.DeviceKey, eid, &m)
	} else {
		id, err = s.store.Add(&m)
	}
	if err != nil {
		return err
	}
	m.ID = id
	s.hub.Publish(m)
	return nil
}

// MessagesAfterByDevice returns the device's messages with ID > after in
// ascending id order, plus a hasMore flag for forward incremental sync.
func (s *Service) MessagesAfterByDevice(device string, after uint64, limit int) ([]Message, bool, error) {
	return s.store.AfterByDevice(device, after, limit)
}

// MessagesPageByDevice is the newest-first history read with a hasMore flag
// reporting whether older messages remain in the since-pagination direction.
func (s *Service) MessagesPageByDevice(device string, limit int, since uint64) ([]Message, bool, error) {
	return s.store.RecentPageByDevice(device, limit, since)
}

// DeletionsByDevice returns one page of the device's deletion log starting
// after cursor `since`.
func (s *Service) DeletionsByDevice(device string, since uint64) (DeletionsPage, error) {
	return s.store.DeletionsByDevice(device, since)
}

// ExpireMessages removes ttl-expired messages (and their tombstones); it is
// invoked by the maintenance ticker and directly in tests.
func (s *Service) ExpireMessages(now time.Time) (int, error) {
	return s.store.ExpireMessages(now)
}

// CleanupDeletions prunes deletion-log rows past the 30-day retention window
// (inserting reset sentinels); it is invoked by the maintenance ticker.
func (s *Service) CleanupDeletions(now time.Time) (int, error) {
	return s.store.CleanupDeletions(now)
}

// startMaintenance runs the background sweeps for ttl expiry and deletion
// retention until Close.
func (s *Service) startMaintenance() {
	go func() {
		ttlTicker := time.NewTicker(ttlSweepInterval)
		retentionTicker := time.NewTicker(retentionSweepInterval)
		defer ttlTicker.Stop()
		defer retentionTicker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ttlTicker.C:
				n, err := s.ExpireMessages(time.Now())
				if err != nil {
					logger.Warnf("gotify compat: ttl sweep failed: %v", err)
				} else if n > 0 {
					logger.Infof("gotify compat: ttl sweep removed %d expired messages", n)
				}
			case <-retentionTicker.C:
				n, err := s.CleanupDeletions(time.Now())
				if err != nil {
					logger.Warnf("gotify compat: deletion retention sweep failed: %v", err)
				} else if n > 0 {
					logger.Infof("gotify compat: deletion retention sweep dropped %d old events", n)
				}
			}
		}
	}()
}

// Close stops the background sweeps and releases the store. Safe to call once.
func (s *Service) Close() error {
	s.closeOnce.Do(func() { close(s.stopCh) })
	return s.store.Close()
}
