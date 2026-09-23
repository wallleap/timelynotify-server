package pushpolicy

import "testing"

func TestShouldPublishHistory(t *testing.T) {
	tests := []struct {
		name       string
		extras     map[string]interface{}
		hasHarmony bool
		want       bool
	}{
		{name: "missing defaults to archive", extras: map[string]interface{}{}, want: true},
		{name: "string one archives", extras: map[string]interface{}{"isarchive": "1"}, want: true},
		{name: "numeric one archives", extras: map[string]interface{}{"isArchive": float64(1)}, want: true},
		{name: "boolean true archives", extras: map[string]interface{}{"isArchive": true}, want: true},
		{name: "zero skips ordinary history", extras: map[string]interface{}{"isArchive": "0"}, want: false},
		{name: "encrypted Harmony is retained temporarily", extras: map[string]interface{}{"isArchive": "0", "ciphertext": "encrypted"}, hasHarmony: true, want: true},
		{name: "encrypted iOS does not need Harmony migration", extras: map[string]interface{}{"isArchive": "0", "ciphertext": "encrypted"}, want: false},
		{name: "empty ciphertext does not retain", extras: map[string]interface{}{"isArchive": "0", "ciphertext": ""}, hasHarmony: true, want: false},
		{name: "null ciphertext does not retain", extras: map[string]interface{}{"isArchive": "0", "ciphertext": nil}, hasHarmony: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldPublishHistory(tt.extras, tt.hasHarmony); got != tt.want {
				t.Fatalf("ShouldPublishHistory() = %v, want %v", got, tt.want)
			}
		})
	}
}
