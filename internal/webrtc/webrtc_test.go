package webrtc

import (
	"encoding/json"
	"testing"

	"remoteaccess/internal/protocol"
)

func TestHostSessionCreation(t *testing.T) {
	var sentMessages []protocol.SignalingMessage
	sendSignal := func(msg protocol.SignalingMessage) {
		sentMessages = append(sentMessages, msg)
	}

	sess, err := NewHostSession("client_123", "Admin Controller", 30, 65, sendSignal)
	if err != nil {
		t.Fatalf("Failed to create HostSession: %v", err)
	}
	defer sess.Close()

	if sess.ClientID != "client_123" {
		t.Errorf("Expected ClientID client_123, got %s", sess.ClientID)
	}
	if sess.ClientAlias != "Admin Controller" {
		t.Errorf("Expected ClientAlias 'Admin Controller', got %s", sess.ClientAlias)
	}
	if sess.FPS != 30 {
		t.Errorf("Expected FPS 30, got %d", sess.FPS)
	}
	if sess.Quality != 65 {
		t.Errorf("Expected Quality 65, got %d", sess.Quality)
	}
	if !sess.IsActive() {
		t.Errorf("Expected session to be active")
	}
}

func TestHostSessionHandleControlData(t *testing.T) {
	sess, err := NewHostSession("client_456", "Test", 30, 60, nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer sess.Close()

	// 1. Send Ping
	pingMsg, _ := json.Marshal(protocol.ControlMessage{Type: protocol.TypePing, Time: 100.0})
	sess.HandleControlData(pingMsg)

	// 2. Send Config change
	cfgMsg, _ := json.Marshal(protocol.ControlMessage{Type: protocol.TypeConfig, Quality: 80, FPS: 45})
	sess.HandleControlData(cfgMsg)
	if sess.FPS != 45 {
		t.Errorf("Expected updated FPS 45, got %d", sess.FPS)
	}

	// 3. Send invalid JSON (must not panic)
	sess.HandleControlData([]byte("invalid json"))
}

func TestHostSessionDisconnectGrace(t *testing.T) {
	sess, err := NewHostSession("client_grace", "TestGrace", 30, 60, nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer sess.Close()

	// 1. Trigger disconnect grace
	sess.handleDisconnectGrace("ICE")
	sess.mu.Lock()
	timerActive := sess.disconnectTimer != nil
	sess.mu.Unlock()
	if !timerActive {
		t.Errorf("Expected disconnectTimer to be non-nil after handleDisconnectGrace")
	}

	// 2. Cancel disconnect grace (reconnected in time)
	sess.cancelDisconnectGrace()
	sess.mu.Lock()
	timerCleared := sess.disconnectTimer == nil
	sess.mu.Unlock()
	if !timerCleared {
		t.Errorf("Expected disconnectTimer to be nil after cancelDisconnectGrace")
	}

	// 3. Ensure session is still active
	if !sess.IsActive() {
		t.Errorf("Expected session to remain active after grace cancel")
	}
}
