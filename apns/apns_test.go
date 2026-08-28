package apns

import "testing"

func TestPushMessageIsEmptyAlert(t *testing.T) {
	tests := []struct {
		name string
		msg  PushMessage
		want bool
	}{
		{"all empty", PushMessage{}, true},
		{"title only", PushMessage{Title: "t"}, false},
		{"body only", PushMessage{Body: "b"}, false},
		{"subtitle only", PushMessage{Subtitle: "s"}, false},
		{"all present", PushMessage{Title: "t", Body: "b", Subtitle: "s"}, false},
		{"title and subtitle", PushMessage{Title: "t", Subtitle: "s"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.msg.Title+"/"+tt.msg.Body, func(t *testing.T) {
			if got := tt.msg.IsEmptyAlert(); got != tt.want {
				t.Fatalf("IsEmptyAlert() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPushMessageIsDelete(t *testing.T) {
	tests := []struct {
		name string
		msg  PushMessage
		want bool
	}{
		{"no delete key", PushMessage{ExtParams: map[string]interface{}{}}, false},
		{"delete string 1", PushMessage{ExtParams: map[string]interface{}{"delete": "1"}}, true},
		{"delete int 1", PushMessage{ExtParams: map[string]interface{}{"delete": 1}}, true},
		{"delete float 1", PushMessage{ExtParams: map[string]interface{}{"delete": 1.0}}, true},
		{"delete string 0", PushMessage{ExtParams: map[string]interface{}{"delete": "0"}}, false},
		{"delete int 0", PushMessage{ExtParams: map[string]interface{}{"delete": 0}}, false},
		{"delete other string", PushMessage{ExtParams: map[string]interface{}{"delete": "true"}}, false},
		{"nil ext", PushMessage{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msg.IsDelete(); got != tt.want {
				t.Fatalf("IsDelete() = %v, want %v", got, tt.want)
			}
		})
	}
}
