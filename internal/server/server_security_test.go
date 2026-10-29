package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"remoteaccess/internal/config"
)

func setupTestServer(t *testing.T) (*LocalServer, func()) {
	tmpDir, err := os.MkdirTemp("", "ra_srv_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	cfgPath := filepath.Join(tmpDir, "config.json")
	cm := config.NewTestConfigManager(cfgPath, config.AppConfig{
		ID:       "999888",
		Alias:    "TestHost",
		Password: "SecretPassword123",
	})
	_ = cm.Save()
	srv := NewLocalServer(cm)
	cleanup := func() {
		os.RemoveAll(tmpDir)
	}
	return srv, cleanup
}

func TestLocalCSRFProtection(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Calling /api/set-password WITHOUT token should return 403 Forbidden
	body := strings.NewReader(`{"password":"NewPassword123"}`)
	req := httptest.NewRequest("POST", "/api/set-password", body)
	w := httptest.NewRecorder()
	srv.handleSetPassword(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for missing token, got %d", w.Code)
	}

	// 2. Calling with WRONG token should return 403 Forbidden
	body = strings.NewReader(`{"password":"NewPassword123"}`)
	req = httptest.NewRequest("POST", "/api/set-password", body)
	req.Header.Set("X-Local-Token", "invalid_token_12345")
	w = httptest.NewRecorder()
	srv.handleSetPassword(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for invalid token, got %d", w.Code)
	}

	// 3. Calling with VALID token should succeed (200 OK)
	body = strings.NewReader(`{"password":"NewPassword123"}`)
	req = httptest.NewRequest("POST", "/api/set-password", body)
	req.Header.Set("X-Local-Token", srv.sessionToken)
	w = httptest.NewRecorder()
	srv.handleSetPassword(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for valid token, got %d", w.Code)
	}
}

func TestHostInfoPasswordMasking(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Calling /api/host-info without token masks the password
	req := httptest.NewRequest("GET", "/api/host-info", nil)
	w := httptest.NewRecorder()
	srv.handleHostInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if data["password"] != "••••••••" {
		t.Errorf("Expected password to be masked ('••••••••'), got %v", data["password"])
	}
	if data["has_password"] != true {
		t.Errorf("Expected has_password to be true")
	}

	// 2. Calling /api/host-info WITH valid token reveals real password
	req = httptest.NewRequest("GET", "/api/host-info", nil)
	req.Header.Set("X-Local-Token", srv.sessionToken)
	w = httptest.NewRecorder()
	srv.handleHostInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var authData map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&authData); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if authData["password"] != "SecretPassword123" {
		t.Errorf("Expected real password 'SecretPassword123', got %v", authData["password"])
	}
}

func TestLocalCheckOrigin(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Empty Origin (desktop app) should be allowed
	req := httptest.NewRequest("GET", "/ws", nil)
	if !srv.upgrader.CheckOrigin(req) {
		t.Errorf("Expected empty origin to be allowed")
	}

	// 2. Localhost Origin should be allowed
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	if !srv.upgrader.CheckOrigin(req) {
		t.Errorf("Expected localhost origin to be allowed")
	}

	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	if !srv.upgrader.CheckOrigin(req) {
		t.Errorf("Expected 127.0.0.1 origin to be allowed")
	}

	// 3. Malicious third-party Origin should be REJECTED (CSWSH defense)
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "http://evil-attacker.com")
	if srv.upgrader.CheckOrigin(req) {
		t.Errorf("Expected external origin evil-attacker.com to be REJECTED")
	}
}

func TestInteractiveConnectionAcceptance(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Initial state: no pending requests
	req := httptest.NewRequest("GET", "/api/session-status", nil)
	w := httptest.NewRecorder()
	srv.handleSessionStatus(w, req)
	var status map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&status)
	if status["active"] != false || status["has_pending_request"] == true {
		t.Fatalf("Expected no active session and no pending request")
	}

	// 2. Simulate incoming request from signaling
	srv.pendingMu.Lock()
	srv.pendingRequest = &PendingIncomingRequest{
		ClientID:    "c_12345",
		ClientAlias: "Suporte Técnico",
		ClientIP:    "192.168.1.100",
		ExpiresAt:   time.Now().Add(45 * time.Second),
	}
	srv.pendingMu.Unlock()

	// 3. Polling /api/session-status should now reflect has_pending_request
	req = httptest.NewRequest("GET", "/api/session-status", nil)
	w = httptest.NewRecorder()
	srv.handleSessionStatus(w, req)
	var pendingStatus map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&pendingStatus)
	if pendingStatus["has_pending_request"] != true {
		t.Fatalf("Expected has_pending_request to be true")
	}
	prData, ok := pendingStatus["pending_request"].(map[string]interface{})
	if !ok || prData["client_id"] != "c_12345" || prData["client_alias"] != "Suporte Técnico" {
		t.Fatalf("Expected pending_request details for c_12345, got %v", pendingStatus)
	}

	// 4. Rejecting connection without token should fail (403)
	rejReq := httptest.NewRequest("POST", "/api/reject-connection", nil)
	w = httptest.NewRecorder()
	srv.handleRejectConnection(w, rejReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden without CSRF token")
	}

	// 5. Rejecting with valid token should succeed and clear pending request
	rejReq = httptest.NewRequest("POST", "/api/reject-connection", nil)
	rejReq.Header.Set("X-Local-Token", srv.sessionToken)
	w = httptest.NewRecorder()
	srv.handleRejectConnection(w, rejReq)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on reject")
	}

	srv.pendingMu.Lock()
	if srv.pendingRequest != nil {
		t.Fatalf("Expected pendingRequest to be cleared after rejection")
	}
	srv.pendingMu.Unlock()
}
