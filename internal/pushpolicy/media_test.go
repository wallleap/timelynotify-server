package pushpolicy

import "testing"

func TestHarmonyImageURL(t *testing.T) {
	tests := []struct {
		name   string
		extras map[string]interface{}
		want   string
	}{
		{name: "icon wins", extras: map[string]interface{}{
			"icon":  "https://example.com/icon.png",
			"image": "https://example.com/image.png",
		}, want: "https://example.com/icon.png"},
		{name: "image fallback", extras: map[string]interface{}{
			"image": "https://example.com/image.png",
		}, want: "https://example.com/image.png"},
		{name: "blank icon falls back", extras: map[string]interface{}{
			"icon":  "  ",
			"image": "https://example.com/image.png",
		}, want: "https://example.com/image.png"},
		{name: "quoted icon is trimmed", extras: map[string]interface{}{
			"icon": "  `https://example.com/icon.png`  ",
		}, want: "https://example.com/icon.png"},
		{name: "empty quoted icon falls back", extras: map[string]interface{}{
			"icon":  " ` ` ",
			"image": "https://example.com/image.png",
		}, want: "https://example.com/image.png"},
		{name: "neither omitted", extras: map[string]interface{}{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HarmonyImageURL(tt.extras); got != tt.want {
				t.Fatalf("HarmonyImageURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
