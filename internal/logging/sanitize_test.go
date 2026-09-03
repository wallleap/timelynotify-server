package logging

import "testing"

func TestMaskMiddle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "***"},
		{"short", "abc", "***"},
		{"exactly 8 (<12)", "12345678", "***"},
		{"11 chars (<12)", "12345678901", "***"},
		{"12 chars (=12, first with middle mask)", "123456789012", "1234***9012"},
		{"13 chars", "1234567890123", "1234***0123"},
		{"16 chars", "abcdefghijklmnop", "abcd***mnop"},
		{"32 chars (gotify client token)", "02ob86vvt71lb8sdr14608emfnbfurh6", "02ob***urh6"},
		{"64-hex device token", "580e322f0470c3eca5df5246d1c251e00ac3ad776664c80d44535f9a73bb0b40", "580e***0b40"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskMiddle(c.in); got != c.want {
				t.Errorf("MaskMiddle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMaskContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "[len=0]"},
		{"short ascii", "hello", "[len=5]"},
		{"long ascii", "hello world foo bar", "[len=19]"},
		{"CJK", "你好世界", "[len=4]"},
		{"emoji", "🎉🚀", "[len=2]"},
		{"mixed", "Hi 你好 🎉", "[len=7]"}, // H i sp 你 好 sp 🎉 = 7 runes
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskContent(c.in); got != c.want {
				t.Errorf("MaskContent(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsSensitiveKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"device_token", true},
		{"Device_Token", true},
		{"DEVICETOKEN", true},
		{"devicetoken", true},
		{"token", true},
		{"Token", true},
		{"client_token", true},
		{"device_key", true},
		{"title", false},
		{"body", false},
		{"key", false},
		{"sound", false},
		{"icon", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := IsSensitiveKey(c.key); got != c.want {
				t.Errorf("IsSensitiveKey(%q) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}

func TestIsContentKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"title", true},
		{"Title", true},
		{"subtitle", true},
		{"SubTitle", true},
		{"body", true},
		{"BODY", true},
		{"markdown", true},
		{"Markdown", true},
		{"ciphertext", true},
		{"device_key", false},
		{"device_token", false},
		{"icon", false},
		{"sound", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := IsContentKey(c.key); got != c.want {
				t.Errorf("IsContentKey(%q) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}

func TestMaskSensitiveFields(t *testing.T) {
	in := map[string]interface{}{
		"device_key":   "abc", // not sensitive; pass through
		"device_token": "580e322f0470c3eca5df5246d1c251e00ac3ad776664c80d44535f9a73bb0b40",
		"Token":        "client-tok-value", // case-insensitive match
		"title":        "hello",
		"body":         "world",
		"subtitle":     "",
		"badge":        5, // non-string passthrough
		"icon":         "https://example.com/x.png",
	}
	out := MaskSensitiveFields(in)

	// Sensitive: middle-masked
	if got := out["device_token"].(string); got == in["device_token"] {
		t.Errorf("device_token not masked: %q", got)
	} else if got != "580e***0b40" {
		t.Errorf("device_token masked incorrectly: %q", got)
	}
	if got := out["Token"].(string); got == in["Token"] {
		t.Errorf("Token (case-insensitive) not masked: %q", got)
	} else if got != "clie***alue" {
		t.Errorf("Token masked incorrectly: %q", got)
	}

	// Content: length placeholder
	if got, want := out["title"].(string), "[len=5]"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if got, want := out["body"].(string), "[len=5]"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got, want := out["subtitle"].(string), "[len=0]"; got != want {
		t.Errorf("subtitle = %q, want %q", got, want)
	}

	// Sensitive (short <12 chars) or non-sensitive non-content:
	if got, want := out["device_key"], "***"; got != want {
		t.Errorf("device_key = %v, want %v (short sensitive key fully masked)", got, want)
	}
	if got, want := out["badge"], 5; got != want {
		t.Errorf("badge = %v, want %v", got, want)
	}
	if got, want := out["icon"], "https://example.com/x.png"; got != want {
		t.Errorf("icon = %v, want %v", got, want)
	}

	// Original map must be untouched.
	if in["device_token"] != "580e322f0470c3eca5df5246d1c251e00ac3ad776664c80d44535f9a73bb0b40" {
		t.Errorf("input map mutated: device_token = %v", in["device_token"])
	}
	if in["title"] != "hello" {
		t.Errorf("input map mutated: title = %v", in["title"])
	}
}

func TestMaskSensitiveFields_EmptyMap(t *testing.T) {
	out := MaskSensitiveFields(nil)
	if out == nil {
		t.Fatal("expected non-nil map for nil input")
	}
	if len(out) != 0 {
		t.Errorf("expected empty map, got %d entries", len(out))
	}
}

func TestMaskSensitiveFields_SensitiveNonStringValue(t *testing.T) {
	// A sensitive key with a non-string value (e.g. token passed as number)
	// passes through unchanged — masking only applies to string values.
	in := map[string]interface{}{"token": 12345}
	out := MaskSensitiveFields(in)
	if out["token"] != 12345 {
		t.Errorf("non-string sensitive value was altered: %v", out["token"])
	}
}

func TestMaskSensitiveFields_ContentNonStringValue(t *testing.T) {
	// A content key with a non-string value also passes through.
	in := map[string]interface{}{"title": 42}
	out := MaskSensitiveFields(in)
	if out["title"] != 42 {
		t.Errorf("non-string content value was altered: %v", out["title"])
	}
}
