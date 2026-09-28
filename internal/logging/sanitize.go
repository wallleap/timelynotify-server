package logging

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
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
	"title":        {},
	"subtitle":     {},
	"body":         {},
	"markdown":     {},
	"copy":         {},
	"data":         {},
	"inboxcontent": {},
	"ciphertext":   {}, // reserved for the upcoming user-key-encryption feature
}

var contentQueryParamRe = regexp.MustCompile(`(?i)[?&](?:markdown|copy)=[^&\s]*`)

// MaskMiddle returns the input with its middle portion replaced by "***":
//   - len < 12  -> "***"                    (full mask; middle would be <4 chars,
//     too little to be meaningful)
//   - len >= 12 -> first4 + "***" + last4  (e.g. "abcdefghijk" -> "abcd***hijk";
//     masks len-8 chars, guaranteed >=4)
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

// MaskContentQueryParams replaces markdown/copy query values in an access-log
// line with their decoded character counts. Malformed escapes are counted as
// raw characters; their original values are never forwarded to the log.
func MaskContentQueryParams(line string) string {
	return contentQueryParamRe.ReplaceAllStringFunc(line, func(param string) string {
		separator := strings.IndexByte(param, '=')
		value := param[separator+1:]
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		return param[:separator+1] + MaskContent(value)
	})
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

// maskContentValue summarizes a value without including its contents. Text and
// scalar values report character count; collections report item/field count.
func maskContentValue(v interface{}) string {
	if v == nil {
		return "[len=0]"
	}
	if s, ok := v.(string); ok {
		return MaskContent(s)
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Map:
		return fmt.Sprintf("[fields=%d]", value.Len())
	case reflect.Array, reflect.Slice:
		return fmt.Sprintf("[items=%d]", value.Len())
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return MaskContent(fmt.Sprint(v))
	default:
		return "[redacted]"
	}
}

// MaskSensitiveFields returns a shallow copy of m with values summarized by
// key (case-insensitive):
//
//   - sensitive keys  (device_token, token, ...)     -> MaskMiddle (keep 4+4 chars)
//   - content keys    (title, body, data, ...)       -> length/count placeholder
//   - other collections                              -> opaque item/field count
//   - other scalar values                            -> pass through untouched
//
// The original map and its nested values are never mutated.
func MaskSensitiveFields(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch {
		case IsSensitiveKey(k):
			if s, ok := v.(string); ok {
				out[k] = MaskMiddle(s)
			} else {
				out[k] = "***"
			}
		case IsContentKey(k):
			out[k] = maskContentValue(v)
		case v != nil && (reflect.ValueOf(v).Kind() == reflect.Map ||
			reflect.ValueOf(v).Kind() == reflect.Slice || reflect.ValueOf(v).Kind() == reflect.Array):
			out[k] = maskContentValue(v)
		default:
			out[k] = v
		}
	}
	return out
}
