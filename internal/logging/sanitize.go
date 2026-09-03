package logging

import (
	"fmt"
	"strings"
)

// sensitiveKeys is the case-insensitive set of push-body field names that
// must be middle-masked before their values are written to logs. The keys
// cover the canonical names plus legacy aliases accepted by /register and
// the gotify client token (rare in push bodies, but masked for safety).
var sensitiveKeys = map[string]struct{}{
	"device_key":   {}, // device identifier (shared across devices; must not leak)
	"device_token": {},
	"devicetoken":  {}, // legacy alias accepted by /register
	"token":        {}, // gotify client token
	"client_token": {},
}

// contentKeys is the case-insensitive set of push-body field names whose
// values carry user-facing content (title, body, etc.). We intentionally do
// NOT log the full text — that could leak personal or private content —
// but we DO want to log that the field was set and its length, so an
// operator can tell a message with an empty body from one with a long one
// without seeing the actual payload.
var contentKeys = map[string]struct{}{
	"title":     {},
	"subtitle":  {},
	"body":      {},
	"markdown":  {},
	"ciphertext": {}, // reserved for the upcoming user-key-encryption feature
}

// MaskMiddle returns the input with its middle portion replaced by "***":
//   - len < 12  -> "***"                    (full mask; middle would be <4 chars,
//                                              too little to be meaningful)
//   - len >= 12 -> first4 + "***" + last4  (e.g. "abcdefghijk" -> "abcd***hijk";
//                                              masks len-8 chars, guaranteed >=4)
//
// The threshold is derived from the mask budget: we always keep 4 chars at
// each end and replace the middle with "***". That middle must hide at least
// 4 real characters to be a meaningful mask — otherwise len=9 would only
// mask 1 char, which is barely masking at all.
//
// Token values are base64url or hex ASCII, so byte-index slicing is safe and
// never splits a multi-byte rune. The masked output keeps enough prefix and
// suffix for an operator to recognize a token from a glance without leaking
// the full value.
func MaskMiddle(s string) string {
	// We want the masked portion (len - 8) to be at least 4 characters long
	// so the mask actually hides something. Rearranged: len must be >= 12.
	if len(s) < 12 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-4:]
}

// MaskContent returns a placeholder that records whether the field was set
// and the number of runes (Unicode characters) it contained. An empty string
// produces "[len=0]"; any non-empty string produces "[len=N]" where N is
// the rune count, so multibyte characters (emoji, CJK) are counted correctly.
func MaskContent(s string) string {
	return fmt.Sprintf("[len=%d]", utf8Len(s))
}

// utf8Len returns the number of runes in s. Uses a simple range loop to
// avoid importing the Unicode package for one function.
func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// IsSensitiveKey reports whether key (case-insensitive) names a sensitive
// push-body field that must be middle-masked before logging.
func IsSensitiveKey(key string) bool {
	_, ok := sensitiveKeys[strings.ToLower(key)]
	return ok
}

// IsContentKey reports whether key (case-insensitive) names a content-carrying
// push-body field whose value should be replaced with a length placeholder.
func IsContentKey(key string) bool {
	_, ok := contentKeys[strings.ToLower(key)]
	return ok
}

// MaskSensitiveFields returns a shallow copy of m with three categories of
// transformation applied per key (case-insensitive):
//
//   - sensitive keys  (device_token, token, ...)     -> MaskMiddle (keep 4+4 chars)
//   - content keys    (title, body, markdown, ...)   -> MaskContent ("[len=N]")
//   - everything else                                 -> pass through untouched
//
// Non-string values in sensitive/content keys also pass through untouched —
// masking only applies to string values. The original map is never mutated.
func MaskSensitiveFields(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if sv, ok := v.(string); ok {
			switch {
			case IsSensitiveKey(k):
				out[k] = MaskMiddle(sv)
				continue
			case IsContentKey(k):
				out[k] = MaskContent(sv)
				continue
			}
		}
		out[k] = v
	}
	return out
}
