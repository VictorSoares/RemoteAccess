package signaling

import (
	_ "embed"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
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
	IsHost         bool            `json:"is_host"`
	Password       string          `json:"-"`
	RemoteAddr     string          `json:"remote_addr"`
	ConnectedAt    time.Time       `json:"connected_at"`
	ActiveTargetID string          `json:"active_target_id"`
	Conn           *websocket.Conn `json:"-"`
	mu             sync.Mutex
}

type Server struct {
	mu           sync.RWMutex
	peers        map[string]*Peer
	startTime    time.Time
	totalPackets uint64
	logsMu       sync.RWMutex
	logs         []string
}

func NewServer() *Server {
	s := &Server{
		peers:     make(map[string]*Peer),
		startTime: time.Now(),
		logs:      make([]string, 0, 200),
	}
	s.addLog("Servidor Cloud Relay inicializado com sucesso.")
	return s
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
	total := len(s.peers)
	s.mu.Unlock()

	s.addLog("Novo dispositivo conectado: %s (IP: %s | Total: %d)", peerID, r.RemoteAddr, total)

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
		totalNow := len(s.peers)
		s.mu.Unlock()
		s.addLog("Dispositivo desconectado: %s (Total online: %d)", peer.ID, totalNow)
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
			peer.IsHost = true
			peer.Password = msg.Password
			s.peers[msg.ID] = peer
			s.mu.Unlock()

			s.addLog("Host registrado com ID fixo: %s (Pronto para conexões)", msg.ID)
			_ = conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "registered",
				Message: "Host registrado com sucesso no Relay",
				ID:      msg.ID,
			})

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

			s.addLog("Sessão iniciada: %s está controlando o Host %s", peer.ID, msg.TargetID)

			targetPeer.mu.Lock()
			_ = targetPeer.Conn.WriteJSON(msg)
			targetPeer.mu.Unlock()

			_ = conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "auth_ok",
				Message: "Autenticado com sucesso! Conectando à tela remota...",
				ID:      msg.TargetID,
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

func (s *Server) HandleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type PeerInfo struct {
		ID             string `json:"id"`
		IsHost         bool   `json:"is_host"`
		DurationSec    int    `json:"duration_sec"`
		RemoteAddr     string `json:"remote_addr"`
		ActiveTargetID string `json:"active_target_id"`
		Status         string `json:"status"`
	}

	peerList := make([]PeerInfo, 0, len(s.peers))
	hostCount := 0
	clientCount := 0
	activeSessions := 0

	for _, p := range s.peers {
		status := "Online / Livre"
		if p.ActiveTargetID != "" {
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
			IsHost:         p.IsHost,
			DurationSec:    int(time.Since(p.ConnectedAt).Seconds()),
			RemoteAddr:     p.RemoteAddr,
			ActiveTargetID: p.ActiveTargetID,
			Status:         status,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "online",
		"uptime_sec":      int(time.Since(s.startTime).Seconds()),
		"total_online":    len(s.peers),
		"host_count":      hostCount,
		"client_count":    clientCount,
		"active_sessions": activeSessions / 2, // Host + Client pairs
		"total_packets":   atomic.LoadUint64(&s.totalPackets),
		"peers":           peerList,
	})
}

func (s *Server) HandleLogs(w http.ResponseWriter, r *http.Request) {
	s.logsMu.RLock()
	defer s.logsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": s.logs,
	})
}

func (s *Server) HandleKick(w http.ResponseWriter, r *http.Request) {
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


