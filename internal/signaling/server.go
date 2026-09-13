package signaling

import (
	_ "embed"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/protocol"
)

//go:embed web/dashboard.html
var dashboardHTML []byte

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Peer struct {
	ID             string          `json:"id"`
	Alias          string          `json:"alias"`
	IsHost         bool            `json:"is_host"`
	Password       string          `json:"password,omitempty"`
	RemoteAddr     string          `json:"remote_addr"`
	ConnectedAt    time.Time       `json:"connected_at"`
	ActiveTargetID string          `json:"active_target_id"`
	Blocked        bool            `json:"blocked"`
	Conn           *websocket.Conn `json:"-"`
	mu             sync.Mutex
}

type SessionRecord struct {
	ID          string    `json:"id"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at,omitempty"`
	DurationSec int       `json:"duration_sec"`
	HostID      string    `json:"host_id"`
	HostAlias   string    `json:"host_alias"`
	ClientID    string    `json:"client_id"`
	Status      string    `json:"status"` // "completed", "active", "rejected"
}

type Server struct {
	mu           sync.RWMutex
	peers        map[string]*Peer
	startTime    time.Time
	totalPackets uint64
	logsMu       sync.RWMutex
	logs         []string
	historyMu    sync.RWMutex
	history      []SessionRecord
	adminKey     string
}

func NewServer() *Server {
	secret := os.Getenv("ADMIN_SECRET")
	if strings.TrimSpace(secret) == "" {
		secret = "remote2026"
	}

	s := &Server{
		peers:     make(map[string]*Peer),
		startTime: time.Now(),
		logs:      make([]string, 0, 200),
		history:   make([]SessionRecord, 0, 500),
		adminKey:  strings.TrimSpace(secret),
	}
	s.addLog("Servidor Cloud Relay inicializado com sucesso. Chave Admin configurada.")
	return s
}

func (s *Server) addHistory(rec SessionRecord) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.history = append([]SessionRecord{rec}, s.history...)
	if len(s.history) > 500 {
		s.history = s.history[:500]
	}
}

func (s *Server) checkAdminAuth(r *http.Request) bool {
	// 1. Check Header
	if key := r.Header.Get("X-Admin-Key"); key == s.adminKey {
		return true
	}
	// 2. Check Query Param
	if key := r.URL.Query().Get("key"); key == s.adminKey {
		return true
	}
	// 3. Check Cookie
	if cookie, err := r.Cookie("admin_key"); err == nil && cookie.Value == s.adminKey {
		return true
	}
	// 4. Check Bearer Authorization
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		if strings.TrimPrefix(auth, "Bearer ") == s.adminKey {
			return true
		}
	}
	return false
}

func (s *Server) addLog(format string, a ...interface{}) {
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
	log.Println("[Signaling]", entry)

	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
}

func generatePeerID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900000))
	return fmt.Sprintf("c_%06d", n.Int64()+100000)
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.addLog("Erro no upgrade de WebSocket: %v", err)
		return
	}
	defer conn.Close()

	peerID := generatePeerID()
	peer := &Peer{
		ID:          peerID,
		IsHost:      false,
		RemoteAddr:  r.RemoteAddr,
		ConnectedAt: time.Now(),
		Conn:        conn,
	}

	s.mu.Lock()
	s.peers[peerID] = peer
	s.mu.Unlock()

	_ = conn.WriteJSON(protocol.SignalingMessage{
		Action:  protocol.ActionStatus,
		Status:  "connected",
		Message: "Conectado ao servidor de sinalização",
		ID:      peerID,
	})

	defer func() {
		s.mu.Lock()
		delete(s.peers, peer.ID)
		if peer.ID != peerID {
			delete(s.peers, peerID)
		}
		if peer.ActiveTargetID != "" {
			if targetPeer, ok := s.peers[peer.ActiveTargetID]; ok {
				targetPeer.ActiveTargetID = ""
				targetPeer.mu.Lock()
				_ = targetPeer.Conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionClose,
					Message: "A outra ponta foi desconectada.",
				})
				targetPeer.mu.Unlock()
			}
		}
		s.mu.Unlock()
		if peer.IsHost {
			s.addLog("Host desconectado: %s", peer.ID)
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		atomic.AddUint64(&s.totalPackets, 1)

		var msg protocol.SignalingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		if msg.ID != "" && msg.ID != peer.ID {
			s.mu.Lock()
			delete(s.peers, peer.ID)
			peer.ID = msg.ID
			s.peers[msg.ID] = peer
			s.mu.Unlock()
		}

		switch msg.Action {
		case protocol.ActionRegister:
			s.mu.Lock()
			delete(s.peers, peer.ID)
			peer.ID = msg.ID
			if msg.Alias != "" {
				peer.Alias = msg.Alias
			}
			peer.IsHost = true
			peer.Password = msg.Password
			s.peers[msg.ID] = peer
			s.mu.Unlock()

			s.addLog("Host registrado: %s [Nome: %s] (Pronto para conexões)", msg.ID, peer.Alias)
			_ = conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "registered",
				Message: "Host registrado com sucesso no Relay",
				ID:      msg.ID,
				Alias:   peer.Alias,
			})

		case protocol.ActionUnregister:
			s.mu.Lock()
			delete(s.peers, peer.ID)
			delete(s.peers, msg.ID)
			if peer.ActiveTargetID != "" {
				if targetPeer, ok := s.peers[peer.ActiveTargetID]; ok {
					targetPeer.ActiveTargetID = ""
					targetPeer.mu.Lock()
					_ = targetPeer.Conn.WriteJSON(protocol.SignalingMessage{
						Action:  protocol.ActionClose,
						Message: "Host encerrou o aplicativo.",
					})
					targetPeer.mu.Unlock()
				}
			}
			s.mu.Unlock()
			s.addLog("Host %s enviou aviso de desligamento (Desconectado)", msg.ID)
			return

		case protocol.ActionConnect:
			s.mu.RLock()
			targetPeer, exists := s.peers[msg.TargetID]
			s.mu.RUnlock()

			if !exists {
				s.addLog("Tentativa de conexão falhou: ID %s offline ou inexistente", msg.TargetID)
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "ID não encontrado ou máquina remota está offline.",
				})
				continue
			}

			if targetPeer.Blocked {
				s.addLog("Conexão recusada: Host %s está temporariamente bloqueado pelo administrador", msg.TargetID)
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Este computador está temporariamente bloqueado para novas conexões remotas.",
				})
				continue
			}

			if targetPeer.Password != "" && targetPeer.Password != msg.Password {
				s.addLog("Autenticação rejeitada para ID %s (senha incorreta)", msg.TargetID)
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Senha incorreta.",
				})
				continue
			}

			if msg.ID == "" {
				msg.ID = peer.ID
			}

			peer.ActiveTargetID = msg.TargetID
			targetPeer.ActiveTargetID = peer.ID

			s.addLog("Sessão iniciada: %s está controlando o Host %s (%s)", peer.ID, msg.TargetID, targetPeer.Alias)

			// Record in session history
			s.addHistory(SessionRecord{
				ID:        fmt.Sprintf("sess_%d", time.Now().UnixNano()),
				StartedAt: time.Now(),
				HostID:    targetPeer.ID,
				HostAlias: targetPeer.Alias,
				ClientID:  peer.ID,
				Status:    "active",
			})

			targetPeer.mu.Lock()
			_ = targetPeer.Conn.WriteJSON(msg)
			targetPeer.mu.Unlock()

			_ = conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "auth_ok",
				Message: "Autenticado com sucesso! Conectando à tela remota...",
				ID:      msg.TargetID,
				Alias:   targetPeer.Alias,
			})

		case protocol.ActionOffer, protocol.ActionAnswer, protocol.ActionCandidate, protocol.ActionData, protocol.ActionClose:
			s.mu.RLock()
			targetPeer, exists := s.peers[msg.TargetID]
			s.mu.RUnlock()

			if exists {
				if msg.ID == "" {
					msg.ID = peer.ID
				}
				if msg.Action == protocol.ActionClose {
					s.addLog("Sessão finalizada entre %s e %s", peer.ID, msg.TargetID)
					peer.ActiveTargetID = ""
					targetPeer.ActiveTargetID = ""
				}
				targetPeer.mu.Lock()
				_ = targetPeer.Conn.WriteJSON(msg)
				targetPeer.mu.Unlock()
			}
		}
	}
}

func (s *Server) HandleAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Requisição inválida", http.StatusBadRequest)
		return
	}

	if req.Key == s.adminKey {
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_key",
			Value:    s.adminKey,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 30, // 30 days
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"auth":   true,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "error",
		"message": "Chave de acesso do cluster incorreta.",
	})
}

func (s *Server) HandleStats(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "unauthorized",
			"message": "Acesso protegido. Informe a chave do cluster.",
		})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type PeerInfo struct {
		ID             string `json:"id"`
		Alias          string `json:"alias"`
		IsHost         bool   `json:"is_host"`
		Password       string `json:"password,omitempty"`
		DurationSec    int    `json:"duration_sec"`
		RemoteAddr     string `json:"remote_addr"`
		ActiveTargetID string `json:"active_target_id"`
		Blocked        bool   `json:"blocked"`
		Status         string `json:"status"`
	}

	peerList := make([]PeerInfo, 0)
	hostCount := 0
	clientCount := 0
	activeSessions := 0

	for _, p := range s.peers {
		// Only list registered Hosts or clients in active sessions
		if !p.IsHost && p.ActiveTargetID == "" {
			continue
		}

		status := "Online / Livre"
		if p.Blocked {
			status = "Bloqueado (Admin)"
		} else if p.ActiveTargetID != "" {
			status = fmt.Sprintf("Em Sessão com %s", p.ActiveTargetID)
			activeSessions++
		}

		if p.IsHost {
			hostCount++
		} else {
			clientCount++
		}

		peerList = append(peerList, PeerInfo{
			ID:             p.ID,
			Alias:          p.Alias,
			IsHost:         p.IsHost,
			Password:       p.Password,
			DurationSec:    int(time.Since(p.ConnectedAt).Seconds()),
			RemoteAddr:     p.RemoteAddr,
			ActiveTargetID: p.ActiveTargetID,
			Blocked:        p.Blocked,
			Status:         status,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "online",
		"uptime_sec":      int(time.Since(s.startTime).Seconds()),
		"total_online":    hostCount + clientCount,
		"host_count":      hostCount,
		"client_count":    clientCount,
		"active_sessions": activeSessions / 2, // Host + Client pairs
		"total_packets":   atomic.LoadUint64(&s.totalPackets),
		"peers":           peerList,
	})
}

func (s *Server) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.historyMu.RLock()
	defer s.historyMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"history": s.history,
	})
}

func (s *Server) HandleBlockPeer(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID    string `json:"id"`
		Block bool   `json:"block"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "ID inválido", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	peer, exists := s.peers[req.ID]
	if exists {
		peer.Blocked = req.Block
		if req.Block && peer.ActiveTargetID != "" {
			// Terminate active session if blocked
			if targetPeer, ok := s.peers[peer.ActiveTargetID]; ok {
				targetPeer.ActiveTargetID = ""
				_ = targetPeer.Conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionClose,
					Message: "Sessão encerrada por bloqueio administrativo do Host.",
				})
			}
			peer.ActiveTargetID = ""
		}
		s.addLog("Status de bloqueio do Host %s alterado para: %v", req.ID, req.Block)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"id":      req.ID,
		"blocked": req.Block,
		"found":   exists,
	})
}

func (s *Server) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.logsMu.RLock()
	defer s.logsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": s.logs,
	})
}

func (s *Server) HandleKick(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "ID inválido", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	peer, exists := s.peers[req.ID]
	if exists {
		_ = peer.Conn.WriteJSON(protocol.SignalingMessage{
			Action:  protocol.ActionError,
			Message: "Conexão encerrada pelo administrador do servidor.",
		})
		_ = peer.Conn.Close()
		delete(s.peers, req.ID)
		s.addLog("Dispositivo %s foi desconectado pelo painel administrativo", req.ID)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"kicked": exists,
	})
}

func (s *Server) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}



