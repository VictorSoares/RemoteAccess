package webrtc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	pion "github.com/pion/webrtc/v3"
	"remoteaccess/internal/audio"
	"remoteaccess/internal/capture"
	"remoteaccess/internal/config"
	fsmod "remoteaccess/internal/fs"
	"remoteaccess/internal/input"
	"remoteaccess/internal/protocol"
	"remoteaccess/internal/tray"
)

var defaultICEServers = []pion.ICEServer{
	{
		URLs: []string{
			"stun:stun.l.google.com:19302",
			"stun:stun1.l.google.com:19302",
			"stun:stun2.l.google.com:19302",
			"stun:stun3.l.google.com:19302",
			"stun:stun4.l.google.com:19302",
			"stun:stun.cloudflare.com:3478",
			"stun:global.stun.twilio.com:3478",
			"stun:stun.nextcloud.com:443",
			"stun:stun.matrix.org:3478",
			"stun:stun.voip.blackberry.com:3478",
		},
	},
}

type HostSession struct {
	mu           sync.Mutex
	peerConn     *pion.PeerConnection
	inputChannel *pion.DataChannel
	videoChannel *pion.DataChannel
	audioChannel *pion.DataChannel
	capturer     *capture.DXGICapturer
	ctx          context.Context
	cancel       context.CancelFunc
	running      int32
	FPS          int
	Quality      int
	frameCounter uint32
	ClientID     string
	ClientAlias  string
	ConnectedAt  time.Time
	activeFiles  map[string]*os.File
	lastActivity int64
	lastInputTime int64
	lastReceivedClipText string
	lastSentClipText     string
	autoLockOnClose      bool
	allowRemoteInput     int32 // 1 = allowed, 0 = paused by host
	fpsUpdate    chan int
	disconnectTimer *time.Timer
	OnStatus     func(status string, msg string)
	SendSignal   func(msg protocol.SignalingMessage)
	OnChat       func(sender string, text string)
	OnClose      func()
}

func NewHostSession(clientID string, clientAlias string, fps int, quality int, sendSignal func(protocol.SignalingMessage)) (*HostSession, error) {
	capturer, err := capture.NewFastCapturer(0)
	if err != nil {
		return nil, fmt.Errorf("failed to init screen capture: %w", err)
	}
	capturer.SetQuality(quality)

	if fps <= 0 {
		fps = 30
	}

	ctx, cancel := context.WithCancel(context.Background())

	input.SetActiveMonitorBounds(capturer.GetBounds())

	sess := &HostSession{
		ClientID:      clientID,
		ClientAlias:   clientAlias,
		ConnectedAt:   time.Now(),
		capturer:      capturer,
		FPS:           fps,
		Quality:       quality,
		ctx:           ctx,
		cancel:        cancel,
		lastActivity:  time.Now().Unix(),
		lastInputTime:    time.Now().Unix(),
		fpsUpdate:        make(chan int, 10),
		activeFiles:      make(map[string]*os.File),
		SendSignal:       sendSignal,
		allowRemoteInput: 1,
	}

	input.ReleaseAllModifierKeys()

	return sess, nil
}

// SetAllowRemoteInput toggles whether the host allows remote mouse and keyboard inputs
func (h *HostSession) SetAllowRemoteInput(allow bool) {
	if allow {
		atomic.StoreInt32(&h.allowRemoteInput, 1)
		h.SendControl(protocol.ControlMessage{
			Type:   protocol.TypeChat,
			Sender: "Sistema",
			Text:   "✅ O cliente ativou o controle remoto de mouse e teclado.",
		})
	} else {
		atomic.StoreInt32(&h.allowRemoteInput, 0)
		h.SendControl(protocol.ControlMessage{
			Type:   protocol.TypeChat,
			Sender: "Sistema",
			Text:   "⚠️ O cliente desativou o controle remoto (modo somente observação).",
		})
	}
}

// IsRemoteInputAllowed returns true if remote control inputs are currently accepted
func (h *HostSession) IsRemoteInputAllowed() bool {
	return atomic.LoadInt32(&h.allowRemoteInput) != 0
}

func (h *HostSession) IsActive() bool {
	if h == nil {
		return false
	}
	select {
	case <-h.ctx.Done():
		return false
	default:
		return true
	}
}

func (h *HostSession) handleDisconnectGrace(source string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.disconnectTimer != nil {
		return // Tolerância já em andamento
	}
	log.Printf("[Host] %s desconectado temporariamente. Tolerância de reconexão de 20s ativada...", source)
	h.disconnectTimer = time.AfterFunc(20*time.Second, func() {
		h.mu.Lock()
		h.disconnectTimer = nil
		h.mu.Unlock()
		log.Printf("[Host] Tempo limite de reconexão esgotado (20s). Encerrando sessão.")
		h.Close()
	})
}

func (h *HostSession) cancelDisconnectGrace() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.disconnectTimer != nil {
		h.disconnectTimer.Stop()
		h.disconnectTimer = nil
		log.Printf("[Host] Conexão P2P restabelecida com sucesso dentro da tolerância!")
	}
}

func (h *HostSession) HandleRemoteOffer(targetID string, sdpStr string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	config := pion.Configuration{
		ICEServers: defaultICEServers,
	}

	pc, err := pion.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	h.peerConn = pc

	pc.OnICEConnectionStateChange(func(state pion.ICEConnectionState) {
		log.Printf("[Host] ICE P2P State: %s", state.String())
		if state == pion.ICEConnectionStateConnected || state == pion.ICEConnectionStateCompleted {
			h.cancelDisconnectGrace()
		} else if state == pion.ICEConnectionStateDisconnected {
			h.handleDisconnectGrace("ICE")
		} else if state == pion.ICEConnectionStateFailed || state == pion.ICEConnectionStateClosed {
			h.cancelDisconnectGrace()
			log.Printf("[Host] ICE desconectado criticamente (%s). Encerrando sessão host.", state.String())
			go h.Close()
		}
	})

	pc.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		log.Printf("[Host] PeerConnection State: %s", state.String())
		if state == pion.PeerConnectionStateConnected {
			h.cancelDisconnectGrace()
		} else if state == pion.PeerConnectionStateDisconnected {
			h.handleDisconnectGrace("PeerConnection")
		} else if state == pion.PeerConnectionStateFailed || state == pion.PeerConnectionStateClosed {
			h.cancelDisconnectGrace()
			log.Printf("[Host] PeerConnection desconectado criticamente (%s). Encerrando sessão host.", state.String())
			go h.Close()
		}
	})

	pc.OnICECandidate(func(c *pion.ICECandidate) {
		if c == nil {
			return
		}
		cJSON, err := json.Marshal(c.ToJSON())
		if err != nil {
			return
		}
		if h.SendSignal != nil {
			h.SendSignal(protocol.SignalingMessage{
				Action:    protocol.ActionCandidate,
				TargetID:  targetID,
				Candidate: cJSON,
			})
		}
	})

	pc.OnDataChannel(func(dc *pion.DataChannel) {
		log.Printf("[Host] WebRTC DataChannel conectado: %s", dc.Label())

		if dc.Label() == "input" {
			h.inputChannel = dc
			dc.OnOpen(func() {
				numDisplays := capture.GetNumDisplays()
				initInfo, _ := json.Marshal(protocol.ControlMessage{
					Type:    protocol.TypeInitInfo,
					Monitor: numDisplays,
				})
				_ = dc.Send(initInfo)
			})
			dc.OnMessage(func(msg pion.DataChannelMessage) {
				h.HandleControlData(msg.Data)
			})
			dc.OnClose(func() {
				log.Println("[Host] Input DataChannel fechado pelo cliente.")
				go h.Close()
			})
		} else if dc.Label() == "video" {
			h.videoChannel = dc
			dc.OnClose(func() {
				log.Println("[Host] Video DataChannel fechado pelo cliente.")
				go h.Close()
			})
		} else if dc.Label() == "audio" {
			h.mu.Lock()
			h.audioChannel = dc
			h.mu.Unlock()
			dc.OnOpen(func() {
				h.mu.Lock()
				h.audioChannel = dc
				h.mu.Unlock()
				log.Println("[Host] Audio DataChannel aberto e pronto para streaming.")
			})
			dc.OnClose(func() {
				h.mu.Lock()
				h.audioChannel = nil
				h.mu.Unlock()
				log.Println("[Host] Audio DataChannel fechado.")
			})
		}
	})

	offer := pion.SessionDescription{
		Type: pion.SDPTypeOffer,
		SDP:  sdpStr,
	}

	if err := pc.SetRemoteDescription(offer); err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create answer: %w", err)
	}

	gatherComplete := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("failed to set local description: %w", err)
	}

	<-gatherComplete

	if h.SendSignal != nil {
		h.SendSignal(protocol.SignalingMessage{
			Action:   protocol.ActionAnswer,
			TargetID: targetID,
			SDP:      pc.LocalDescription().SDP,
		})
	}

	return nil
}

func (h *HostSession) AddICECandidate(candidateJSON []byte) error {
	h.mu.Lock()
	pc := h.peerConn
	h.mu.Unlock()

	if pc == nil {
		return fmt.Errorf("peer connection not initialized")
	}

	var init pion.ICECandidateInit
	if err := json.Unmarshal(candidateJSON, &init); err != nil {
		return err
	}
	return pc.AddICECandidate(init)
}

func (h *HostSession) HandleControlData(data []byte) {
	atomic.StoreInt64(&h.lastActivity, time.Now().Unix())
	var ctrl protocol.ControlMessage
	if err := json.Unmarshal(data, &ctrl); err != nil {
		return
	}

	if ctrl.Type != "ping" && ctrl.Type != "pong" {
		atomic.StoreInt64(&h.lastInputTime, time.Now().Unix())
	}

	switch ctrl.Type {
	case protocol.TypeMouseMove:
		if h.IsRemoteInputAllowed() {
			_ = input.MoveMouseAbsolute(ctrl.X, ctrl.Y)
		}
	case protocol.TypeMouseDown:
		if h.IsRemoteInputAllowed() {
			_ = input.MouseDown(ctrl.Button)
		}
	case protocol.TypeMouseUp:
		if h.IsRemoteInputAllowed() {
			_ = input.MouseUp(ctrl.Button)
		}
	case protocol.TypeWheel:
		if h.IsRemoteInputAllowed() {
			_ = input.MouseWheel(ctrl.DeltaY)
		}
	case protocol.TypeKeyDown:
		if h.IsRemoteInputAllowed() {
			_ = input.KeyDownEx(ctrl.Key, ctrl.Code, ctrl.KeyCode, ctrl.Alt, ctrl.Ctrl, ctrl.Shift, ctrl.Meta)
		}
	case protocol.TypeKeyUp:
		if h.IsRemoteInputAllowed() {
			_ = input.KeyUpEx(ctrl.Key, ctrl.Code, ctrl.KeyCode, ctrl.Alt, ctrl.Ctrl, ctrl.Shift, ctrl.Meta)
		}
	case protocol.TypeMonitorSwitch:
		_ = h.capturer.SetDisplayIndex(ctrl.Monitor)
		bounds := h.capturer.GetBounds()
		input.SetActiveMonitorBounds(bounds)
		if h.inputChannel != nil {
			info, _ := json.Marshal(protocol.ControlMessage{
				Type:      protocol.TypeInitInfo,
				Monitor:   capture.GetNumDisplays(),
				ActiveMon: ctrl.Monitor,
			})
			_ = h.inputChannel.Send(info)
		}
	case protocol.TypeConfig:
		if ctrl.Quality > 0 {
			h.capturer.SetQuality(ctrl.Quality)
		}
		if ctrl.FPS > 0 && ctrl.FPS <= 60 {
			h.FPS = ctrl.FPS
			select {
			case h.fpsUpdate <- ctrl.FPS:
			default:
			}
		}
	case protocol.TypeSysCommand:
		switch ctrl.Command {
		case "close", "disconnect":
			log.Printf("[Host] Comando de encerramento de sessão recebido do cliente (%s)", h.ClientID)
			go h.Close()
		case "release_keys":
			input.ReleaseAllModifierKeys()
		case "lock", "win_l":
			input.LockWorkstation()
		case "taskmgr":
			input.OpenTaskManager()
		case "desktop", "win_d":
			input.ShowDesktop()
		case "explorer", "win_e":
			input.OpenFileExplorer()
		case "run", "win_r":
			input.OpenRunDialog()
		case "win", "start_menu":
			input.OpenStartMenu()
		case "alt_f4":
			input.CloseActiveWindow()
		case "alt_tab":
			input.SwitchWindowAltTab()
		case "cad", "ctrl_alt_del":
			input.SendCtrlAltDel()
		case "ctrl_a", "select_all":
			input.SendCtrlShortcut(0x41) // 'A'
		case "ctrl_c", "copy":
			input.SendCtrlShortcut(0x43) // 'C'
		case "ctrl_v", "paste":
			input.SendCtrlShortcut(0x56) // 'V'
		case "ctrl_z", "undo":
			input.SendCtrlShortcut(0x5A) // 'Z'
		case "block_input":
			_ = input.BlockLocalInput(ctrl.Block)
		case "autolock_on":
			h.mu.Lock()
			h.autoLockOnClose = true
			h.mu.Unlock()
			log.Printf("[Host] Auto-Lock ao desconectar ATIVADO para cliente (%s)", h.ClientID)
		case "autolock_off":
			h.mu.Lock()
			h.autoLockOnClose = false
			h.mu.Unlock()
			log.Printf("[Host] Auto-Lock ao desconectar DESATIVADO para cliente (%s)", h.ClientID)
		case "curtain_on":
			_ = input.EnablePrivacyScreen(true)
			log.Printf("[Host] Modo Cortina (Privacy Screen) ATIVADO pelo cliente (%s)", h.ClientID)
		case "curtain_off":
			_ = input.EnablePrivacyScreen(false)
			log.Printf("[Host] Modo Cortina (Privacy Screen) DESATIVADO pelo cliente (%s)", h.ClientID)
		case "elevate", "request_admin":
			if config.IsAdmin() {
				log.Printf("[Host] Solicitação de elevação ignorada: processo já é Administrador")
			} else {
				log.Printf("[Host] Solicitação de elevação UAC recebida do operador (%s). Disparando UAC...", h.ClientID)
				input.PlayNotificationSound()
				go func() {
					time.Sleep(300 * time.Millisecond)
					_ = config.ElevateSelf("")
				}()
			}
		case "reboot":
			_ = input.RebootMachine()
		case "shutdown":
			_ = input.ShutdownMachine()
		case "suspend", "sleep":
			input.SuspendMachine()
		}
	case protocol.TypeCurtain:
		if ctrl.Command == "on" || ctrl.Command == "curtain_on" || ctrl.Block {
			_ = input.EnablePrivacyScreen(true)
			log.Printf("[Host] Modo Cortina (Privacy Screen) ATIVADO")
		} else {
			_ = input.EnablePrivacyScreen(false)
			log.Printf("[Host] Modo Cortina (Privacy Screen) DESATIVADO")
		}
	case protocol.TypeAutoLock:
		h.mu.Lock()
		h.autoLockOnClose = (ctrl.Command == "on" || ctrl.Command == "autolock_on" || ctrl.Block)
		h.mu.Unlock()
		log.Printf("[Host] Auto-Lock ao desconectar: %v", h.autoLockOnClose)
	case "close":
		log.Printf("[Host] Mensagem de encerramento direto recebida do cliente (%s)", h.ClientID)
		go h.Close()
	case protocol.TypeClipboard:
		if ctrl.Text != "" {
			h.mu.Lock()
			h.lastReceivedClipText = ctrl.Text
			h.mu.Unlock()
			_ = input.SetClipboardTextAndPaste(ctrl.Text)
		}
	case protocol.TypeFileStart:
		if ctrl.FileName != "" {
			cleanName := filepath.Base(ctrl.FileName)
			destDir := ctrl.DestDir
			if destDir == "" {
				userProfile := os.Getenv("USERPROFILE")
				if userProfile == "" {
					userProfile = "."
				}
				destDir = filepath.Join(userProfile, "Downloads", "RemoteAccess_Transfers")
			}
			_ = os.MkdirAll(destDir, 0755)
			destPath := filepath.Join(destDir, cleanName)

			f, err := os.Create(destPath)
			if err == nil {
				h.mu.Lock()
				if oldF, exists := h.activeFiles[cleanName]; exists {
					_ = oldF.Close()
				}
				h.activeFiles[cleanName] = f
				h.mu.Unlock()
			}
		}
	case protocol.TypeFileChunk:
		if ctrl.FileName != "" && ctrl.Chunk != "" {
			cleanName := filepath.Base(ctrl.FileName)
			h.mu.Lock()
			f, exists := h.activeFiles[cleanName]
			h.mu.Unlock()
			if exists && f != nil {
				data, err := base64.StdEncoding.DecodeString(ctrl.Chunk)
				if err == nil {
					_, _ = f.Write(data)
				}
			}
		}
	case protocol.TypeFileEnd:
		if ctrl.FileName != "" {
			cleanName := filepath.Base(ctrl.FileName)
			h.mu.Lock()
			f, exists := h.activeFiles[cleanName]
			delete(h.activeFiles, cleanName)
			h.mu.Unlock()
			if exists && f != nil {
				_ = f.Close()
				tray.ShowBalloonNotification("RemoteAccess — Arquivo Recebido", fmt.Sprintf("📁 %s\nTransferência concluída com sucesso.", cleanName))
				if h.OnChat != nil {
					h.OnChat("Sistema", fmt.Sprintf("📁 Arquivo recebido: %s", cleanName))
				}
			}
		}

	case protocol.TypeFSDrives:
		drives := fsmod.ListDrives()
		h.SendControl(protocol.ControlMessage{
			Type:   protocol.TypeFSDrives,
			Drives: drives,
			Path:   fsmod.GetDefaultRoot(),
		})

	case protocol.TypeFSList:
		targetPath := ctrl.Path
		absPath, items, err := fsmod.ListDirectory(targetPath)
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		h.SendControl(protocol.ControlMessage{
			Type:   protocol.TypeFSListResp,
			Path:   absPath,
			Items:  items,
			Status: errMsg,
			Drives: fsmod.ListDrives(),
		})

	case protocol.TypeFSDelete:
		err := fsmod.DeleteItem(ctrl.Path)
		status := "ok"
		if err != nil {
			status = err.Error()
		}
		// Responde com listagem atualizada do pai
		parentDir := filepath.Dir(ctrl.Path)
		absPath, items, _ := fsmod.ListDirectory(parentDir)
		h.SendControl(protocol.ControlMessage{
			Type:   protocol.TypeFSListResp,
			Path:   absPath,
			Items:  items,
			Status: status,
			Drives: fsmod.ListDrives(),
		})

	case protocol.TypeFSMkdir:
		createdPath, err := fsmod.CreateDirectory(ctrl.Path, ctrl.FileName)
		status := "ok"
		if err != nil {
			status = err.Error()
		}
		absPath, items, _ := fsmod.ListDirectory(ctrl.Path)
		h.SendControl(protocol.ControlMessage{
			Type:     protocol.TypeFSListResp,
			Path:     absPath,
			Items:    items,
			Status:   status,
			FileName: createdPath,
			Drives:   fsmod.ListDrives(),
		})

	case protocol.TypeFSDownload:
		// Cliente solicitou o download de um arquivo do Host para a máquina do operador
		go func(filePath string) {
			file, err := os.Open(filePath)
			if err != nil {
				h.SendControl(protocol.ControlMessage{
					Type:   protocol.TypeFSOpResult,
					Status: fmt.Sprintf("Erro ao abrir arquivo: %v", err),
					Path:   filePath,
				})
				return
			}
			defer file.Close()

			fi, err := file.Stat()
			if err != nil || fi.IsDir() {
				return
			}

			fileName := fi.Name()
			fileSize := fi.Size()

			// Notifica início do download
			h.SendControl(protocol.ControlMessage{
				Type:     protocol.TypeFileStart,
				FileName: fileName,
				FileSize: fileSize,
			})

			buf := make([]byte, 48*1024)
			seq := 1
			for {
				n, readErr := file.Read(buf)
				if n > 0 {
					b64 := base64.StdEncoding.EncodeToString(buf[:n])
					h.SendControl(protocol.ControlMessage{
						Type:     protocol.TypeFileChunk,
						FileName: fileName,
						Chunk:    b64,
						Seq:      seq,
					})
					seq++
					time.Sleep(2 * time.Millisecond) // controle de vazão
				}
				if readErr != nil {
					break
				}
			}

			h.SendControl(protocol.ControlMessage{
				Type:     protocol.TypeFileEnd,
				FileName: fileName,
			})
		}(ctrl.Path)
	case protocol.TypeChat:
		if h.OnChat != nil {
			h.OnChat(h.ClientID, ctrl.Text)
		}
	case protocol.TypePing:
		pongData, _ := json.Marshal(protocol.ControlMessage{
			Type: protocol.TypePong,
			Time: ctrl.Time,
		})
		if h.inputChannel != nil && h.inputChannel.ReadyState() == pion.DataChannelStateOpen {
			_ = h.inputChannel.Send(pongData)
		} else if h.SendSignal != nil {
			h.SendSignal(protocol.SignalingMessage{
				Action:  protocol.ActionData,
				Payload: string(pongData),
			})
		}
	}
}

// SendControl sends a control packet (such as chat, pong, or status) to the remote client
func (h *HostSession) SendControl(ctrl protocol.ControlMessage) {
	data, err := json.Marshal(ctrl)
	if err != nil {
		return
	}
	if h.inputChannel != nil && h.inputChannel.ReadyState() == pion.DataChannelStateOpen {
		_ = h.inputChannel.Send(data)
		return
	}
	if h.SendSignal != nil {
		h.SendSignal(protocol.SignalingMessage{
			Action:   protocol.ActionData,
			TargetID: h.ClientID,
			Payload:  string(data),
		})
	}
}

func (h *HostSession) StartStreaming() {
	if !atomic.CompareAndSwapInt32(&h.running, 0, 1) {
		return
	}

	// Start WASAPI loopback audio capture
	_ = audio.StartLoopbackCapture(h.ctx, func(pcmData []byte) {
		h.mu.Lock()
		aChan := h.audioChannel
		h.mu.Unlock()
		if aChan != nil && aChan.ReadyState() == pion.DataChannelStateOpen {
			if aChan.BufferedAmount() <= 64*1024 {
				_ = aChan.Send(pcmData)
			}
		}
	})

	// Start Host clipboard monitor for bidirectional clipboard synchronization
	go func() {
		lastSeq := input.GetClipboardSequence()
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-h.ctx.Done():
				return
			case <-ticker.C:
				currSeq := input.GetClipboardSequence()
				if currSeq != lastSeq {
					lastSeq = currSeq
					txt, err := input.GetClipboardText()
					if err == nil && txt != "" {
						h.mu.Lock()
						isEcho := (txt == h.lastReceivedClipText || txt == h.lastSentClipText)
						if !isEcho {
							h.lastSentClipText = txt
						}
						h.mu.Unlock()
						if !isEcho {
							h.SendControl(protocol.ControlMessage{
								Type: protocol.TypeClipSync,
								Text: txt,
							})
						}
					}
				}
			}
		}
	}()

	// Start Host dynamic cursor shape monitor (~120ms)
	go func() {
		lastCur := ""
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-h.ctx.Done():
				return
			case <-ticker.C:
				cur := capture.GetActiveCursorType()
				if cur != lastCur {
					lastCur = cur
					h.SendControl(protocol.ControlMessage{
						Type:    protocol.TypeCursor,
						Command: cur,
					})
				}
			}
		}
	}()

	go func() {
		defer atomic.StoreInt32(&h.running, 0)
		ticker := time.NewTicker(time.Second / time.Duration(h.FPS))
		defer ticker.Stop()
		watchdogTicker := time.NewTicker(1 * time.Second)
		defer watchdogTicker.Stop()

		for {
			select {
			case <-h.ctx.Done():
				return
			case newFPS := <-h.fpsUpdate:
				if newFPS > 0 && newFPS <= 60 {
					ticker.Reset(time.Second / time.Duration(newFPS))
					log.Printf("[Host] FPS de streaming reconfigurado dinamicamente para %d FPS", newFPS)
				}
			case <-watchdogTicker.C:
				now := time.Now().Unix()
				last := atomic.LoadInt64(&h.lastActivity)
				if last > 0 && now-last > 90 {
					log.Printf("[Host] Conexão de rede com cliente (%s) interrompida (>90s sem resposta). Encerrando sessão.", h.ClientID)
					go h.Close()
					return
				}

				// Only apply inactivity timeout if NOT running on direct P2P DataChannel (to save cloud relay bandwidth)
				h.mu.Lock()
				isDirectP2P := (h.videoChannel != nil && h.videoChannel.ReadyState() == pion.DataChannelStateOpen)
				h.mu.Unlock()

				if !isDirectP2P {
					lastInput := atomic.LoadInt64(&h.lastInputTime)
					if lastInput > 0 && now-lastInput > 130 {
						log.Printf("[Host] Sessão em Relay/Nuvem do cliente (%s) inativa por mais de 2 minutos. Encerrando para poupar banda do servidor.", h.ClientID)
						go h.Close()
						return
					}
				}
			case <-ticker.C:
				h.mu.Lock()
				vChan := h.videoChannel
				h.mu.Unlock()

				// Transmit video exclusively via WebRTC P2P DataChannel (100% direct, zero cloud server bandwidth)
				if vChan == nil || vChan.ReadyState() != pion.DataChannelStateOpen {
					continue
				}

				// Guard against buffer bloat / backpressure freeze: skip frame if client has > 256KB unconsumed
				if vChan.BufferedAmount() > 256*1024 {
					continue
				}

				frameData, err := h.capturer.CaptureFrame()
				if err != nil {
					continue
				}

				frameID := atomic.AddUint32(&h.frameCounter, 1)
				totalLen := len(frameData)
				const maxChunk = 60 * 1024
				totalChunks := (totalLen + maxChunk - 1) / maxChunk
				if totalChunks <= 1 {
					pkt := protocol.EncodeVideoChunk(frameID, 0, 1, frameData)
					_ = vChan.Send(pkt)
				} else {
					for i := 0; i < totalChunks; i++ {
						start := i * maxChunk
						end := start + maxChunk
						if end > totalLen {
							end = totalLen
						}
						pkt := protocol.EncodeVideoChunk(frameID, uint16(i), uint16(totalChunks), frameData[start:end])
						_ = vChan.Send(pkt)
					}
				}
			}
		}
	}()
}

func (h *HostSession) StopStreaming() {
	if atomic.CompareAndSwapInt32(&h.running, 1, 0) {
		h.cancel()
	}
}

func (h *HostSession) Close() {
	_ = input.BlockLocalInput(false)
	_ = input.EnablePrivacyScreen(false) // Always restore physical display when session ends
	input.ReleaseAllModifierKeys()        // Always clean up any held keys when session finishes
	h.cancel()
	h.mu.Lock()
	shouldLock := h.autoLockOnClose
	if h.disconnectTimer != nil {
		h.disconnectTimer.Stop()
		h.disconnectTimer = nil
	}
	if h.inputChannel != nil && h.inputChannel.ReadyState() == pion.DataChannelStateOpen {
		closeMsg, _ := json.Marshal(protocol.ControlMessage{
			Type: "close",
			Text: "Sessão encerrada pelo Host.",
		})
		_ = h.inputChannel.Send(closeMsg)
	}
	// Notify Cloud Signaling Server so the controller and relay are in 100% sync
	if h.SendSignal != nil && h.ClientID != "" {
		h.SendSignal(protocol.SignalingMessage{
			Action:   protocol.ActionClose,
			TargetID: h.ClientID,
			Message:  "Sessão encerrada pelo Host.",
		})
	}
	for _, f := range h.activeFiles {
		if f != nil {
			_ = f.Close()
		}
	}
	h.activeFiles = make(map[string]*os.File)
	if h.peerConn != nil {
		_ = h.peerConn.Close()
		h.peerConn = nil
	}
	onClose := h.OnClose
	h.OnClose = nil
	h.mu.Unlock()

	if shouldLock {
		log.Printf("[Host] Bloqueando estação de trabalho (Auto-Lock on Disconnect)")
		input.LockWorkstation()
	}

	if onClose != nil {
		onClose()
	}
}
