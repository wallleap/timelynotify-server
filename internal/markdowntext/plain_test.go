package markdowntext

import "testing"

func TestPlain(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"formatting", "# 标题\n**加粗**、*斜体*、~~删除~~、`代码`", "标题\n加粗、斜体、删除、代码"},
		{"links and image", "[文档](https://example.com/doc) ![图](https://example.com/a.png)", "文档 (https://example.com/doc) https://example.com/a.png"},
		{"list and code", "- 第一项\n- 第二项\n\n```go\nfmt.Println(1)\n```", "• 第一项\n• 第二项\nfmt.Println(1)"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Plain(tt.input); got != tt.want {
				t.Errorf("Plain(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
