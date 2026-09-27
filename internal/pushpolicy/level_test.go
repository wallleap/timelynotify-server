package pushpolicy

import "testing"

func TestHarmonyPassive(t *testing.T) {
	tests := []struct {
		name   string
		extras map[string]interface{}
		want   bool
	}{
		{name: "missing level", extras: nil},
		{name: "active", extras: map[string]interface{}{"level": "active"}},
		{name: "time sensitive falls back to active", extras: map[string]interface{}{"level": "timeSensitive"}},
		{name: "critical falls back to active", extras: map[string]interface{}{"level": "critical"}},
		{name: "passive", extras: map[string]interface{}{"level": "passive"}, want: true},
		{name: "mixed case and whitespace", extras: map[string]interface{}{"Level": " Passive "}, want: true},
		{name: "non-string", extras: map[string]interface{}{"level": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HarmonyPassive(tt.extras); got != tt.want {
				t.Fatalf("HarmonyPassive(%v) = %v, want %v", tt.extras, got, tt.want)
			}
		})
	}
}
