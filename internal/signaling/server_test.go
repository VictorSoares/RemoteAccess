package signaling

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
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

func TestServerGzipDashboardAndStats(t *testing.T) {
	srv := NewServer()

	// 1. Dashboard with Accept-Encoding: gzip
	reqGz := httptest.NewRequest("GET", "/", nil)
	reqGz.Header.Set("Accept-Encoding", "gzip")
	wGz := httptest.NewRecorder()
	srv.HandleDashboard(wGz, reqGz)

	if wGz.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for dashboard, got %d", wGz.Code)
	}
	if enc := wGz.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Expected Content-Encoding: gzip, got %q", enc)
	}

	// Decompress and verify content equals dashboardHTML
	gr, err := gzip.NewReader(wGz.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer gr.Close()
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("Failed to read decompressed bytes: %v", err)
	}
	if !bytes.Equal(decompressed, dashboardHTML) {
		t.Errorf("Decompressed dashboard content does not match original dashboardHTML (len %d vs %d)", len(decompressed), len(dashboardHTML))
	}

	// Verify size reduction: compressed dashboard should be significantly smaller
	compressedLen := len(getDashboardGzip())
	originalLen := len(dashboardHTML)
	if compressedLen >= originalLen {
		t.Errorf("Expected compressed length (%d) to be smaller than original (%d)", compressedLen, originalLen)
	}

	// 2. Dashboard without Accept-Encoding: gzip (raw bytes)
	reqRaw := httptest.NewRequest("GET", "/", nil)
	wRaw := httptest.NewRecorder()
	srv.HandleDashboard(wRaw, reqRaw)
	if wRaw.Header().Get("Content-Encoding") != "" {
		t.Errorf("Expected no Content-Encoding for raw request, got %q", wRaw.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(wRaw.Body.Bytes(), dashboardHTML) {
		t.Errorf("Raw response body did not match dashboardHTML")
	}

	// 3. API Stats with Accept-Encoding: gzip
	reqStatsGz := httptest.NewRequest("GET", "/api/stats?key="+srv.adminKey, nil)
	reqStatsGz.Header.Set("Accept-Encoding", "gzip")
	wStatsGz := httptest.NewRecorder()
	srv.HandleStats(wStatsGz, reqStatsGz)

	if wStatsGz.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for stats, got %d", wStatsGz.Code)
	}
	if enc := wStatsGz.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Expected Content-Encoding: gzip for stats, got %q", enc)
	}

	grStats, err := gzip.NewReader(wStatsGz.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader for stats: %v", err)
	}
	defer grStats.Close()
	var statsData map[string]interface{}
	if err := json.NewDecoder(grStats).Decode(&statsData); err != nil {
		t.Fatalf("Failed to decode decompressed stats JSON: %v", err)
	}
	if _, ok := statsData["host_count"]; !ok {
		t.Errorf("Expected host_count in decompressed stats")
	}
}
