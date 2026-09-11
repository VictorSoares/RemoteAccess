package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/config"
	"remoteaccess/internal/logger"
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
	cloudWS     *websocket.Conn
	cloudStatus string
	stopCloud   chan struct{}
}

func normalizeWSURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "https://") {
		rawURL = "wss://" + strings.TrimPrefix(rawURL, "https://")
	} else if strings.HasPrefix(rawURL, "http://") {
		rawURL = "ws://" + strings.TrimPrefix(rawURL, "http://")
	} else if !strings.HasPrefix(rawURL, "ws://") && !strings.HasPrefix(rawURL, "wss://") {
		rawURL = "wss://" + rawURL
	}
	rawURL = strings.TrimRight(rawURL, "/")
	if !strings.HasSuffix(rawURL, "/ws") {
		rawURL = rawURL + "/ws"
	}
	return rawURL
}

func NewLocalServer(cfg *config.ConfigManager) *LocalServer {
	return &LocalServer{
		Config:      cfg,
		Signaling:   signaling.NewServer(),
		cloudStatus: "local",
		stopCloud:   make(chan struct{}),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (s *LocalServer) Start(port int) error {
	go s.cloudSignalingLoop()

	mux := http.NewServeMux()

	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		return fmt.Errorf("failed to load embedded web assets: %w", err)
	}

	mux.Handle("/", http.FileServer(http.FS(webContent)))

	mux.HandleFunc("/api/host-info", s.handleHostInfo)
	mux.HandleFunc("/api/set-password", s.handleSetPassword)
	mux.HandleFunc("/api/set-signaling", s.handleSetSignaling)
	mux.HandleFunc("/api/autostart", s.handleAutoStart)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/logs", s.handleLogs)

	mux.HandleFunc("/ws", s.handleWS)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[RemoteAccess] Painel Web iniciado em http://localhost:%d", port)
	return http.ListenAndServe(addr, mux)
}

func (s *LocalServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	logs := logger.GetLogs()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
	})
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
		"cloud_status":  s.cloudStatus,
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

	normURL := normalizeWSURL(req.SignalingURL)
	_ = s.Config.SetSignalingURL(normURL)

	s.mu.Lock()
	if s.cloudWS != nil {
		_ = s.cloudWS.Close()
		s.cloudWS = nil
	}
	s.mu.Unlock()

	log.Printf("[Config] Novo servidor de sinalização configurado: %s", normURL)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":        "ok",
		"signaling_url": normURL,
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

	s.mu.RLock()
	cws := s.cloudWS
	hostID := s.Config.Data.ID
	s.mu.RUnlock()

	if cws != nil {
		_ = cws.WriteJSON(protocol.SignalingMessage{
			Action:   protocol.ActionRegister,
			ID:       hostID,
			Password: req.Password,
		})
	}

	log.Printf("[Config] Senha fixa atualizada com sucesso")

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

	log.Printf("[Config] Auto-start com Windows alterado para: %v", req.Enable)

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

func (s *LocalServer) cloudSignalingLoop() {
	for {
		s.mu.RLock()
		targetURL := s.Config.Data.SignalingURL
		s.mu.RUnlock()

		if targetURL == "" {
			s.mu.Lock()
			s.cloudStatus = "local"
			s.mu.Unlock()
			time.Sleep(2 * time.Second)
			continue
		}

		targetURL = normalizeWSURL(targetURL)

		s.mu.Lock()
		s.cloudStatus = "connecting"
		s.mu.Unlock()

		log.Printf("[Nuvem] Conectando a %s ...", targetURL)
		conn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
		if err != nil {
			log.Printf("[Nuvem] Falha ao conectar: %v. Tentando novamente em 4s...", err)
			s.mu.Lock()
			s.cloudStatus = "error"
			s.mu.Unlock()
			time.Sleep(4 * time.Second)
			continue
		}

		s.mu.Lock()
		s.cloudWS = conn
		s.cloudStatus = "connected"
		hostID := s.Config.Data.ID
		hostPwd := s.Config.Data.Password
		s.mu.Unlock()

		log.Printf("[Nuvem] Conectado com sucesso! Registrado Host ID: %s", hostID)

		err = conn.WriteJSON(protocol.SignalingMessage{
			Action:   protocol.ActionRegister,
			ID:       hostID,
			Password: hostPwd,
		})
		if err != nil {
			conn.Close()
			continue
		}

		for {
			var msg protocol.SignalingMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				log.Printf("[Nuvem] Conexão perdida: %v", err)
				break
			}

			s.handleSignalingMessage(conn, msg)
		}

		conn.Close()
		s.mu.Lock()
		s.cloudWS = nil
		s.cloudStatus = "disconnected"
		s.mu.Unlock()

		time.Sleep(3 * time.Second)
	}
}

func (s *LocalServer) handleSignalingMessage(conn *websocket.Conn, msg protocol.SignalingMessage) {
	switch msg.Action {
	case protocol.ActionConnect, protocol.ActionOffer:
		senderID := msg.ID
		if senderID == "" {
			senderID = msg.TargetID
		}
		log.Printf("[Conexão] Cliente remoto conectado: %s (iniciando captura de tela)", senderID)

		s.mu.Lock()
		sess := s.hostSession
		if sess == nil {
			var err error
			sess, err = webrtcmod.NewHostSession(s.Config.Data.FPS, s.Config.Data.Quality, func(outMsg protocol.SignalingMessage) {
				outMsg.ID = s.Config.Data.ID
				if outMsg.TargetID == "" {
					outMsg.TargetID = senderID
				}
				_ = conn.WriteJSON(outMsg)
			})
			if err != nil {
				s.mu.Unlock()
				log.Printf("[Conexão] Erro ao criar sessão host: %v", err)
				return
			}

			sess.OnRelayFrame = func(jpegBase64 string) {
				_ = conn.WriteJSON(protocol.SignalingMessage{
					Action:   protocol.ActionData,
					ID:       s.Config.Data.ID,
					TargetID: senderID,
					Payload:  jpegBase64,
				})
			}

			s.hostSession = sess
			sess.StartStreaming()
		}
		s.mu.Unlock()

		if msg.SDP != "" {
			_ = sess.HandleRemoteOffer(senderID, msg.SDP)
		}

	case protocol.ActionData:
		s.mu.RLock()
		sess := s.hostSession
		s.mu.RUnlock()

		if sess != nil && msg.Payload != "" {
			sess.HandleControlData([]byte(msg.Payload))
		}

	case protocol.ActionCandidate:
		s.mu.RLock()
		sess := s.hostSession
		s.mu.RUnlock()

		if sess != nil && len(msg.Candidate) > 0 {
			_ = sess.AddICECandidate(msg.Candidate)
		}

	case protocol.ActionClose:
		log.Printf("[Conexão] Sessão remota encerrada")
		s.mu.Lock()
		if s.hostSession != nil {
			s.hostSession.Close()
			s.hostSession = nil
		}
		s.mu.Unlock()
	}
}

func (s *LocalServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for {
		var msg protocol.SignalingMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		s.mu.RLock()
		cloudConn := s.cloudWS
		s.mu.RUnlock()

		if cloudConn != nil {
			_ = cloudConn.WriteJSON(msg)
		} else {
			s.handleSignalingMessage(conn, msg)
		}
	}
}
