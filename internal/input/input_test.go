package input

import (
	"testing"
)

func TestResolveVK(t *testing.T) {
	tests := []struct {
		key     string
		code    string
		keyCode int
		want    uint16
	}{
		{"c", "KeyC", 67, 0x43},
		{"v", "KeyV", 86, 0x56},
		{"a", "KeyA", 65, 0x41},
		{"z", "KeyZ", 90, 0x5A},
		{"Enter", "Enter", 13, 0x0D},
		{"Tab", "Tab", 9, 0x09},
		{"Escape", "Escape", 27, 0x1B},
		{"F4", "F4", 115, 0x73},
		{"Control", "ControlLeft", 17, 0xA2},
		{"Alt", "AltLeft", 18, 0xA4},
		{"Shift", "ShiftLeft", 16, 0xA0},
		{"Meta", "MetaLeft", 91, 0x5B},
	}

	for _, tt := range tests {
		got := resolveVK(tt.key, tt.code, tt.keyCode)
		if got != tt.want {
			t.Errorf("resolveVK(%q, %q, %d) = 0x%X; want 0x%X", tt.key, tt.code, tt.keyCode, got, tt.want)
		}
	}
}

func TestIsExtendedKey(t *testing.T) {
	if !isExtendedKey(0x5B) { // VK_LWIN
		t.Errorf("expected VK_LWIN to be extended key")
	}
	if !isExtendedKey(0x2E) { // VK_DELETE
		t.Errorf("expected VK_DELETE to be extended key")
	}
	if isExtendedKey(0x43) { // 'C'
		t.Errorf("expected 'C' not to be extended key")
	}
}
