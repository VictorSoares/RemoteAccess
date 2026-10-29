package signaling

import (
	_ "embed"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

//go:embed web/icon_32.png
var icon32PNG []byte

//go:embed web/icon.png
var iconPNG []byte

//go:embed web/favicon.ico
var faviconICO []byte

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
	IsAdmin        bool            `json:"is_admin,omitempty"`
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

type PendingRequest struct {
	ClientID    string
	ClientAlias string
	ClientIP    string
	TargetID    string
	ClientConn  *websocket.Conn
	RequestedAt time.Time
	OriginalMsg protocol.SignalingMessage
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	TimeStr   string    `json:"time"`
	Message   string    `json:"message"`
}

type Server struct {
	mu              sync.RWMutex
	peers           map[string]*Peer
	startTime       time.Time
	totalPackets    uint64
	logsMu          sync.RWMutex
	logs            []LogEntry
	legacyLogs      []string
	historyMu       sync.RWMutex
	history         []SessionRecord
	adminKey        string
	rateMu          sync.Mutex
	failedLogins    map[string]*loginAttempt
	pendingMu       sync.Mutex
	pendingRequests map[string]*PendingRequest
	logFilePath     string
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

	logDir := os.Getenv("SIGNALING_DATA_DIR")
	if logDir == "" {
		progData := os.Getenv("ProgramData")
		if progData != "" {
			logDir = filepath.Join(progData, "RemoteAccess")
		} else {
			logDir = "."
		}
	}
	_ = os.MkdirAll(logDir, 0755)
	logFilePath := filepath.Join(logDir, "signaling_events.jsonl")

	s := &Server{
		peers:           make(map[string]*Peer),
		startTime:       time.Now(),
		logs:            make([]LogEntry, 0, 500),
		legacyLogs:      make([]string, 0, 200),
		history:         make([]SessionRecord, 0, 500),
		adminKey:        secret,
		failedLogins:    make(map[string]*loginAttempt),
		pendingRequests: make(map[string]*PendingRequest),
		logFilePath:     logFilePath,
	}

	// Carrega histórico de logs dos últimos 7 dias gravados em disco
	s.loadPersistedLogs()

	// Goroutine de purga automática de logs com mais de 7 dias
	go s.startRetentionCleaner()

	s.addLog("Servidor Cloud Relay inicializado com sucesso. Chave Admin configurada.")
	return s
}

func (s *Server) loadPersistedLogs() {
	if s.logFilePath == "" {
		return
	}
	file, err := os.Open(s.logFilePath)
	if err != nil {
		return
	}
	defer file.Close()

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err == nil {
			if !entry.Timestamp.Before(cutoff) {
				s.logs = append(s.logs, entry)
				legacyStr := fmt.Sprintf("[%s] %s", entry.Timestamp.Format("15:04:05"), entry.Message)
				s.legacyLogs = append(s.legacyLogs, legacyStr)
				if len(s.legacyLogs) > 200 {
					s.legacyLogs = s.legacyLogs[len(s.legacyLogs)-200:]
				}
			}
		}
	}
}

func (s *Server) startRetentionCleaner() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		s.purgeExpiredLogs()
	}
}

func (s *Server) purgeExpiredLogs() {
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	s.logsMu.Lock()
	defer s.logsMu.Unlock()

	idx := 0
	for idx < len(s.logs) && s.logs[idx].Timestamp.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		s.logs = s.logs[idx:]
		if s.logFilePath != "" {
			tmpPath := s.logFilePath + ".tmp"
			f, err := os.Create(tmpPath)
			if err == nil {
				w := bufio.NewWriter(f)
				for _, entry := range s.logs {
					if b, err := json.Marshal(entry); err == nil {
						_, _ = w.Write(b)
						_ = w.WriteByte('\n')
					}
				}
				_ = w.Flush()
				_ = f.Close()
				_ = os.Rename(tmpPath, s.logFilePath)
			}
		}
	}
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
	now := time.Now()
	msg := fmt.Sprintf(format, a...)
	entry := LogEntry{
		Timestamp: now,
		TimeStr:   now.Format("02/01 15:04:05"),
		Message:   msg,
	}
	legacyEntry := fmt.Sprintf("[%s] %s", now.Format("15:04:05"), msg)
	log.Println("[Signaling]", legacyEntry)

	s.logsMu.Lock()
	s.logs = append(s.logs, entry)
	s.legacyLogs = append(s.legacyLogs, legacyEntry)
	if len(s.legacyLogs) > 200 {
		s.legacyLogs = s.legacyLogs[len(s.legacyLogs)-200:]
	}
	filePath := s.logFilePath
	s.logsMu.Unlock()

	if filePath != "" {
		go func(e LogEntry, path string) {
			b, err := json.Marshal(e)
			if err == nil {
				f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if err == nil {
					_, _ = f.Write(append(b, '\n'))
					_ = f.Close()
				}
			}
		}(entry, filePath)
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
		// Apenas remove de s.peers se o peer registrado ainda for esta exata conexão
		if currentPeer, ok := s.peers[peer.ID]; ok && currentPeer == peer {
			delete(s.peers, peer.ID)
		}
		if peer.ID != peerID {
			if currentPeer, ok := s.peers[peerID]; ok && currentPeer == peer {
				delete(s.peers, peerID)
			}
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
		s.pendingMu.Lock()
		delete(s.pendingRequests, peer.ID)
		for cid, req := range s.pendingRequests {
			if req.TargetID == peer.ID {
				delete(s.pendingRequests, cid)
				_ = req.ClientConn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Host desconectou-se do servidor de sinalização.",
				})
			}
		}
		s.pendingMu.Unlock()
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
			// Se já existe uma conexão anterior registrada sob este ID, fecha a conexão antiga
			if oldPeer, exists := s.peers[msg.ID]; exists && oldPeer != peer {
				if oldPeer.Conn != nil {
					_ = oldPeer.Conn.Close()
				}
			}
			if currentPeer, ok := s.peers[peer.ID]; ok && currentPeer == peer {
				delete(s.peers, peer.ID)
			}
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
			peer.IsAdmin = msg.IsAdmin
			peer.Password = msg.Password
			s.peers[msg.ID] = peer
			s.mu.Unlock()

			adminTag := ""
			if msg.IsAdmin {
				adminTag = " [🛡️ Modo Admin]"
			}
			s.addLog("Host registrado: %s [Nome: %s, Monitores: %d, MAC: %s]%s (Pronto para conexões)", msg.ID, peer.Alias, peer.Monitors, peer.MAC, adminTag)
			_ = conn.WriteJSON(protocol.SignalingMessage{
				Action:   protocol.ActionStatus,
				Status:   "registered",
				Message:  "Host registrado com sucesso no Relay",
				ID:       msg.ID,
				Alias:    peer.Alias,
				Monitors: peer.Monitors,
				MAC:      peer.MAC,
				IsAdmin:  peer.IsAdmin,
			})

		case protocol.ActionUnregister:
			s.mu.Lock()
			activeTarget := peer.ActiveTargetID
			if currentPeer, ok := s.peers[peer.ID]; ok && currentPeer == peer {
				delete(s.peers, peer.ID)
			}
			if currentPeer, ok := s.peers[msg.ID]; ok && currentPeer == peer {
				delete(s.peers, msg.ID)
			}
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

			isUnattended := false
			if targetPeer.Password != "" && msg.Password != "" {
				if subtle.ConstantTimeCompare([]byte(targetPeer.Password), []byte(msg.Password)) == 1 {
					isUnattended = true
				}
			}

			if isUnattended {
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

				s.addLog("Sessão iniciada (acesso direto com senha): %s (%s) está controlando o Host %s (%s)", peer.Alias, peer.ID, targetPeer.Alias, msg.TargetID)

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
			} else {
				// Attended Access: Sem senha ou senha divergente -> Requer aprovação explícita na tela do Host
				if msg.ID == "" {
					msg.ID = peer.ID
				}
				if msg.Alias != "" {
					peer.Alias = msg.Alias
				}
				peer.IsHost = false

				s.pendingMu.Lock()
				s.pendingRequests[peer.ID] = &PendingRequest{
					ClientID:    peer.ID,
					ClientAlias: peer.Alias,
					ClientIP:    clientIP,
					TargetID:    msg.TargetID,
					ClientConn:  conn,
					RequestedAt: time.Now(),
					OriginalMsg: msg,
				}
				s.pendingMu.Unlock()

				s.addLog("Solicitação de autorização pendente: %s (%s) pediu para acessar o Host %s (%s)", peer.Alias, peer.ID, targetPeer.Alias, msg.TargetID)

				// 1. Notifica o Operador
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionStatus,
					Status:  "pending_approval",
					Message: "Aguardando autorização no computador remoto...",
					ID:      msg.TargetID,
					Alias:   targetPeer.Alias,
				})

				// 2. Notifica o Host
				clientDisplayName := peer.Alias
				if clientDisplayName == "" {
					clientDisplayName = msg.Alias
				}
				if clientDisplayName == "" {
					clientDisplayName = "Operador Remoto"
				}

				targetPeer.mu.Lock()
				_ = targetPeer.Conn.WriteJSON(protocol.SignalingMessage{
					Action:   protocol.ActionIncomingRequest,
					ID:       peer.ID,
					Alias:    clientDisplayName,
					ClientIP: clientIP,
					Message:  fmt.Sprintf("%s deseja controlar o seu computador.", clientDisplayName),
				})
				targetPeer.mu.Unlock()

				// 3. Timeout automático de 45 segundos
				go func(clientID, targetID string) {
					time.Sleep(45 * time.Second)
					s.pendingMu.Lock()
					req, ok := s.pendingRequests[clientID]
					if ok && req.TargetID == targetID {
						delete(s.pendingRequests, clientID)
						s.pendingMu.Unlock()

						_ = req.ClientConn.WriteJSON(protocol.SignalingMessage{
							Action:  protocol.ActionError,
							Message: "Tempo limite esgotado. Ninguém autorizou o acesso no computador remoto.",
						})
					} else {
						s.pendingMu.Unlock()
					}
				}(peer.ID, msg.TargetID)
			}

		case protocol.ActionAccept:
			s.pendingMu.Lock()
			pending, exists := s.pendingRequests[msg.TargetID]
			if exists && pending.TargetID == peer.ID {
				delete(s.pendingRequests, msg.TargetID)
			} else {
				pending = nil
			}
			s.pendingMu.Unlock()

			if pending == nil {
				s.addLog("Aceite ignorado ou expirado para Cliente %s pelo Host %s", msg.TargetID, peer.ID)
				continue
			}

			s.mu.RLock()
			clientPeer, cExists := s.peers[msg.TargetID]
			s.mu.RUnlock()

			if !cExists {
				s.addLog("Aceite falhou: Cliente %s já desconectado", msg.TargetID)
				continue
			}

			clientPeer.IsHost = false
			clientPeer.ActiveTargetID = peer.ID
			peer.ActiveTargetID = clientPeer.ID

			s.addLog("Sessão autorizada na tela: %s (%s) está controlando o Host %s (%s)", clientPeer.Alias, clientPeer.ID, peer.Alias, peer.ID)

			s.addHistory(SessionRecord{
				ID:          fmt.Sprintf("sess_%d", time.Now().UnixNano()),
				StartedAt:   time.Now(),
				HostID:      peer.ID,
				HostAlias:   peer.Alias,
				ClientID:    clientPeer.ID,
				ClientAlias: clientPeer.Alias,
				Status:      "active",
			})

			// 1. Notifica o Cliente com auth_ok e permissões
			clientPeer.mu.Lock()
			_ = clientPeer.Conn.WriteJSON(protocol.SignalingMessage{
				Action:      protocol.ActionStatus,
				Status:      "auth_ok",
				Message:     "Conexão aceita pelo usuário remoto! Conectando à tela remota...",
				ID:          peer.ID,
				Alias:       peer.Alias,
				Permissions: msg.Permissions,
			})
			clientPeer.mu.Unlock()

			// 2. Repassa a mensagem original (ActionConnect) com as permissões ao Host para iniciar a sessão
			peer.mu.Lock()
			origMsg := pending.OriginalMsg
			origMsg.Permissions = msg.Permissions
			_ = peer.Conn.WriteJSON(origMsg)
			peer.mu.Unlock()

		case protocol.ActionReject:
			s.pendingMu.Lock()
			pending, exists := s.pendingRequests[msg.TargetID]
			if exists && pending.TargetID == peer.ID {
				delete(s.pendingRequests, msg.TargetID)
			} else {
				pending = nil
			}
			s.pendingMu.Unlock()

			if pending != nil {
				s.mu.RLock()
				clientPeer, cExists := s.peers[msg.TargetID]
				s.mu.RUnlock()
				if cExists {
					clientPeer.mu.Lock()
					_ = clientPeer.Conn.WriteJSON(protocol.SignalingMessage{
						Action:  protocol.ActionError,
						Message: "Conexão recusada pelo usuário remoto.",
					})
					clientPeer.mu.Unlock()
				}
				s.addLog("Conexão recusada pelo usuário remoto: Host %s rejeitou Cliente %s", peer.ID, msg.TargetID)
			}

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
		IsAdmin        bool   `json:"is_admin"`
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
			IsAdmin:        p.IsAdmin,
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

	pageQuery := r.URL.Query().Get("page")
	limitQuery := r.URL.Query().Get("limit")
	searchQuery := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	page := 1
	if p, err := strconv.Atoi(pageQuery); err == nil && p > 0 {
		page = p
	}
	limit := 50
	if l, err := strconv.Atoi(limitQuery); err == nil && l > 0 {
		limit = l
		if limit > 200 {
			limit = 200
		}
	}

	s.logsMu.RLock()
	defer s.logsMu.RUnlock()

	// Filtra por retenção de 7 dias e termo de busca
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	filtered := make([]LogEntry, 0, len(s.logs))

	// Mais recentes primeiro (ordem decrescente)
	for i := len(s.logs) - 1; i >= 0; i-- {
		e := s.logs[i]
		if e.Timestamp.Before(cutoff) {
			continue
		}
		if searchQuery != "" {
			if !strings.Contains(strings.ToLower(e.Message), searchQuery) &&
				!strings.Contains(strings.ToLower(e.TimeStr), searchQuery) {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	totalCount := len(filtered)
	totalPages := 1
	if totalCount > 0 {
		totalPages = int(math.Ceil(float64(totalCount) / float64(limit)))
	}
	if page > totalPages && totalPages > 0 {
		page = totalPages
	}

	start := (page - 1) * limit
	if start > totalCount {
		start = totalCount
	}
	end := start + limit
	if end > totalCount {
		end = totalCount
	}

	pagedLogs := filtered[start:end]

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"logs":           s.legacyLogs,
		"entries":        pagedLogs,
		"page":           page,
		"limit":          limit,
		"total_count":    totalCount,
		"total_pages":    totalPages,
		"retention_days": 7,
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

func (s *Server) HandleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(faviconICO)
}

func (s *Server) HandleIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(iconPNG)
}

func (s *Server) HandleIcon32(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(icon32PNG)
}

func (s *Server) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		s.HandleFavicon(w, r)
		return
	}
	if r.URL.Path == "/icon.png" {
		s.HandleIcon(w, r)
		return
	}
	if r.URL.Path == "/icon_32.png" {
		s.HandleIcon32(w, r)
		return
	}
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



