package signaling

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCleanAndFormatID(t *testing.T) {
	tests := []struct {
		input       string
		wantClean   string
		wantFormat  string
	}{
		{"123 456", "123456", "123 456"},
		{"c_987654", "c_987654", "c_987654"},
		{"", "", "--- ---"},
		{"abc-123", "abc123", "abc 123"},
	}

	for _, tt := range tests {
		gotClean := cleanID(tt.input)
		if gotClean != tt.wantClean {
			t.Errorf("cleanID(%q) = %q, want %q", tt.input, gotClean, tt.wantClean)
		}
		gotFormat := formatID(tt.input)
		if gotFormat != tt.wantFormat {
			t.Errorf("formatID(%q) = %q, want %q", tt.input, gotFormat, tt.wantFormat)
		}
	}
}

func TestServerAuthAndStatsAPI(t *testing.T) {
	srv := NewServer()

	// 1. Test Auth with invalid key
	authReqBody, _ := json.Marshal(map[string]string{"key": "wrong_key"})
	req := httptest.NewRequest("POST", "/api/auth", bytes.NewReader(authReqBody))
	w := httptest.NewRecorder()
	srv.HandleAuth(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for wrong key, got %d", w.Code)
	}

	// 2. Test Auth with valid key
	authValidBody, _ := json.Marshal(map[string]string{"key": srv.adminKey})
	req = httptest.NewRequest("POST", "/api/auth", bytes.NewReader(authValidBody))
	w = httptest.NewRecorder()
	srv.HandleAuth(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for valid key, got %d", w.Code)
	}

	// 3. Test Stats API with valid key in query
	req = httptest.NewRequest("GET", "/api/stats?key="+srv.adminKey, nil)
	w = httptest.NewRecorder()
	srv.HandleStats(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for stats, got %d", w.Code)
	}

	var statsResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&statsResp); err != nil {
		t.Fatalf("Failed to decode stats JSON: %v", err)
	}
	if _, ok := statsResp["host_count"]; !ok {
		t.Errorf("Expected host_count in stats response")
	}
}

func TestServerHistory(t *testing.T) {
	srv := NewServer()

	rec := SessionRecord{
		ID:        "test_session_1",
		HostID:    "123456",
		HostAlias: "Host PC",
		ClientID:  "654321",
		Status:    "active",
	}
	srv.addHistory(rec)

	req := httptest.NewRequest("GET", "/api/history?key="+srv.adminKey, nil)
	w := httptest.NewRecorder()
	srv.HandleHistory(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for history, got %d", w.Code)
	}

	var histResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&histResp); err != nil {
		t.Fatalf("Failed to decode history response: %v", err)
	}
	historyList, ok := histResp["history"].([]interface{})
	if !ok || len(historyList) == 0 {
		t.Errorf("Expected non-empty history list")
	}
}
