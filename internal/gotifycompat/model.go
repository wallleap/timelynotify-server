package gotifycompat

// Message mirrors the gotify message wire format consumed by hotify-bridge.
// Date is intentionally an opaque string (RFC3339Nano) so it passes through
// untouched into the bridge's Push Kit payloads (the bridge treats it as an
// opaque string and never parses it).
type Message struct {
	ID       uint64                 `json:"id"`
	AppID    uint                   `json:"appid"`
	Title    string                 `json:"title"`
	Message  string                 `json:"message"`
	Priority int                    `json:"priority"`
	Extras   map[string]interface{} `json:"extras"`
	Date     string                 `json:"date"`
	// DeviceKey records which device_key produced this message, used for
	// device-scoped history reads and stream fan-out. It is persisted for
	// filtering but kept out of the wire extras visible to the bridge (which
	// already carries device_key in extras).
	DeviceKey string `json:"-"`
	// ExpiresAt is the unix-second deadline after which the message is
	// removed by the TTL sweep. Zero means no expiry. Derived from the "ttl"
	// extra at publish time; the deadline itself lives in the ttl index.
	ExpiresAt int64 `json:"-"`
}

// SourceDevice returns the device this message originated from, reading the
// stored field and falling back to the extras (for previously persisted rows).
func (m *Message) SourceDevice() string {
	if m.DeviceKey != "" {
		return m.DeviceKey
	}
	if dk, ok := m.Extras["device_key"].(string); ok {
		return dk
	}
	return ""
}

// deviceOf extracts the device key from the extras map (the source of truth on
// the push path).
func deviceOf(extras map[string]interface{}) string {
	if dk, ok := extras["device_key"].(string); ok {
		return dk
	}
	return ""
}
