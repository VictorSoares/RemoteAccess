package server

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"remoteaccess/internal/capture"
	"remoteaccess/internal/config"
	"remoteaccess/internal/input"
	"remoteaccess/internal/logger"
	"remoteaccess/internal/protocol"
	"remoteaccess/internal/signaling"
	"remoteaccess/internal/tray"
	webrtcmod "remoteaccess/internal/webrtc"
	"remoteaccess/internal/wol"
)

//go:embed web/*
var webFS embed.FS

type ChatMessage struct {
	Sender string `json:"sender"`
	Text   string `json:"text"`
	Time   string `json:"time"`
}

type PendingIncomingRequest struct {
	ClientID    string    `json:"client_id"`
	ClientAlias string    `json:"client_alias"`
	ClientIP    string    `json:"client_ip"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type LocalServer struct {
	mu             sync.RWMutex
	Config         *config.ConfigManager
	Signaling      *signaling.Server
	hostSession    *webrtcmod.HostSession
	upgrader       websocket.Upgrader
	cloudWS        *websocket.Conn
	cloudWsMu      sync.Mutex
	cloudStatus    string
	stopCloud      chan struct{}
	chatMsgs       []ChatMessage
	chatMu         sync.Mutex
	sessionToken   string
	pendingMu      sync.Mutex
	pendingRequest *PendingIncomingRequest
}

func (s *LocalServer) writeCloudJSON(msg protocol.SignalingMessage) error {
	s.cloudWsMu.Lock()
	defer s.cloudWsMu.Unlock()
	if s.cloudWS == nil {
		return fmt.Errorf("cloud ws is nil")
	}
	_ = s.cloudWS.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.cloudWS.WriteJSON(msg)
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

func (s *LocalServer) validateLocalRequest(r *http.Request) bool {
	// 1. Validate Origin / Referer if present
	if origin := r.Header.Get("Origin"); origin != "" {
		if u, err := url.Parse(origin); err == nil {
			h := u.Hostname()
			if h != "127.0.0.1" && h != "localhost" {
				return false
			}
		} else {
			return false
		}
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil {
			h := u.Hostname()
			if h != "127.0.0.1" && h != "localhost" {
				return false
			}
		} else {
			return false
		}
	}

	// 2. Validate token from Header X-Local-Token or Cookie ra_local_token
	token := r.Header.Get("X-Local-Token")
	if token == "" {
		if c, err := r.Cookie("ra_local_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" || s.sessionToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.sessionToken)) == 1
}

func NewLocalServer(cfg *config.ConfigManager) *LocalServer {
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		tokenBytes = []byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()))
	}
	token := hex.EncodeToString(tokenBytes)

	return &LocalServer{
		Config:       cfg,
		Signaling:    signaling.NewServer(),
		cloudStatus:  "local",
		stopCloud:    make(chan struct{}),
		sessionToken: token,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // desktop apps / edge app mode without Origin header
				}
				u, err := url.Parse(origin)
				if err != nil {
					return false
				}
				h := u.Hostname()
				return h == "127.0.0.1" || h == "localhost"
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

	fileHandler := http.FileServer(http.FS(webContent))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "ra_local_token",
			Value:    s.sessionToken,
			Path:     "/",
			HttpOnly: false, // Accessible to app.js
			SameSite: http.SameSiteStrictMode,
		})
		fileHandler.ServeHTTP(w, r)
	})

	mux.HandleFunc("/api/csrf-token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token": s.sessionToken,
		})
	})

	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		s.Signaling.HandleDashboard(w, r)
	})
	mux.HandleFunc("/api/stats", s.Signaling.HandleStats)
	mux.HandleFunc("/api/history", s.Signaling.HandleHistory)
	mux.HandleFunc("/api/block", s.Signaling.HandleBlockPeer)
	mux.HandleFunc("/api/kick", s.Signaling.HandleKick)
	mux.HandleFunc("/api/wol", s.Signaling.HandleWoL)
	mux.HandleFunc("/api/auth", s.Signaling.HandleAuth)

	mux.HandleFunc("/api/host-info", s.handleHostInfo)
	mux.HandleFunc("/api/set-alias", s.handleSetAlias)
	mux.HandleFunc("/api/set-password", s.handleSetPassword)
	mux.HandleFunc("/api/set-signaling", s.handleSetSignaling)
	mux.HandleFunc("/api/autostart", s.handleAutoStart)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/set-log-file", s.handleSetLogFile)
	mux.HandleFunc("/api/session-status", s.handleSessionStatus)
	mux.HandleFunc("/api/accept-connection", s.handleAcceptConnection)
	mux.HandleFunc("/api/reject-connection", s.handleRejectConnection)
	mux.HandleFunc("/api/host-toggle-control", s.handleHostToggleControl)
	mux.HandleFunc("/api/kick-session", s.handleKickSession)
	mux.HandleFunc("/api/chat-messages", s.handleGetChatMessages)
	mux.HandleFunc("/api/send-chat", s.handleSendChatMessage)
	mux.HandleFunc("/api/send-wol", s.handleSendWoL)
	mux.HandleFunc("/api/system-info", s.handleSystemInfo)
	mux.HandleFunc("/api/elevate", s.handleElevate)
	mux.HandleFunc("/api/install", s.handleInstall)
	mux.HandleFunc("/api/app-close", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		log.Println("[Janela] Janela fechada pelo usuário. O serviço continua ativo em segundo plano na bandeja do sistema.")
	})
	mux.HandleFunc("/api/app-terminate", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		log.Println("[Instâncias] Pedido de encerramento para takeover recebido. Finalizando instância com segurança...")
		go func() {
			time.Sleep(100 * time.Millisecond)
			s.Shutdown()
			os.Exit(0)
		}()
	})

	mux.HandleFunc("/ws", s.handleWS)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Printf("[RemoteAccess] Painel Web iniciado em http://127.0.0.1:%d (Loopback estrito)", port)
	return http.ListenAndServe(addr, mux)
}

func (s *LocalServer) addChatMessage(sender, text string) {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	s.chatMsgs = append(s.chatMsgs, ChatMessage{
		Sender: sender,
		Text:   text,
		Time:   time.Now().Format("15:04:05"),
	})
	if len(s.chatMsgs) > 100 {
		s.chatMsgs = s.chatMsgs[len(s.chatMsgs)-100:]
	}
}

func (s *LocalServer) handleGetChatMessages(w http.ResponseWriter, r *http.Request) {
	s.chatMu.Lock()
	msgs := make([]ChatMessage, len(s.chatMsgs))
	copy(msgs, s.chatMsgs)
	s.chatMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"messages": msgs,
	})
}

func (s *LocalServer) handleSendChatMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "Mensagem vazia", http.StatusBadRequest)
		return
	}

	cleanText := strings.TrimSpace(req.Text)
	s.addChatMessage("Você (Host)", cleanText)

	s.mu.RLock()
	sess := s.hostSession
	s.mu.RUnlock()

	if sess != nil {
		sess.SendControl(protocol.ControlMessage{
			Type: protocol.TypeChat,
			Text: cleanText,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (s *LocalServer) handleSetLogFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = s.Config.SetSaveLogFile(req.Enable)
	logger.SetFileLogging(req.Enable)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "ok",
		"save_log_file": req.Enable,
	})
}

func (s *LocalServer) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.hostSession
	if sess != nil && !sess.IsActive() {
		sess = nil
		s.hostSession = nil
	}
	s.mu.Unlock()

	s.pendingMu.Lock()
	pr := s.pendingRequest
	if pr != nil && time.Now().After(pr.ExpiresAt) {
		s.pendingRequest = nil
		pr = nil
	}
	s.pendingMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if sess != nil && sess.IsActive() {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"active":                 true,
			"client_id":              sess.ClientID,
			"client_alias":           sess.ClientAlias,
			"duration":               int(time.Since(sess.ConnectedAt).Seconds()),
			"remote_control_enabled": sess.IsRemoteInputAllowed(),
		})
	} else if pr != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"active":              false,
			"has_pending_request": true,
			"pending_request": map[string]interface{}{
				"client_id":    pr.ClientID,
				"client_alias": pr.ClientAlias,
				"client_ip":    pr.ClientIP,
				"seconds_left": int(time.Until(pr.ExpiresAt).Seconds()),
			},
		})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"active": false,
		})
	}
}

func (s *LocalServer) handleAcceptConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}

	var req struct {
		AllowInput bool `json:"allow_input"`
		AllowFiles bool `json:"allow_files"`
		AllowAudio bool `json:"allow_audio"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	s.pendingMu.Lock()
	pr := s.pendingRequest
	s.pendingRequest = nil
	s.pendingMu.Unlock()

	if pr == nil || time.Now().After(pr.ExpiresAt) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Nenhum pedido de conexão pendente ou expirou.",
		})
		return
	}

	perms := &protocol.SessionPermissions{
		AllowInput: req.AllowInput,
		AllowFiles: req.AllowFiles,
		AllowAudio: req.AllowAudio,
	}

	err := s.writeCloudJSON(protocol.SignalingMessage{
		Action:      protocol.ActionAccept,
		ID:          s.Config.Data.ID,
		TargetID:    pr.ClientID,
		Permissions: perms,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": "Falha ao enviar resposta ao servidor de sinalização.",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
	})
}

func (s *LocalServer) handleRejectConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}

	s.pendingMu.Lock()
	pr := s.pendingRequest
	s.pendingRequest = nil
	s.pendingMu.Unlock()

	if pr != nil {
		_ = s.writeCloudJSON(protocol.SignalingMessage{
			Action:   protocol.ActionReject,
			ID:       s.Config.Data.ID,
			TargetID: pr.ClientID,
			Message:  "Conexão recusada pelo usuário remoto.",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
	})
}

func (s *LocalServer) handleHostToggleControl(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.hostSession
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if sess != nil && sess.IsActive() {
		cur := sess.IsRemoteInputAllowed()
		newVal := !cur
		sess.SetAllowRemoteInput(newVal)
		log.Printf("[Host] Controle remoto de mouse/teclado alterado pelo cliente local para: %v", newVal)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":                 "ok",
			"remote_control_enabled": newVal,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "error",
		"message": "Nenhuma sessão ativa no momento",
	})
}

func (s *LocalServer) handleKickSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.hostSession
	s.hostSession = nil
	clientID := ""
	if sess != nil {
		clientID = sess.ClientID
	}
	hostID := s.Config.Data.ID
	s.mu.Unlock()

	if sess != nil {
		if clientID != "" {
			_ = s.writeCloudJSON(protocol.SignalingMessage{
				Action:   protocol.ActionClose,
				ID:       hostID,
				TargetID: clientID,
			})
		}
		sess.Close()
	}

	s.chatMu.Lock()
	s.chatMsgs = nil
	s.chatMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (s *LocalServer) handleSendWoL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.MAC) == "" {
		http.Error(w, "Endereço MAC inválido", http.StatusBadRequest)
		return
	}
	cleanMAC := strings.TrimSpace(req.MAC)
	err := wol.SendMagicPacket(cleanMAC)
	if err != nil {
		http.Error(w, fmt.Sprintf("Erro ao enviar pacote WoL: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[WoL] Pacote Magic Packet Wake-on-LAN enviado para: %s", cleanMAC)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": fmt.Sprintf("Pacote Wake-on-LAN transmitido para %s", cleanMAC),
	})
}

func (s *LocalServer) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	numDisplays := capture.GetNumDisplays()
	primaryMAC := wol.GetPrimaryMACAddress()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"hostname":     hostname,
		"os":           runtime.GOOS + " " + runtime.GOARCH,
		"monitors":     numDisplays,
		"mac":          primaryMAC,
		"is_admin":     config.IsAdmin(),
		"is_installed": config.IsInstalled(),
	})
}

func (s *LocalServer) handleElevate(w http.ResponseWriter, r *http.Request) {
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
	if config.IsAdmin() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "ok",
			"is_admin": true,
			"message":  "O aplicativo já está em execução com privilégios de Administrador.",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Solicitando elevação de privilégios UAC...",
	})

	go func() {
		time.Sleep(200 * time.Millisecond)
		log.Println("[Elevação] Solicitando autorização do usuário via UAC...")
		err := config.ElevateSelf("")
		if err != nil {
			log.Printf("[Elevação] Elevação cancelada ou falhou: %v. Mantendo serviço ativo.", err)
			return
		}
		log.Println("[Elevação] Processo elevado iniciado com sucesso! Transferindo controle...")
		time.Sleep(300 * time.Millisecond)
		s.Shutdown()
		os.Exit(0)
	}()
}

func (s *LocalServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
	if !config.IsAdmin() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"message": "Solicitando privilégios de Administrador para instalar...",
		})
		go func() {
			time.Sleep(300 * time.Millisecond)
			s.Shutdown()
			_ = config.ElevateSelf("-install")
			os.Exit(0)
		}()
		return
	}

	err := s.Config.InstallAsAdmin()
	if err != nil {
		http.Error(w, fmt.Sprintf("Erro ao instalar: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "RemoteAccess instalado com sucesso em Program Files com privilégios elevados!",
	})
}

func (s *LocalServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("page") != "" || r.Header.Get("X-Admin-Key") != "" {
		s.Signaling.HandleLogs(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	logs := logger.GetLogs()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": logs,
	})
}

func (s *LocalServer) handleHostInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pwd := s.Config.Data.Password
	if !s.validateLocalRequest(r) {
		pwd = "••••••••"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            s.Config.Data.ID,
		"alias":         s.Config.Data.Alias,
		"password":      pwd,
		"has_password":  len(s.Config.Data.Password) > 0,
		"quality":       s.Config.Data.Quality,
		"fps":           s.Config.Data.FPS,
		"auto_start":    s.Config.Data.AutoStart,
		"signaling_url": s.Config.Data.SignalingURL,
		"save_log_file": s.Config.Data.SaveLogFile,
		"cloud_status":  s.cloudStatus,
		"mac":           wol.GetPrimaryMACAddress(),
		"is_admin":      config.IsAdmin(),
		"is_installed":  config.IsInstalled(),
	})
}

func (s *LocalServer) handleSetAlias(w http.ResponseWriter, r *http.Request) {
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
	var req struct {
		Alias string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Alias) == "" {
		http.Error(w, "Nome/Apelido inválido", http.StatusBadRequest)
		return
	}

	cleanAlias := strings.TrimSpace(req.Alias)
	if err := s.Config.SetAlias(cleanAlias); err != nil {
		http.Error(w, "Erro ao salvar apelido", http.StatusInternalServerError)
		return
	}

	s.mu.RLock()
	cws := s.cloudWS
	hostID := s.Config.Data.ID
	hostPwd := s.Config.Data.Password
	s.mu.RUnlock()

	if cws != nil {
		_ = cws.WriteJSON(protocol.SignalingMessage{
			Action:   protocol.ActionRegister,
			ID:       hostID,
			Alias:    cleanAlias,
			Password: hostPwd,
			Monitors: capture.GetNumDisplays(),
			MAC:      wol.GetPrimaryMACAddress(),
			IsAdmin:  config.IsAdmin(),
		})
	}

	log.Printf("[Config] Apelido do computador atualizado para: %s", cleanAlias)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"alias":  cleanAlias,
	})
}

func (s *LocalServer) handleSetSignaling(w http.ResponseWriter, r *http.Request) {
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
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
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(strings.TrimSpace(req.Password)) < 3 {
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
	hostAlias := s.Config.Data.Alias
	s.mu.RUnlock()

	if cws != nil {
		_ = cws.WriteJSON(protocol.SignalingMessage{
			Action:   protocol.ActionRegister,
			ID:       hostID,
			Alias:    hostAlias,
			Password: req.Password,
			Monitors: capture.GetNumDisplays(),
			MAC:      wol.GetPrimaryMACAddress(),
			IsAdmin:  config.IsAdmin(),
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
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
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
	if !s.validateLocalRequest(r) {
		http.Error(w, "Acesso não autorizado", http.StatusForbidden)
		return
	}
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

		s.cloudWsMu.Lock()
		s.cloudWS = conn
		s.cloudWsMu.Unlock()
		s.mu.Lock()
		s.cloudStatus = "connected"
		hostID := s.Config.Data.ID
		hostAlias := s.Config.Data.Alias
		hostPwd := s.Config.Data.Password
		numDisplays := capture.GetNumDisplays()
		primaryMAC := wol.GetPrimaryMACAddress()
		s.mu.Unlock()

		isAdmin := config.IsAdmin()
		log.Printf("[Nuvem] Conectado com sucesso! Registrado Host ID: %s (Alias: %s, Monitores: %d, MAC: %s, Admin: %v)", hostID, hostAlias, numDisplays, primaryMAC, isAdmin)

		err = s.writeCloudJSON(protocol.SignalingMessage{
			Action:   protocol.ActionRegister,
			ID:       hostID,
			Alias:    hostAlias,
			Password: hostPwd,
			Monitors: numDisplays,
			MAC:      primaryMAC,
			IsAdmin:  isAdmin,
		})
		if err != nil {
			conn.Close()
			continue
		}

		pingTicker := time.NewTicker(15 * time.Second)
		pingDone := make(chan struct{})
		go func() {
			defer pingTicker.Stop()
			for {
				select {
				case <-pingDone:
					return
				case <-pingTicker.C:
					s.cloudWsMu.Lock()
					if s.cloudWS != nil {
						_ = s.cloudWS.SetWriteDeadline(time.Now().Add(5 * time.Second))
						_ = s.cloudWS.WriteMessage(websocket.PingMessage, []byte{})
					}
					s.cloudWsMu.Unlock()
				}
			}
		}()

		for {
			var msg protocol.SignalingMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				log.Printf("[Nuvem] Conexão perdida: %v", err)
				break
			}

			s.handleSignalingMessage(conn, msg)
		}

		close(pingDone)
		conn.Close()
		s.cloudWsMu.Lock()
		s.cloudWS = nil
		s.cloudWsMu.Unlock()
		s.mu.Lock()
		s.cloudStatus = "disconnected"
		s.mu.Unlock()

		time.Sleep(3 * time.Second)
	}
}

func (s *LocalServer) handleSignalingMessage(conn *websocket.Conn, msg protocol.SignalingMessage) {
	switch msg.Action {
	case protocol.ActionIncomingRequest:
		senderID := msg.ID
		if senderID == "" {
			senderID = msg.TargetID
		}
		senderAlias := msg.Alias
		if senderAlias == "" {
			senderAlias = "Operador Remoto"
		}
		log.Printf("[Host] Solicitação de conexão recebida de %s (%s, IP: %s)", senderAlias, senderID, msg.ClientIP)

		s.pendingMu.Lock()
		s.pendingRequest = &PendingIncomingRequest{
			ClientID:    senderID,
			ClientAlias: senderAlias,
			ClientIP:    msg.ClientIP,
			ExpiresAt:   time.Now().Add(45 * time.Second),
		}
		s.pendingMu.Unlock()

		go func() {
			tray.ShowBalloonNotification(
				"RemoteAccess — Pedido de Conexão",
				fmt.Sprintf("%s (%s) deseja conectar ao seu computador.", senderAlias, senderID),
			)
			input.BringAppToFront()
			input.FlashAppWindow()
			input.PlayNotificationSound()
		}()

	case protocol.ActionConnect:
		senderID := msg.ID
		if senderID == "" {
			senderID = msg.TargetID
		}
		senderAlias := msg.Alias
		if senderAlias == "" {
			senderAlias = "Controlador"
		}

		// Validação local adicional de segurança (Defense-in-Depth):
		// O Host só aceita ActionConnect se a senha local coincidir (acesso desassistido) OU
		// se houver concessão explícita de permissões concedida pelo usuário na tela (acesso assistido)
		if s.Config.Data.Password != "" {
			hasValidPwd := msg.Password != "" && subtle.ConstantTimeCompare([]byte(s.Config.Data.Password), []byte(msg.Password)) == 1
			hasExplicitApproval := msg.Permissions != nil
			if !hasValidPwd && !hasExplicitApproval {
				log.Printf("[Segurança] ActionConnect rejeitado para cliente %s: senha local não coincide e não houve aprovação na tela.", senderID)
				return
			}
		}

		s.pendingMu.Lock()
		s.pendingRequest = nil
		s.pendingMu.Unlock()

		log.Printf("[Conexão] Cliente remoto autorizado: %s (%s) (iniciando sessão host)", senderAlias, senderID)

		s.mu.Lock()
		sess := s.hostSession
		if sess == nil {
			var err error
			sess, err = webrtcmod.NewHostSession(senderID, senderAlias, s.Config.Data.FPS, s.Config.Data.Quality, func(outMsg protocol.SignalingMessage) {
				outMsg.ID = s.Config.Data.ID
				if outMsg.TargetID == "" {
					outMsg.TargetID = senderID
				}
				_ = s.writeCloudJSON(outMsg)
			})
			if err != nil {
				s.mu.Unlock()
				log.Printf("[Conexão] Erro ao criar sessão host: %v", err)
				return
			}

			if msg.Permissions != nil {
				sess.SetAllowRemoteInput(msg.Permissions.AllowInput)
			}

			sess.OnChat = func(sender string, text string) {
				senderName := "Controlador"
				if sess.ClientAlias != "" {
					senderName = sess.ClientAlias
				}
				s.addChatMessage(senderName, text)
				go func() {
					tray.ShowBalloonNotification(fmt.Sprintf("RemoteAccess — Chat (%s)", senderName), text)
					input.BringAppToFront()
					input.FlashAppWindow()
					input.PlayNotificationSound()
				}()
			}

			sess.OnClose = func() {
				s.mu.Lock()
				if s.hostSession == sess {
					s.hostSession = nil
				}
				s.mu.Unlock()
				s.chatMu.Lock()
				s.chatMsgs = nil
				s.chatMu.Unlock()
				log.Println("[Sessão] Sessão remota finalizada e recursos liberados.")
			}

			s.hostSession = sess
			sess.StartStreaming()

			// Send display count and system info to remote viewer
			numDisplays := capture.GetNumDisplays()
			initInfo, _ := json.Marshal(protocol.ControlMessage{
				Type:    protocol.TypeInitInfo,
				Monitor: numDisplays,
			})
			_ = s.writeCloudJSON(protocol.SignalingMessage{
				Action:   protocol.ActionData,
				ID:       s.Config.Data.ID,
				TargetID: senderID,
				Payload:  string(initInfo),
			})
		} else if msg.Alias != "" {
			sess.ClientAlias = msg.Alias
		}
		s.mu.Unlock()

		if msg.SDP != "" {
			_ = sess.HandleRemoteOffer(senderID, msg.SDP)
		}

	case protocol.ActionOffer:
		senderID := msg.ID
		if senderID == "" {
			senderID = msg.TargetID
		}

		s.mu.RLock()
		sess := s.hostSession
		s.mu.RUnlock()

		if sess == nil || (sess.ClientID != senderID) {
			log.Printf("[Segurança] ActionOffer descartado: nenhuma sessão autorizada ativa para o cliente %s", senderID)
			return
		}

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
		s.pendingMu.Lock()
		if s.pendingRequest != nil && s.pendingRequest.ClientID == msg.ID {
			s.pendingRequest = nil
		}
		s.pendingMu.Unlock()
		log.Printf("[Conexão] Sessão remota encerrada via sinalização")
		s.mu.Lock()
		sess := s.hostSession
		s.hostSession = nil
		s.mu.Unlock()

		if sess != nil {
			sess.Close()
		}

		s.chatMu.Lock()
		s.chatMsgs = nil
		s.chatMu.Unlock()
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

func (s *LocalServer) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cloudWsMu.Lock()
	if s.cloudWS != nil {
		log.Printf("[RemoteAccess] Notificando servidor Cloud que o Host %s foi encerrado...", s.Config.Data.ID)
		_ = s.cloudWS.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
		_ = s.cloudWS.WriteJSON(protocol.SignalingMessage{
			Action: protocol.ActionUnregister,
			ID:     s.Config.Data.ID,
		})
		_ = s.cloudWS.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Host shutdown"))
		_ = s.cloudWS.Close()
		s.cloudWS = nil
	}
	s.cloudWsMu.Unlock()

	if s.hostSession != nil {
		s.hostSession.Close()
		s.hostSession = nil
	}
}


