package signaling

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignalingRateLimiting(t *testing.T) {
	srv := NewServer()
	clientIP := "192.168.1.50"
	targetID := "654321"

	// 1. First 4 attempts should NOT be rate limited
	for i := 1; i <= 4; i++ {
		if srv.isRateLimited(clientIP, targetID) {
			t.Errorf("Attempt %d should not be rate-limited yet", i)
		}
		srv.recordFailedAttempt(clientIP, targetID)
	}

	// 2. 5th failure should trigger lockout
	srv.recordFailedAttempt(clientIP, targetID)
	if !srv.isRateLimited(clientIP, targetID) {
		t.Errorf("Expected 5th failure to trigger lockout")
	}

	// 3. Reset should clear lockout
	srv.resetFailedAttempts(clientIP, targetID)
	if srv.isRateLimited(clientIP, targetID) {
		t.Errorf("Reset should clear lockout")
	}
}

func TestStatsOmitPlaintextPasswords(t *testing.T) {
	srv := NewServer()

	// Register a host peer with a password
	srv.mu.Lock()
	srv.peers["111222"] = &Peer{
		ID:          "111222",
		Alias:       "HostWithSecret",
		IsHost:      true,
		Password:    "SuperSecretPassword",
		ConnectedAt: time.Now(),
	}
	srv.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/stats", nil)
	req.Header.Set("X-Admin-Key", srv.adminKey)
	w := httptest.NewRecorder()
	srv.HandleStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("Failed to decode stats response: %v", err)
	}

	peers, ok := data["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		t.Fatalf("Expected peers list in response")
	}

	peerData, ok := peers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected peer to be a JSON object")
	}

	// VERIFY: 'password' field must NOT exist in the response
	if _, exists := peerData["password"]; exists {
		t.Errorf("CRITICAL SECURITY FLAW: 'password' field was exposed in /api/stats: %v", peerData["password"])
	}
	if hasPwd, exists := peerData["has_password"]; !exists || hasPwd != true {
		t.Errorf("Expected 'has_password' to be true, got %v", hasPwd)
	}
}

func TestSignalingCheckOrigin(t *testing.T) {
	// 1. Native desktop app (no Origin) -> Allowed
	req := httptest.NewRequest("GET", "/ws", nil)
	if !upgrader.CheckOrigin(req) {
		t.Errorf("Expected native client without Origin to be allowed")
	}

	// 2. Render cloud domain -> Allowed
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "https://remoteaccess-ltwx.onrender.com")
	if !upgrader.CheckOrigin(req) {
		t.Errorf("Expected remoteaccess-ltwx.onrender.com to be allowed")
	}

	// 3. Localhost -> Allowed
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	if !upgrader.CheckOrigin(req) {
		t.Errorf("Expected localhost to be allowed")
	}

	// 4. Malicious third-party origin -> REJECTED (CSWSH)
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "https://hacker-malicious-site.com")
	if upgrader.CheckOrigin(req) {
		t.Errorf("Expected hacker-malicious-site.com to be REJECTED")
	}
}
