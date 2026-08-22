package gotifycompat

import "sync"

// Hub fan-outs newly published messages to all connected subscribers.
//
// Delivery is non-blocking with a small per-client buffer: a slow or dead
// subscriber is dropped rather than blocking the push path. This keeps the
// monitoring layer isolated from the APNs delivery path (high availability).
type Hub struct {
	mu   sync.RWMutex
	next uint64
	subs map[uint64]subscriber
}

// subscriber ties a channel to an optional device filter. device=="" means the
// subscriber hears every message.
type subscriber struct {
	ch     chan Message
	device string
}

func newHub() *Hub {
	return &Hub{subs: make(map[uint64]subscriber)}
}

// SubscribeByDevice registers a client filtered to a single device (device==""
// disables the filter). After Unsubscribe is called, the channel is closed and
// must no longer be used for sending.
func (h *Hub) SubscribeByDevice(device string) (<-chan Message, func()) {
	h.mu.Lock()
	id := h.next
	h.next++
	ch := make(chan Message, 32)
	h.subs[id] = subscriber{ch: ch, device: device}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if s, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(s.ch)
		}
		h.mu.Unlock()
	}
}

// SubscriberCount reports the number of live subscribers.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Publish delivers a message to every subscriber whose filter matches, dropping
// slow ones.
func (h *Hub) Publish(m Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		if s.device != "" && m.SourceDevice() != s.device {
			continue
		}
		select {
		case s.ch <- m:
		default: // slow/backpressured subscriber: drop to protect the hub
		}
	}
}
