package authfree

import "testing"

func TestIsAuthFreePath(t *testing.T) {
	tests := []struct {
		name      string
		urlPrefix string
		p         string
		want      bool
	}{
		// 说明：每个用例断言 IsAuthFreePath(urlPrefix, p) 的返回值。
		// want: true  = 该路径可免 Basic Auth 鉴权；
		// want: false = 该路径不在白名单，开启 Basic Auth 时应被 418 拦截。
		// Global exact-match whitelist.
		{name: "ping exact", urlPrefix: "/", p: "/ping", want: true},
		{name: "register exact", urlPrefix: "/", p: "/register", want: true},
		{name: "healthz exact", urlPrefix: "/", p: "/healthz", want: true},
		{name: "info exact", urlPrefix: "/", p: "/info", want: true},
		// Global subpaths (all whitelisted parents, incl. the real
		// /register/:device_key route).
		{name: "register device subpath", urlPrefix: "/", p: "/register/abc123", want: true},
		{name: "ping subpath", urlPrefix: "/", p: "/ping/x", want: true},
		{name: "healthz subpath", urlPrefix: "/", p: "/healthz/x", want: true},
		{name: "info subpath", urlPrefix: "/", p: "/info/extra", want: true},
		// Trailing slash on a whitelisted parent.
		{name: "info trailing slash", urlPrefix: "/", p: "/info/", want: true},
		// Lookalike must NOT match (bare-prefix safety, all parents).
		{name: "pingevil rejected", urlPrefix: "/", p: "/pingevil", want: false},
		{name: "registerevil rejected", urlPrefix: "/", p: "/registerevil", want: false},
		{name: "healthevil rejected", urlPrefix: "/", p: "/healthevil", want: false},
		// Removed global gotify routes are no longer whitelisted.
		{name: "global message rejected", urlPrefix: "/", p: "/message", want: false},
		{name: "global stream rejected", urlPrefix: "/", p: "/stream", want: false},
		{name: "global version rejected", urlPrefix: "/", p: "/version", want: false},
		{name: "messageevil rejected", urlPrefix: "/", p: "/messageevil", want: false},
		{name: "streamevil rejected", urlPrefix: "/", p: "/streamevil", want: false},
		{name: "versionevil rejected", urlPrefix: "/", p: "/versionevil", want: false},
		{name: "infoevil rejected", urlPrefix: "/", p: "/infoevil", want: false},
		// Case-sensitive: an uppercase lookalike must not be whitelisted.
		{name: "upper info rejected", urlPrefix: "/", p: "/INFO", want: false},
		{name: "upper stream rejected", urlPrefix: "/", p: "/Stream", want: false},
		{name: "capitalized message rejected", urlPrefix: "/", p: "/Message", want: false},
		// Unrelated paths (must require auth).
		{name: "push rejected", urlPrefix: "/", p: "/push", want: false},
		{name: "device push rejected", urlPrefix: "/", p: "/dev/body", want: false},
		{name: "mcp rejected", urlPrefix: "/", p: "/mcp", want: false},
		{name: "metrics rejected", urlPrefix: "/", p: "/metrics", want: false},

		// Device-scoped 2-segment paths.
		{name: "device message", urlPrefix: "/", p: "/dev/message", want: true},
		{name: "device stream", urlPrefix: "/", p: "/dev/stream", want: true},
		{name: "device version", urlPrefix: "/", p: "/dev/version", want: true},
		{name: "device message id", urlPrefix: "/", p: "/dev/message/12", want: true},
		// The 3-segment message/<id> route treats ANY third segment as the id
		// parameter; it must stay exempt so the gotify token can gate it (the
		// long-term bug this guards: device-scoped delete id needing Basic
		// Auth while the global /message/:id does not).
		{name: "device message id any value", urlPrefix: "/", p: "/dev/message/evil", want: true},
		// Device-scoped lookalikes.
		{name: "device messageevil rejected", urlPrefix: "/", p: "/dev/messageevil", want: false},
		{name: "device message third segment rejected", urlPrefix: "/", p: "/dev/message/id/evil", want: false},
		{name: "device empty id rejected", urlPrefix: "/", p: "/dev/message/", want: false},
		// Device-scoped path with a non-suffix third segment must NOT glob onto
		// the ones the gotify routes serve (only /message/:id is 3-segment).
		{name: "device stream third segment rejected", urlPrefix: "/", p: "/dev/stream/x", want: false},
		{name: "device version third segment rejected", urlPrefix: "/", p: "/dev/version/x", want: false},
		// Device-scoped DELETE all (also exempt).
		{name: "device message delete-all", urlPrefix: "/", p: "/dev/message", want: true},
		// Root and single-segment.
		{name: "root rejected", urlPrefix: "/", p: "/", want: false},
		{name: "single segment rejected", urlPrefix: "/", p: "/dev", want: false},
		{name: "double slash rejected", urlPrefix: "/", p: "//x", want: false},

		// urlPrefix support: bare "/", a non-empty prefix, a trailing-slash
		// prefix, and an empty prefix.
		{name: "prefixed ping", urlPrefix: "/bark", p: "/bark/ping", want: true},
		{name: "prefixed device message", urlPrefix: "/bark", p: "/bark/dev/message", want: true},
		{name: "prefixed lookalike rejected", urlPrefix: "/bark", p: "/bark/dev/messageevil", want: false},
		{name: "prefixed trailing slash ping", urlPrefix: "/bark/", p: "/bark/ping", want: true},
		{name: "prefixed trailing slash lookalike", urlPrefix: "/bark/", p: "/bark/pingevil", want: false},
		{name: "empty prefix ping", urlPrefix: "", p: "/ping", want: true},
		{name: "empty prefix lookalike rejected", urlPrefix: "", p: "/pingevil", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAuthFreePath(tt.urlPrefix, tt.p); got != tt.want {
				t.Errorf("IsAuthFreePath(%q, %q) = %v, want %v", tt.urlPrefix, tt.p, got, tt.want)
			}
		})
	}
}
