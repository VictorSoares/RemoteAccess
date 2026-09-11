package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/config"
	"remoteaccess/internal/protocol"
	"remoteaccess/internal/signaling"
	webrtcmod "remoteaccess/internal/webrtc"
)

//go:embed web/*
var webFS embed.FS

type LocalServer struct {
	mu          sync.RWMutex
	Config      *config.ConfigManager
	Signaling   *signaling.Server
	hostSession *webrtcmod.HostSession
	upgrader    websocket.Upgrader
}

func NewLocalServer(cfg *config.ConfigManager) *LocalServer {
	return &LocalServer{
		Config:    cfg,
		Signaling: signaling.NewServer(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (s *LocalServer) Start(port int) error {
	mux := http.NewServeMux()

	// Sub-filesystem for embedded web files
	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		return fmt.Errorf("failed to load embedded web assets: %w", err)
	}

	// Serve Static Files
	fileServer := http.FileServer(http.FS(webContent))
	mux.Handle("/", fileServer)

	// API Routes
	mux.HandleFunc("/api/host-info", s.handleHostInfo)
	mux.HandleFunc("/api/set-password", s.handleSetPassword)
	mux.HandleFunc("/api/set-signaling", s.handleSetSignaling)
	mux.HandleFunc("/api/autostart", s.handleAutoStart)
	mux.HandleFunc("/api/config", s.handleConfig)

	// WebSocket Signaling Route
	mux.HandleFunc("/ws", s.handleWS)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[RemoteAccess] Painel Web iniciado em http://localhost:%d", port)
	return http.ListenAndServe(addr, mux)
}

func (s *LocalServer) handleHostInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            s.Config.Data.ID,
		"password":      s.Config.Data.Password,
		"quality":       s.Config.Data.Quality,
		"fps":           s.Config.Data.FPS,
		"auto_start":    s.Config.Data.AutoStart,
		"signaling_url": s.Config.Data.SignalingURL,
	})
}

func (s *LocalServer) handleSetSignaling(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SignalingURL string `json:"signaling_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Requisição inválida", http.StatusBadRequest)
		return
	}

	_ = s.Config.SetSignalingURL(req.SignalingURL)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":        "ok",
		"signaling_url": req.SignalingURL,
	})
}

func (s *LocalServer) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Password) < 3 {
		http.Error(w, "Senha inválida (mínimo 3 caracteres)", http.StatusBadRequest)
		return
	}

	if err := s.Config.SetPassword(req.Password); err != nil {
		http.Error(w, "Erro ao salvar senha", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":   "ok",
		"password": req.Password,
	})
}

func (s *LocalServer) handleAutoStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.Config.SetAutoStart(req.Enable); err != nil {
		http.Error(w, fmt.Sprintf("Erro ao configurar inicialização: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"auto_start": req.Enable,
	})
}

func (s *LocalServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Quality int `json:"quality"`
		FPS     int `json:"fps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = s.Config.SetConfig(req.Quality, req.FPS)

	w.WriteHeader(http.StatusOK)
}

func (s *LocalServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}
	defer conn.Close()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg protocol.SignalingMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Action {
		case protocol.ActionRegister:
			s.mu.Lock()
			s.Config.Data.ID = msg.ID
			s.Config.Data.Password = msg.Password
			s.mu.Unlock()

			conn.WriteJSON(protocol.SignalingMessage{
				Action:  protocol.ActionStatus,
				Status:  "registered",
				Message: "Host registrado com sucesso.",
				ID:      msg.ID,
			})

		case protocol.ActionOffer:
			s.mu.RLock()
			expectedPwd := s.Config.Data.Password
			s.mu.RUnlock()

			if msg.Password != "" && msg.Password != expectedPwd {
				conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: "Senha incorreta.",
				})
				continue
			}

			// Initialize WebRTC Host Session
			hostSess, err := webrtcmod.NewHostSession(s.Config.Data.FPS, s.Config.Data.Quality, func(outMsg protocol.SignalingMessage) {
				_ = conn.WriteJSON(outMsg)
			})
			if err != nil {
				conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: fmt.Sprintf("Erro ao iniciar sessão host: %v", err),
				})
				continue
			}

			s.mu.Lock()
			if s.hostSession != nil {
				s.hostSession.Close()
			}
			s.hostSession = hostSess
			s.mu.Unlock()

			err = hostSess.HandleRemoteOffer(msg.TargetID, msg.SDP)
			if err != nil {
				conn.WriteJSON(protocol.SignalingMessage{
					Action:  protocol.ActionError,
					Message: fmt.Sprintf("Erro ao processar oferta WebRTC: %v", err),
				})
				continue
			}

		case protocol.ActionCandidate:
			s.mu.RLock()
			sess := s.hostSession
			s.mu.RUnlock()

			if sess != nil && len(msg.Candidate) > 0 {
				_ = sess.AddICECandidate(msg.Candidate)
			}

		case protocol.ActionClose:
			s.mu.Lock()
			if s.hostSession != nil {
				s.hostSession.Close()
				s.hostSession = nil
			}
			s.mu.Unlock()
		}
	}
}
