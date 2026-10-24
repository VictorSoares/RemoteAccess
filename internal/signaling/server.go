package signaling

import (
	_ "embed"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/protocol"
	"remoteaccess/internal/wol"
)

//go:embed web/dashboard.html
var dashboardHTML []byte

var (
	dashboardGzOnce sync.Once
	dashboardHTMLGz []byte
)

func getDashboardGzip() []byte {
	dashboardGzOnce.Do(func() {
		var buf bytes.Buffer
		gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err == nil {
			_, _ = gz.Write(dashboardHTML)
			_ = gz.Close()
			dashboardHTMLGz = buf.Bytes()
		}
	})
	return dashboardHTMLGz
}

func writeJSON(w http.ResponseWriter, r *http.Request, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if r != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.WriteHeader(statusCode)
		gz := gzip.NewWriter(w)
		defer gz.Close()
		_ = json.NewEncoder(gz).Encode(data)
		return
	}
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // native desktop client
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		h := strings.ToLower(u.Hostname())
		return h == "remoteaccess-ltwx.onrender.com" ||
			h == "localhost" ||
			h == "127.0.0.1" ||
			strings.HasSuffix(h, ".onrender.com")
	},
}

type Peer struct {
	ID             string          `json:"id"`
	Alias          string          `json:"alias"`
	Monitors       int             `json:"monitors"`
	MAC            string          `json:"mac,omitempty"`
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
	ClientAlias string    `json:"client_alias"`
	Status      string    `json:"status"` // "completed", "active", "rejected"
}

type loginAttempt struct {
	count     int
	lastFail  time.Time
	lockUntil time.Time
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
	rateMu       sync.Mutex
	failedLogins map[string]*loginAttempt
}

func (s *Server) isRateLimited(clientIP, targetID string) bool {
	key := fmt.Sprintf("%s:%s", clientIP, cleanID(targetID))
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	attempt, exists := s.failedLogins[key]
	if !exists {
		return false
	}
	return time.Now().Before(attempt.lockUntil)
}

func (s *Server) recordFailedAttempt(clientIP, targetID string) {
	key := fmt.Sprintf("%s:%s", clientIP, cleanID(targetID))
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	attempt, exists := s.failedLogins[key]
	if !exists || time.Since(attempt.lastFail) > 2*time.Minute {
		s.failedLogins[key] = &loginAttempt{
			count:    1,
			lastFail: time.Now(),
		}
		return
	}

	attempt.count++
	attempt.lastFail = time.Now()
	if attempt.count >= 5 {
		attempt.lockUntil = time.Now().Add(60 * time.Second)
	}
}

func (s *Server) resetFailedAttempts(clientIP, targetID string) {
	key := fmt.Sprintf("%s:%s", clientIP, cleanID(targetID))
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	delete(s.failedLogins, key)
}

func NewServer() *Server {
	secret := strings.TrimSpace(os.Getenv("ADMIN_SECRET"))
	if secret == "" {
		secret = "remote2026"
		log.Printf("[Signaling] ADMIN_SECRET não configurado no ambiente. Utilizando chave padrão: %s", secret)
	}

	s := &Server{
		peers:        make(map[string]*Peer),
		startTime:    time.Now(),
		logs:         make([]string, 0, 200),
		history:      make([]SessionRecord, 0, 500),
		adminKey:     secret,
		failedLogins: make(map[string]*loginAttempt),
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

func (s *Server) endHistorySession(hostID, clientID string) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	cleanHost := cleanID(hostID)
	cleanClient := cleanID(clientID)
	now := time.Now()
	for i := range s.history {
		if s.history[i].Status == "active" {
			hID := cleanID(s.history[i].HostID)
			cID := cleanID(s.history[i].ClientID)
			if hID == cleanHost || cID == cleanHost || hID == cleanClient || cID == cleanClient {
				s.history[i].Status = "completed"
				s.history[i].EndedAt = now
				s.history[i].DurationSec = int(now.Sub(s.history[i].StartedAt).Seconds())
			}
		}
	}
}

func (s *Server) checkAdminAuth(r *http.Request) bool {
	var key string
	// 1. Check Header X-Admin-Key
	if h := r.Header.Get("X-Admin-Key"); h != "" {
		key = h
	} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		key = strings.TrimPrefix(auth, "Bearer ")
	} else if cookie, err := r.Cookie("admin_key"); err == nil && cookie.Value != "" {
		key = cookie.Value
	} else if cookie, err := r.Cookie("ra_admin_key"); err == nil && cookie.Value != "" {
		key = cookie.Value
	}
	if key == "" || s.adminKey == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(s.adminKey)) == 1
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
		activeTarget := peer.ActiveTargetID
		peerAlias := peer.Alias
		if peerAlias == "" {
			peerAlias = "Dispositivo"
		}
		isHost := peer.IsHost
		delete(s.peers, peer.ID)
		if peer.ID != peerID {
			delete(s.peers, peerID)
		}
		if activeTarget != "" {
			if targetPeer, ok := s.peers[activeTarget]; ok {
				targetPeer.ActiveTargetID = ""
				// Não envia ActionClose: conexões WebRTC P2P diretas continuam ativas mesmo se o WebSocket de sinalização oscilar
			}
		}
		s.mu.Unlock()
		if activeTarget != "" {
			s.endHistorySession(peer.ID, activeTarget)
			s.addLog("Sessão encerrada por desconexão de %s (%s) com %s", peerAlias, peer.ID, activeTarget)
		}
		if isHost {
			s.addLog("Host desconectado: %s (%s)", peerAlias, peer.ID)
		} else {
			s.addLog("Controlador/Cliente desconectado: %s (%s)", peerAlias, peer.ID)
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
			if msg.Monitors > 0 {
				peer.Monitors = msg.Monitors
			}
			if msg.MAC != "" {
				peer.MAC = msg.MAC
			}
			peer.IsHost = true
			peer.Password = msg.Password
			s.peers[msg.ID] = peer
			s.mu.Unlock()

			s.addLog("Host registrado: %s [Nome: %s, Monitores: %d, MAC: %s] (Pronto para conexões)", msg.ID, peer.Alias, peer.Monitors, peer.MAC)
			_ = conn.WriteJSON(protocol.SignalingMessage{
				Action:   protocol.ActionStatus,
				Status:   "registered",
				Message:  "Host registrado com sucesso no Relay",
				ID:       msg.ID,
				Alias:    peer.Alias,
				Monitors: peer.Monitors,
				MAC:      peer.MAC,
			})

		case protocol.ActionUnregister:
			s.mu.Lock()
			activeTarget := peer.ActiveTargetID
			delete(s.peers, peer.ID)
			delete(s.peers, msg.ID)
			if activeTarget != "" {
				if targetPeer, ok := s.peers[activeTarget]; ok {
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
			if activeTarget != "" {
				s.endHistorySession(peer.ID, activeTarget)
			}
			s.addLog("Host %s enviou aviso de desligamento (Desconectado)", msg.ID)
			return

		case protocol.ActionConnect:
			if (msg.TargetID != "" && msg.TargetID == peer.ID) || (msg.HostID != "" && msg.HostID == msg.TargetID) {
				s.addLog("Conexão recusada: Tentativa de auto-conexão (HostID == TargetID: %s)", msg.TargetID)
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Auto-conexão não permitida: Você não pode controlar seu próprio computador remotamente na mesma sessão.",
				})
				continue
			}

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

			clientIP := strings.Split(conn.RemoteAddr().String(), ":")[0]
			if s.isRateLimited(clientIP, msg.TargetID) {
				s.addLog("Conexão recusada: Rate-limit de tentativas excedido para ID %s (IP %s)", msg.TargetID, clientIP)
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Muitas tentativas com senha incorreta. Aguarde 60 segundos.",
				})
				continue
			}

			if targetPeer.Password != "" {
				matchPeer := subtle.ConstantTimeCompare([]byte(targetPeer.Password), []byte(msg.Password)) == 1
				matchMaster := s.adminKey != "" && subtle.ConstantTimeCompare([]byte(s.adminKey), []byte(msg.Password)) == 1
				if !matchPeer && !matchMaster {
					s.recordFailedAttempt(clientIP, msg.TargetID)
					s.addLog("Autenticação rejeitada para ID %s (senha incorreta, IP %s)", msg.TargetID, clientIP)
					_ = conn.WriteJSON(protocol.SignalingMessage{
						Action:  protocol.ActionError,
						Message: "Senha incorreta.",
					})
					continue
				}
			}
			s.resetFailedAttempts(clientIP, msg.TargetID)

			if msg.ID == "" {
				msg.ID = peer.ID
			}
			if msg.Alias != "" {
				peer.Alias = msg.Alias
			}
			peer.IsHost = false

			peer.ActiveTargetID = msg.TargetID
			targetPeer.ActiveTargetID = peer.ID

			s.addLog("Sessão iniciada: %s (%s) está controlando o Host %s (%s)", peer.Alias, peer.ID, targetPeer.Alias, msg.TargetID)

			// Record in session history
			s.addHistory(SessionRecord{
				ID:          fmt.Sprintf("sess_%d", time.Now().UnixNano()),
				StartedAt:   time.Now(),
				HostID:      targetPeer.ID,
				HostAlias:   targetPeer.Alias,
				ClientID:    peer.ID,
				ClientAlias: peer.Alias,
				Status:      "active",
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
			// Guard: Drop any large payload packets (video relay blocked - pure P2P only to save server bandwidth)
			if msg.Action == protocol.ActionData && len(msg.Payload) > 8192 {
				continue
			}

			s.mu.RLock()
			targetPeer, exists := s.peers[msg.TargetID]
			if !exists {
				// Try matching by cleaned ID
				for _, p := range s.peers {
					if cleanID(p.ID) == cleanID(msg.TargetID) {
						targetPeer = p
						exists = true
						break
					}
				}
			}
			if !exists && peer.ActiveTargetID != "" {
				if tp, ok := s.peers[peer.ActiveTargetID]; ok {
					targetPeer = tp
					exists = true
				}
			}
			s.mu.RUnlock()

			if exists {
				if msg.ID == "" {
					msg.ID = peer.ID
				}
				if msg.Action == protocol.ActionClose {
					pAlias := peer.Alias
					if pAlias == "" {
						pAlias = peer.ID
					}
					tAlias := targetPeer.Alias
					if tAlias == "" {
						tAlias = targetPeer.ID
					}
					s.addLog("Sessão finalizada: %s (%s) encerrou a conexão com %s (%s)", pAlias, peer.ID, tAlias, targetPeer.ID)
					s.mu.Lock()
					peer.ActiveTargetID = ""
					targetPeer.ActiveTargetID = ""
					s.mu.Unlock()
					s.endHistorySession(peer.ID, targetPeer.ID)
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

	if subtle.ConstantTimeCompare([]byte(req.Key), []byte(s.adminKey)) == 1 {
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_key",
			Value:    s.adminKey,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 30, // 30 days
		})
		writeJSON(w, r, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"auth":   true,
		})
		return
	}

	writeJSON(w, r, http.StatusUnauthorized, map[string]interface{}{
		"status":  "error",
		"message": "Chave de acesso do cluster incorreta.",
	})
}

func (s *Server) HandleStats(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		writeJSON(w, r, http.StatusUnauthorized, map[string]interface{}{
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
		Monitors       int    `json:"monitors"`
		MAC            string `json:"mac,omitempty"`
		IsHost         bool   `json:"is_host"`
		HasPassword    bool   `json:"has_password"`
		DurationSec    int    `json:"duration_sec"`
		RemoteAddr     string `json:"remote_addr"`
		ActiveTargetID string `json:"active_target_id"`
		Blocked        bool   `json:"blocked"`
		Status         string `json:"status"`
	}

	peerList := make([]PeerInfo, 0)
	hostCount := 0
	activeSessions := 0

	// 1. Index active clients controlling hosts
	// targetHostID -> clientPeer
	controllingClients := make(map[string]*Peer)
	// clientIP / clientAlias -> targetHostID
	clientControllerMap := make(map[string]string)

	for _, p := range s.peers {
		if !p.IsHost && p.ActiveTargetID != "" {
			controllingClients[cleanID(p.ActiveTargetID)] = p
			controllingClients[p.ActiveTargetID] = p
			if p.Alias != "" {
				clientControllerMap[p.Alias] = p.ActiveTargetID
			}
			hostIP := strings.Split(p.RemoteAddr, ":")[0]
			if hostIP != "" {
				clientControllerMap[hostIP] = p.ActiveTargetID
			}
		}
	}

	for _, p := range s.peers {
		// Only list registered host machines in the cluster
		if !p.IsHost {
			continue
		}

		hostCount++
		status := "Online / Livre"

		if p.Blocked {
			status = "Bloqueado (Admin)"
		} else if p.ActiveTargetID != "" || controllingClients[cleanID(p.ID)] != nil || controllingClients[p.ID] != nil {
			// This host is currently being controlled
			activeSessions++
			clientPeer := controllingClients[cleanID(p.ID)]
			if clientPeer == nil {
				clientPeer = controllingClients[p.ID]
			}
			callerName := "Controlador Remoto"
			if clientPeer != nil {
				if clientPeer.Alias != "" && clientPeer.Alias != "Não definido" {
					callerName = clientPeer.Alias
				} else {
					callerName = formatID(clientPeer.ID)
				}
			}
			status = fmt.Sprintf("Sendo controlado por %s", callerName)
		} else {
			// Check if this host is actively controlling another machine
			hostIP := strings.Split(p.RemoteAddr, ":")[0]
			targetID := ""
			if p.Alias != "" && clientControllerMap[p.Alias] != "" {
				targetID = clientControllerMap[p.Alias]
			} else if hostIP != "" && clientControllerMap[hostIP] != "" {
				targetID = clientControllerMap[hostIP]
			}

			if targetID != "" {
				targetLabel := formatID(targetID)
				for _, other := range s.peers {
					if other.ID == targetID || cleanID(other.ID) == cleanID(targetID) {
						if other.Alias != "" && other.Alias != "Não definido" {
							targetLabel = fmt.Sprintf("%s (%s)", other.Alias, formatID(other.ID))
						}
						break
					}
				}
				status = fmt.Sprintf("Controlando %s", targetLabel)
			}
		}

		mons := p.Monitors
		if mons <= 0 {
			mons = 1
		}

		peerList = append(peerList, PeerInfo{
			ID:             p.ID,
			Alias:          p.Alias,
			Monitors:       mons,
			MAC:            p.MAC,
			IsHost:         p.IsHost,
			HasPassword:    p.Password != "",
			DurationSec:    int(time.Since(p.ConnectedAt).Seconds()),
			RemoteAddr:     p.RemoteAddr,
			ActiveTargetID: p.ActiveTargetID,
			Blocked:        p.Blocked,
			Status:         status,
		})
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":          "online",
		"uptime_sec":      int(time.Since(s.startTime).Seconds()),
		"total_online":    hostCount,
		"host_count":      hostCount,
		"client_count":    len(controllingClients),
		"active_sessions": activeSessions,
		"total_packets":   atomic.LoadUint64(&s.totalPackets),
		"peers":           peerList,
	})
}

func (s *Server) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		writeJSON(w, r, http.StatusUnauthorized, map[string]interface{}{
			"status":  "unauthorized",
			"message": "Acesso protegido.",
		})
		return
	}

	s.historyMu.RLock()
	defer s.historyMu.RUnlock()

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"history": s.history,
	})
}

func (s *Server) HandleBlockPeer(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		writeJSON(w, r, http.StatusUnauthorized, map[string]interface{}{
			"status":  "unauthorized",
			"message": "Acesso protegido.",
		})
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
			activeTarget := peer.ActiveTargetID
			// Terminate active session if blocked
			if targetPeer, ok := s.peers[activeTarget]; ok {
				targetPeer.ActiveTargetID = ""
				_ = targetPeer.Conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionClose,
					Message: "Sessão encerrada por bloqueio administrativo do Host.",
				})
			}
			peer.ActiveTargetID = ""
			s.endHistorySession(peer.ID, activeTarget)
		}
		s.addLog("Status de bloqueio do Host %s alterado para: %v", req.ID, req.Block)
	}
	s.mu.Unlock()

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"id":      req.ID,
		"blocked": req.Block,
		"found":   exists,
	})
}

func (s *Server) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		writeJSON(w, r, http.StatusUnauthorized, map[string]interface{}{
			"status":  "unauthorized",
			"message": "Acesso protegido.",
		})
		return
	}

	s.logsMu.RLock()
	defer s.logsMu.RUnlock()

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
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
		// If peer was in an active session, notify the other peer and end session!
		if peer.ActiveTargetID != "" {
			activeTarget := peer.ActiveTargetID
			if targetPeer, ok := s.peers[activeTarget]; ok {
				targetPeer.ActiveTargetID = ""
				targetPeer.mu.Lock()
				_ = targetPeer.Conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionClose,
					Message: "A sessão foi encerrada pelo administrador do servidor.",
				})
				targetPeer.mu.Unlock()
			}
			peer.ActiveTargetID = ""
			s.endHistorySession(peer.ID, activeTarget)
		}

		_ = peer.Conn.WriteJSON(protocol.SignalingMessage{
			Action:  protocol.ActionClose,
			Message: "Conexão e sessão encerradas pelo administrador do servidor.",
		})
		_ = peer.Conn.Close()
		delete(s.peers, req.ID)
		s.addLog("Dispositivo %s teve sua conexão e sessão encerradas pelo painel administrativo", req.ID)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"kicked": exists,
	})
}

func (s *Server) HandleWoL(w http.ResponseWriter, r *http.Request) {
	if !s.checkAdminAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.MAC) == "" {
		http.Error(w, "Endereço MAC inválido", http.StatusBadRequest)
		return
	}

	cleanMAC := strings.TrimSpace(req.MAC)
	err := wol.SendMagicPacket(cleanMAC)
	s.addLog("Pacote Wake-on-LAN disparado para o MAC: %s", cleanMAC)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": fmt.Sprintf("Erro ao enviar pacote WoL: %v", err),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("Pacote Magic Packet Wake-on-LAN transmitido para %s", cleanMAC),
	})
}

func (s *Server) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		gzData := getDashboardGzip()
		if len(gzData) > 0 {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Vary", "Accept-Encoding")
			w.Write(gzData)
			return
		}
	}

	w.Write(dashboardHTML)
}

func cleanID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatID(id string) string {
	if id == "" {
		return "--- ---"
	}
	clean := cleanID(id)
	if len(clean) == 6 {
		return clean[:3] + " " + clean[3:]
	}
	return id
}



