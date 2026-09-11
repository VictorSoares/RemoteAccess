package signaling

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Peer struct {
	ID       string
	IsHost   bool
	Password string
	Conn     *websocket.Conn
	mu       sync.Mutex
}

type Server struct {
	mu    sync.RWMutex
	peers map[string]*Peer
}

func NewServer() *Server {
	return &Server{
		peers: make(map[string]*Peer),
	}
}

func generatePeerID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900000))
	return fmt.Sprintf("c_%06d", n.Int64()+100000)
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Signaling] Upgrade error: %v", err)
		return
	}
	defer conn.Close()

	peerID := generatePeerID()
	peer := &Peer{
		ID:     peerID,
		IsHost: false,
		Conn:   conn,
	}

	s.mu.Lock()
	s.peers[peerID] = peer
	s.mu.Unlock()

	log.Printf("[Signaling] Peer conectado: %s (Total online: %d)", peerID, len(s.peers))

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
		total := len(s.peers)
		s.mu.Unlock()
		log.Printf("[Signaling] Peer desconectado: %s (Total online: %d)", peer.ID, total)
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg protocol.SignalingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		// If client sent an ID and it's different from current peer.ID, update registration map
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

			log.Printf("[Signaling] Host registrado com ID fixo: %s", msg.ID)
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
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "ID não encontrado ou máquina remota está offline.",
				})
				continue
			}

			if targetPeer.Password != "" && targetPeer.Password != msg.Password {
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Senha incorreta.",
				})
				continue
			}

			if msg.ID == "" {
				msg.ID = peer.ID
			}

			// Forward connect request to Host
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
				targetPeer.mu.Lock()
				_ = targetPeer.Conn.WriteJSON(msg)
				targetPeer.mu.Unlock()
			}
		}
	}
}
