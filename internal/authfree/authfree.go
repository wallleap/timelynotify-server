// Package authfree decides which request paths are exempt from Basic Auth
// because they carry their own authentication (a gotify client token on
// device-level /message & /stream) or need none (probe endpoints).
package authfree

import (
	"path"
	"strings"
)

// Routers are global paths exempt from Basic Auth; they need no
// authentication (probe endpoints) or carry their own.
var Routers = []string{"/ping", "/register", "/healthz", "/info"}

// Suffixes are single device-key-scoped path suffixes exempt from Basic Auth,
// matching /:device_key/<suffix> or /:device_key/message/:id. They carry their
// own authentication (gotify client token on /message & /stream, none on
// /version). ":id" is a placeholder matching one non-empty segment.
var Suffixes = []string{"/version", "/message", "/stream", "/message/:id"}

// IsAuthFreePath reports whether p is (or is under) a Basic-Auth whitelisted
// path, with exact-or-subpath semantics so lookalikes like /messageevil are
// NOT whitelisted. Device-scoped paths of the form /<device>/version,
// /<device>/message, /<device>/stream and /<device>/message/<id> are also
// whitelisted.
func IsAuthFreePath(urlPrefix, p string) bool {
	for _, item := range Routers {
		base := path.Join(urlPrefix, item)
		if p == base || strings.HasPrefix(p, base+"/") {
			return true
		}
	}
	// Device-scoped paths: /<device>/<suffix> (2 segments) or
	// /<device>/message/<id> (3 segments). Match segment-by-segment so
	// lookalikes like /dev/messageevil or /dev/message/evil are never
	// whitelisted.
	trimmed := strings.TrimPrefix(p, urlPrefix)
	trimmed = strings.TrimPrefix(trimmed, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] == "" {
		return false
	}
	for _, suffix := range Suffixes {
		suffixParts := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
		if len(suffixParts) != len(parts)-1 {
			continue
		}
		match := true
		for i := range suffixParts {
			if suffixParts[i] == ":id" {
				if parts[i+1] == "" {
					match = false
				}
				continue
			}
			if suffixParts[i] != parts[i+1] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
