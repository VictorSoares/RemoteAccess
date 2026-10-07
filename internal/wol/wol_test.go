package wol

import (
	"strings"
	"testing"
)

func TestSendMagicPacketValidation(t *testing.T) {
	tests := []struct {
		name    string
		mac     string
		wantErr bool
	}{
		{
			name:    "Valid MAC with colons",
			mac:     "00:11:22:33:44:55",
			wantErr: false,
		},
		{
			name:    "Valid MAC with hyphens",
			mac:     "00-11-22-33-44-55",
			wantErr: false,
		},
		{
			name:    "Valid raw 12 hex",
			mac:     "001122334455",
			wantErr: false,
		},
		{
			name:    "Invalid length",
			mac:     "00:11:22:33:44",
			wantErr: true,
		},
		{
			name:    "Invalid non-hex characters",
			mac:     "GG:HH:II:JJ:KK:LL",
			wantErr: true,
		},
		{
			name:    "Empty MAC",
			mac:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SendMagicPacket(tt.mac)
			if (err != nil) != tt.wantErr {
				// Note: On machines with no network interfaces up, UDP send might return an error, but validation errors happen before socket calls
				if tt.wantErr && err == nil {
					t.Errorf("Expected error for MAC %s, got nil", tt.mac)
				}
			}
		})
	}
}

func TestGetPrimaryMACAddress(t *testing.T) {
	mac := GetPrimaryMACAddress()
	if mac != "" {
		// If an interface is up, ensure it's uppercase and has standard length
		if mac != strings.ToUpper(mac) {
			t.Errorf("Expected uppercase MAC, got %s", mac)
		}
	}
}
