package signaling

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/protocol"
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

	// 3. Test Stats API with valid key in header
	req = httptest.NewRequest("GET", "/api/stats", nil)
	req.Header.Set("X-Admin-Key", srv.adminKey)
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

	req := httptest.NewRequest("GET", "/api/history", nil)
	req.Header.Set("X-Admin-Key", srv.adminKey)
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
	reqStatsGz := httptest.NewRequest("GET", "/api/stats", nil)
	reqStatsGz.Header.Set("X-Admin-Key", srv.adminKey)
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

func TestSelfConnectionRejected(t *testing.T) {
	srv := NewServer()
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:]
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial test websocket: %v", err)
	}
	defer ws.Close()

	// Read initial status message
	var initMsg protocol.SignalingMessage
	if err := ws.ReadJSON(&initMsg); err != nil {
		t.Fatalf("Failed to read initial status: %v", err)
	}

	// Attempt self-connection with HostID matching TargetID
	selfConnect := protocol.SignalingMessage{
		Action:   protocol.ActionConnect,
		ID:       initMsg.ID,
		HostID:   "123456",
		TargetID: "123456",
	}
	if err := ws.WriteJSON(selfConnect); err != nil {
		t.Fatalf("Failed to send self-connect: %v", err)
	}

	// Expect ActionError rejecting self-connection
	var respMsg protocol.SignalingMessage
	if err := ws.ReadJSON(&respMsg); err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}
	if respMsg.Action != protocol.ActionError {
		t.Errorf("Expected ActionError for self-connection, got %q", respMsg.Action)
	}
	if !strings.Contains(respMsg.Message, "Auto-conexão não permitida") {
		t.Errorf("Expected auto-connection error message, got %q", respMsg.Message)
	}
}

func TestUnattendedAccessWithPassword(t *testing.T) {
	srv := NewServer()
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:]

	// 1. Host registers
	hostWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial host websocket: %v", err)
	}
	defer hostWS.Close()

	var hostInit protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostInit)

	_ = hostWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionRegister,
		ID:       "host999",
		Password: "secretPassword",
	})
	var hostRegResp protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostRegResp)

	// 2. Client connects with matching password
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial client websocket: %v", err)
	}
	defer clientWS.Close()

	var clientInit protocol.SignalingMessage
	_ = clientWS.ReadJSON(&clientInit)

	_ = clientWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionConnect,
		ID:       clientInit.ID,
		TargetID: "host999",
		Password: "secretPassword",
	})

	// Client should immediately receive auth_ok
	var clientResp protocol.SignalingMessage
	if err := clientWS.ReadJSON(&clientResp); err != nil {
		t.Fatalf("Failed to read client response: %v", err)
	}
	if clientResp.Action != protocol.ActionStatus || clientResp.Status != "auth_ok" {
		t.Fatalf("Expected immediate auth_ok for unattended access, got action=%s status=%s", clientResp.Action, clientResp.Status)
	}

	// Host should receive the connect message
	var hostMsg protocol.SignalingMessage
	if err := hostWS.ReadJSON(&hostMsg); err != nil {
		t.Fatalf("Failed to read host message: %v", err)
	}
	if hostMsg.Action != protocol.ActionConnect {
		t.Fatalf("Expected host to receive ActionConnect, got %s", hostMsg.Action)
	}
}

func TestAttendedAccessInteractiveApproval(t *testing.T) {
	srv := NewServer()
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:]

	// 1. Host registers
	hostWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial host websocket: %v", err)
	}
	defer hostWS.Close()

	var hostInit protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostInit)

	_ = hostWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionRegister,
		ID:       "host777",
		Password: "secureLocalPassword",
	})
	var hostRegResp protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostRegResp)

	// 2. Client connects WITHOUT password (or wrong password)
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial client websocket: %v", err)
	}
	defer clientWS.Close()

	var clientInit protocol.SignalingMessage
	_ = clientWS.ReadJSON(&clientInit)

	_ = clientWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionConnect,
		ID:       clientInit.ID,
		TargetID: "host777",
		Password: "", // No password supplied
		Alias:    "Suporte Victor",
	})

	// 3. Client should receive pending_approval
	var clientPending protocol.SignalingMessage
	if err := clientWS.ReadJSON(&clientPending); err != nil {
		t.Fatalf("Failed to read client pending status: %v", err)
	}
	if clientPending.Action != protocol.ActionStatus || clientPending.Status != "pending_approval" {
		t.Fatalf("Expected pending_approval, got action=%s status=%s", clientPending.Action, clientPending.Status)
	}

	// 4. Host should receive ActionIncomingRequest
	var hostReq protocol.SignalingMessage
	if err := hostWS.ReadJSON(&hostReq); err != nil {
		t.Fatalf("Failed to read host request: %v", err)
	}
	if hostReq.Action != protocol.ActionIncomingRequest {
		t.Fatalf("Expected ActionIncomingRequest on host, got %s", hostReq.Action)
	}
	if hostReq.ID != clientInit.ID {
		t.Fatalf("Expected requester ID %s, got %s", clientInit.ID, hostReq.ID)
	}

	// 5. Host approves the connection (ActionAccept)
	_ = hostWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionAccept,
		ID:       "host777",
		TargetID: clientInit.ID,
		Permissions: &protocol.SessionPermissions{
			AllowInput: true,
			AllowFiles: false,
			AllowAudio: true,
		},
	})

	// 6. Client receives auth_ok with granted permissions
	var clientApproved protocol.SignalingMessage
	if err := clientWS.ReadJSON(&clientApproved); err != nil {
		t.Fatalf("Failed to read client approval: %v", err)
	}
	if clientApproved.Action != protocol.ActionStatus || clientApproved.Status != "auth_ok" {
		t.Fatalf("Expected auth_ok after approval, got action=%s status=%s", clientApproved.Action, clientApproved.Status)
	}
	if clientApproved.Permissions == nil || clientApproved.Permissions.AllowFiles != false {
		t.Fatalf("Expected permissions to be preserved on client response")
	}

	// 7. Host receives the original connect message with permissions
	var hostConnMsg protocol.SignalingMessage
	if err := hostWS.ReadJSON(&hostConnMsg); err != nil {
		t.Fatalf("Failed to read host connect msg: %v", err)
	}
	if hostConnMsg.Action != protocol.ActionConnect {
		t.Fatalf("Expected ActionConnect on host, got %s", hostConnMsg.Action)
	}
}

func TestMasterKeyCannotAccessHostWithoutHostPassword(t *testing.T) {
	srv := NewServer()
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:]

	// 1. Host registers with its own unique fixed password
	hostWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial host: %v", err)
	}
	defer hostWS.Close()

	var hostInit protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostInit)

	_ = hostWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionRegister,
		ID:       "my_pc_100",
		Password: "MyRealFixedPassword_XYZ",
	})
	var hostRegResp protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostRegResp)

	// 2. Client attempts to connect using the server's admin key (or remote2026) instead of host's password
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial client: %v", err)
	}
	defer clientWS.Close()

	var clientInit protocol.SignalingMessage
	_ = clientWS.ReadJSON(&clientInit)

	_ = clientWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionConnect,
		ID:       clientInit.ID,
		TargetID: "my_pc_100",
		Password: "remote2026", // Master key should NOT bypass host password!
	})

	// Client must NOT get auth_ok! It must be immediately rejected with ActionError (Senha incorreta)
	var clientResp protocol.SignalingMessage
	if err := clientWS.ReadJSON(&clientResp); err != nil {
		t.Fatalf("Failed to read client response: %v", err)
	}
	if clientResp.Action == protocol.ActionStatus && clientResp.Status == "auth_ok" {
		t.Fatalf("SECURITY VIOLATION: Master key 'remote2026' was accepted for host access!")
	}
	if clientResp.Action != protocol.ActionError || !strings.Contains(clientResp.Message, "Senha incorreta") {
		t.Fatalf("Expected ActionError with 'Senha incorreta', got action=%s msg=%s", clientResp.Action, clientResp.Message)
	}

	// 3. Client attempts to send ActionOffer directly without authorization -> must be blocked
	_ = clientWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionOffer,
		ID:       clientInit.ID,
		TargetID: "my_pc_100",
		SDP:      "v=0\r\no=fake 123 123 IN IP4 127.0.0.1...",
	})

	var unauthResp protocol.SignalingMessage
	if err := clientWS.ReadJSON(&unauthResp); err != nil {
		t.Fatalf("Failed to read unauth response: %v", err)
	}
	if unauthResp.Action != protocol.ActionError || !strings.Contains(unauthResp.Message, "não autorizada") {
		t.Fatalf("Expected ActionError for unauthorized ActionOffer, got action=%s msg=%s", unauthResp.Action, unauthResp.Message)
	}
}

func TestLogs7DaysRetentionAndPagination(t *testing.T) {
	srv := NewServer()

	// Clear initial logs for precise test counting
	srv.logsMu.Lock()
	srv.logs = nil
	srv.legacyLogs = nil
	srv.logsMu.Unlock()

	// Add 5 log entries with distinct messages
	srv.addLog("Event Alpha - Connect")
	srv.addLog("Event Beta - Transfer")
	srv.addLog("Event Gamma - Screen Switch")
	srv.addLog("Event Delta - Disconnect")
	srv.addLog("Event Epsilon - Ping")

	// 1. Test Page 1 with limit 2
	req := httptest.NewRequest("GET", "/api/logs?page=1&limit=2", nil)
	req.Header.Set("X-Admin-Key", srv.adminKey)
	w := httptest.NewRecorder()
	srv.HandleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var resp struct {
		Entries       []LogEntry `json:"entries"`
		TotalCount    int        `json:"total_count"`
		TotalPages    int        `json:"total_pages"`
		Page          int        `json:"page"`
		Limit         int        `json:"limit"`
		RetentionDays int        `json:"retention_days"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.TotalCount != 5 {
		t.Errorf("Expected TotalCount 5, got %d", resp.TotalCount)
	}
	if resp.TotalPages != 3 {
		t.Errorf("Expected TotalPages 3, got %d", resp.TotalPages)
	}
	if len(resp.Entries) != 2 {
		t.Errorf("Expected 2 entries on page 1, got %d", len(resp.Entries))
	}
	// Newest first: Event Epsilon should be first
	if !strings.Contains(resp.Entries[0].Message, "Event Epsilon") {
		t.Errorf("Expected newest entry 'Event Epsilon' first, got %s", resp.Entries[0].Message)
	}

	// 2. Test Search filtering
	reqSearch := httptest.NewRequest("GET", "/api/logs?search=transfer", nil)
	reqSearch.Header.Set("X-Admin-Key", srv.adminKey)
	wSearch := httptest.NewRecorder()
	srv.HandleLogs(wSearch, reqSearch)

	var respSearch struct {
		Entries    []LogEntry `json:"entries"`
		TotalCount int        `json:"total_count"`
	}
	_ = json.NewDecoder(wSearch.Body).Decode(&respSearch)
	if respSearch.TotalCount != 1 || len(respSearch.Entries) != 1 {
		t.Fatalf("Expected 1 result for 'transfer', got %d", respSearch.TotalCount)
	}
	if !strings.Contains(respSearch.Entries[0].Message, "Event Beta") {
		t.Errorf("Expected 'Event Beta', got %s", respSearch.Entries[0].Message)
	}

	// 3. Test 7-Day Purge
	srv.logsMu.Lock()
	// Insert an expired log entry (8 days ago)
	srv.logs = append([]LogEntry{
		{
			Timestamp: time.Now().Add(-8 * 24 * time.Hour),
			TimeStr:   "Expired",
			Message:   "Old 8-day expired log",
		},
	}, srv.logs...)
	srv.logsMu.Unlock()

	srv.purgeExpiredLogs()

	srv.logsMu.RLock()
	for _, entry := range srv.logs {
		if strings.Contains(entry.Message, "Old 8-day expired") {
			t.Errorf("Expired 8-day log was NOT purged!")
		}
	}
	srv.logsMu.RUnlock()
}

func TestAdminRegistrationAndStatsBadge(t *testing.T) {
	srv := NewServer()
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	hostWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial host: %v", err)
	}
	defer hostWS.Close()

	var hostInit protocol.SignalingMessage
	_ = hostWS.ReadJSON(&hostInit)

	// Register with IsAdmin: true
	_ = hostWS.WriteJSON(protocol.SignalingMessage{
		Action:   protocol.ActionRegister,
		ID:       "admin_pc_1",
		Alias:    "CASAPC-Admin",
		Password: "secretPassword",
		IsAdmin:  true,
		Monitors: 2,
	})

	var regResp protocol.SignalingMessage
	if err := hostWS.ReadJSON(&regResp); err != nil {
		t.Fatalf("Failed to read register response: %v", err)
	}
	if !regResp.IsAdmin {
		t.Errorf("Expected IsAdmin: true in registration response, got false")
	}

	// Verify stats endpoint returns is_admin: true
	req := httptest.NewRequest("GET", "/api/stats", nil)
	req.Header.Set("X-Admin-Key", "remote2026")
	w := httptest.NewRecorder()
	srv.HandleStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 from HandleStats, got %d", w.Code)
	}

	var statsResp struct {
		Peers []struct {
			ID      string `json:"id"`
			Alias   string `json:"alias"`
			IsAdmin bool   `json:"is_admin"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &statsResp); err != nil {
		t.Fatalf("Failed to unmarshal stats response: %v", err)
	}

	found := false
	for _, p := range statsResp.Peers {
		if p.ID == "admin_pc_1" {
			found = true
			if !p.IsAdmin {
				t.Errorf("Expected peer is_admin: true in /api/stats, got false")
			}
		}
	}
	if !found {
		t.Errorf("Peer admin_pc_1 not found in /api/stats")
	}
}

func TestStaticIconsAndFavicon(t *testing.T) {
	srv := NewServer()

	// 1. Favicon.ico
	reqFav := httptest.NewRequest("GET", "/favicon.ico", nil)
	wFav := httptest.NewRecorder()
	srv.HandleDashboard(wFav, reqFav)
	if wFav.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for /favicon.ico, got %d", wFav.Code)
	}
	if ct := wFav.Header().Get("Content-Type"); ct != "image/x-icon" {
		t.Errorf("Expected Content-Type: image/x-icon, got %s", ct)
	}
	if wFav.Body.Len() == 0 {
		t.Errorf("Expected non-empty favicon body")
	}

	// 2. Icon.png
	reqIcon := httptest.NewRequest("GET", "/icon.png", nil)
	wIcon := httptest.NewRecorder()
	srv.HandleDashboard(wIcon, reqIcon)
	if wIcon.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for /icon.png, got %d", wIcon.Code)
	}
	if ct := wIcon.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Expected Content-Type: image/png, got %s", ct)
	}
	if wIcon.Body.Len() == 0 {
		t.Errorf("Expected non-empty icon body")
	}

	// 3. Icon_32.png
	reqIcon32 := httptest.NewRequest("GET", "/icon_32.png", nil)
	wIcon32 := httptest.NewRecorder()
	srv.HandleDashboard(wIcon32, reqIcon32)
	if wIcon32.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for /icon_32.png, got %d", wIcon32.Code)
	}
	if ct := wIcon32.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Expected Content-Type: image/png, got %s", ct)
	}
	if wIcon32.Body.Len() == 0 {
		t.Errorf("Expected non-empty icon_32 body")
	}
}
