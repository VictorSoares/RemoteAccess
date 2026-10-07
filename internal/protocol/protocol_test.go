package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSignalingMessageSerialization(t *testing.T) {
	msg := SignalingMessage{
		Action:   ActionConnect,
		ID:       "c_123456",
		TargetID: "573221",
		Password: "SECRET_PASSWORD",
		Alias:    "Meu Computador (Controlador)",
		Monitors: 2,
		MAC:      "AA:BB:CC:DD:EE:FF",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal SignalingMessage: %v", err)
	}

	var decoded SignalingMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal SignalingMessage: %v", err)
	}

	if decoded.Action != ActionConnect {
		t.Errorf("Expected action %s, got %s", ActionConnect, decoded.Action)
	}
	if decoded.TargetID != "573221" {
		t.Errorf("Expected target ID 573221, got %s", decoded.TargetID)
	}
	if decoded.Monitors != 2 {
		t.Errorf("Expected 2 monitors, got %d", decoded.Monitors)
	}
}

func TestControlMessageSerialization(t *testing.T) {
	tests := []struct {
		name string
		msg  ControlMessage
	}{
		{
			name: "Mouse move",
			msg:  ControlMessage{Type: TypeMouseMove, X: 0.54, Y: 0.72},
		},
		{
			name: "Mouse click",
			msg:  ControlMessage{Type: TypeMouseDown, Button: 0},
		},
		{
			name: "Key down",
			msg:  ControlMessage{Type: TypeKeyDown, Key: "Enter", Code: "Enter", KeyCode: 13},
		},
		{
			name: "Monitor switch",
			msg:  ControlMessage{Type: TypeMonitorSwitch, Monitor: 1},
		},
		{
			name: "System command",
			msg:  ControlMessage{Type: TypeSysCommand, Command: "lock"},
		},
		{
			name: "Clipboard paste",
			msg:  ControlMessage{Type: TypeClipboard, Text: "Hello, World!"},
		},
		{
			name: "Ping message",
			msg:  ControlMessage{Type: TypePing, Time: 12345.67},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}
			var decoded ControlMessage
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			if decoded.Type != tt.msg.Type {
				t.Errorf("Expected type %s, got %s", tt.msg.Type, decoded.Type)
			}
		})
	}
}

func TestVideoChunkEncodingDecoding(t *testing.T) {
	frameID := uint32(42)
	chunkIdx := uint16(2)
	totalChunks := uint16(5)
	payload := []byte("fake_jpeg_binary_payload_stream_data_here")

	encoded := EncodeVideoChunk(frameID, chunkIdx, totalChunks, payload)
	if len(encoded) != 8+len(payload) {
		t.Fatalf("Expected encoded length %d, got %d", 8+len(payload), len(encoded))
	}

	decFrameID, decChunkIdx, decTotalChunks, decPayload, err := DecodeVideoChunk(encoded)
	if err != nil {
		t.Fatalf("Failed to decode chunk: %v", err)
	}

	if decFrameID != frameID {
		t.Errorf("Expected frameID %d, got %d", frameID, decFrameID)
	}
	if decChunkIdx != chunkIdx {
		t.Errorf("Expected chunkIdx %d, got %d", chunkIdx, decChunkIdx)
	}
	if decTotalChunks != totalChunks {
		t.Errorf("Expected totalChunks %d, got %d", totalChunks, decTotalChunks)
	}
	if !bytes.Equal(decPayload, payload) {
		t.Errorf("Payload mismatch: expected %q, got %q", payload, decPayload)
	}
}

func TestVideoChunkShortPacketError(t *testing.T) {
	shortData := []byte{0x00, 0x01, 0x02}
	_, _, _, _, err := DecodeVideoChunk(shortData)
	if err == nil {
		t.Errorf("Expected error for short packet (<8 bytes), got nil")
	}
}
