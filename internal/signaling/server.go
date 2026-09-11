package signaling

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for signaling
	},
}

type Session struct {
	ID       string
	Password string
	HostConn *websocket.Conn
	mu       sync.Mutex
}

type Server struct {
	mu       sync.RWMutex
	sessions map[string]*Session // ID -> Session
}

func NewServer() *Server {
	return &Server{
		sessions: make(map[string]*Session),
	}
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Signaling] Upgrade error: %v", err)
		return
	}
	defer conn.Close()

	var currentSessionID string
	var isHost bool

	defer func() {
		s.mu.Lock()
		if isHost && currentSessionID != "" {
			delete(s.sessions, currentSessionID)
			log.Printf("[Signaling] Host session %s unregistered", currentSessionID)
		}
		s.mu.Unlock()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg protocol.SignalingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[Signaling] JSON decode error: %v", err)
			continue
		}

		switch msg.Action {
		case protocol.ActionRegister:
			s.mu.Lock()
			currentSessionID = msg.ID
			isHost = true
			s.sessions[msg.ID] = &Session{
				ID:       msg.ID,
				Password: msg.Password,
				HostConn: conn,
			}
			s.mu.Unlock()

			log.Printf("[Signaling] Host registered ID: %s", msg.ID)
			conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "registered",
				Message: "Registered successfully as host",
				ID:      msg.ID,
			})

		case protocol.ActionConnect:
			// Client requests to connect to a Host ID
			s.mu.RLock()
			sess, exists := s.sessions[msg.TargetID]
			s.mu.RUnlock()

			if !exists {
				conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "ID não encontrado ou máquina remota está offline.",
				})
				continue
			}

			if sess.Password != "" && sess.Password != msg.Password {
				conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Senha incorreta.",
				})
				continue
			}

			// Forward connect request to Host
			sess.mu.Lock()
			err = sess.HostConn.WriteJSON(protocol.SignalingMessage{
				Action:   protocol.ActionConnect,
				TargetID: msg.ID, // Client's ephemeral ID
			})
			sess.mu.Unlock()

			if err != nil {
				conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Falha ao comunicar com a máquina remota.",
				})
				continue
			}

			conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "connected",
				Message: "Conectado ao host, iniciando negociação WebRTC...",
			})

		case protocol.ActionOffer, protocol.ActionAnswer, protocol.ActionCandidate:
			// Relay WebRTC signaling packets to the target peer
			s.mu.RLock()
			sess, exists := s.sessions[msg.TargetID]
			s.mu.RUnlock()

			if exists {
				sess.mu.Lock()
				_ = sess.HostConn.WriteJSON(msg)
				sess.mu.Unlock()
			}

		case protocol.ActionClose:
			// Peer wants to disconnect
			s.mu.RLock()
			sess, exists := s.sessions[msg.TargetID]
			s.mu.RUnlock()
			if exists {
				sess.mu.Lock()
				_ = sess.HostConn.WriteJSON(msg)
				sess.mu.Unlock()
			}
		}
	}
}
