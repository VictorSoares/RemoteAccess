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
	"remoteaccess/internal/capture"
	"remoteaccess/internal/input"
	"remoteaccess/internal/protocol"
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
		},
	},
}

type HostSession struct {
	mu           sync.Mutex
	peerConn     *pion.PeerConnection
	inputChannel *pion.DataChannel
	videoChannel *pion.DataChannel
	capturer     *capture.DXGICapturer
	ctx          context.Context
	cancel       context.CancelFunc
	running      int32
	FPS          int
	Quality      int
	ClientID     string
	ClientAlias  string
	ConnectedAt  time.Time
	activeFiles  map[string]*os.File
	lastActivity int64
	fpsUpdate    chan int
	OnStatus     func(status string, msg string)
	SendSignal   func(msg protocol.SignalingMessage)
	OnRelayFrame func(jpegBase64 string)
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
		ClientID:     clientID,
		ClientAlias:  clientAlias,
		ConnectedAt:  time.Now(),
		capturer:     capturer,
		FPS:          fps,
		Quality:      quality,
		ctx:          ctx,
		cancel:       cancel,
		lastActivity: time.Now().Unix(),
		fpsUpdate:    make(chan int, 10),
		activeFiles:  make(map[string]*os.File),
		SendSignal:   sendSignal,
	}

	return sess, nil
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
		log.Printf("[Host] ICE P2P State: %s (Relay WSS ativo em paralelo)", state.String())
	})

	pc.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		log.Printf("[Host] PeerConnection State: %s (Relay WSS ativo em paralelo)", state.String())
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
				log.Println("[Host] Video DataChannel fechado.")
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

	switch ctrl.Type {
	case protocol.TypeMouseMove:
		_ = input.MoveMouseAbsolute(ctrl.X, ctrl.Y)
	case protocol.TypeMouseDown:
		_ = input.MouseDown(ctrl.Button)
	case protocol.TypeMouseUp:
		_ = input.MouseUp(ctrl.Button)
	case protocol.TypeWheel:
		_ = input.MouseWheel(ctrl.DeltaY)
	case protocol.TypeKeyDown:
		_ = input.KeyDown(ctrl.Key, ctrl.Code, ctrl.KeyCode)
	case protocol.TypeKeyUp:
		_ = input.KeyUp(ctrl.Key, ctrl.Code, ctrl.KeyCode)
	case protocol.TypeMonitorSwitch:
		_ = h.capturer.SetDisplayIndex(ctrl.Monitor)
		input.SetActiveMonitorBounds(h.capturer.GetBounds())
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
		case "lock", "win_l":
			input.LockWorkstation()
		case "taskmgr":
			input.OpenTaskManager()
		case "desktop", "win_d":
			input.ShowDesktop()
		case "cad", "ctrl_alt_del":
			input.SendCtrlAltDel()
		case "run", "win_r":
			_ = input.KeyDown("Meta", "MetaLeft", 91)
			_ = input.KeyDown("r", "KeyR", 82)
			time.Sleep(50 * time.Millisecond)
			_ = input.KeyUp("r", "KeyR", 82)
			_ = input.KeyUp("Meta", "MetaLeft", 91)
		case "alt_tab":
			_ = input.KeyDown("Alt", "AltLeft", 18)
			_ = input.KeyDown("Tab", "Tab", 9)
			time.Sleep(50 * time.Millisecond)
			_ = input.KeyUp("Tab", "Tab", 9)
			_ = input.KeyUp("Alt", "AltLeft", 18)
		case "block_input":
			_ = input.BlockLocalInput(ctrl.Block)
		case "reboot":
			_ = input.RebootMachine()
		case "shutdown":
			_ = input.ShutdownMachine()
		case "suspend", "sleep":
			input.SuspendMachine()
		}
	case "close":
		log.Printf("[Host] Mensagem de encerramento direto recebida do cliente (%s)", h.ClientID)
		go h.Close()
	case protocol.TypeClipboard:
		if ctrl.Text != "" {
			_ = input.SetClipboardTextAndPaste(ctrl.Text)
		}
	case protocol.TypeFileStart:
		if ctrl.FileName != "" {
			cleanName := filepath.Base(ctrl.FileName)
			userProfile := os.Getenv("USERPROFILE")
			if userProfile == "" {
				userProfile = "."
			}
			destDir := filepath.Join(userProfile, "Downloads", "RemoteAccess_Transfers")
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
				if h.OnChat != nil {
					h.OnChat("Sistema", fmt.Sprintf("📁 Arquivo recebido: %s (Salvo em Downloads\\RemoteAccess_Transfers)", cleanName))
				}
			}
		}
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

	go func() {
		defer atomic.StoreInt32(&h.running, 0)
		ticker := time.NewTicker(time.Second / time.Duration(h.FPS))
		defer ticker.Stop()
		watchdogTicker := time.NewTicker(1 * time.Second)
		defer watchdogTicker.Stop()
		var lastRelayTime time.Time

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
				last := atomic.LoadInt64(&h.lastActivity)
				if last > 0 && time.Now().Unix()-last > 30 {
					log.Printf("[Host] Cliente (%s) inativo por mais de 30s. Encerrando sessão automaticamente.", h.ClientID)
					go h.Close()
					return
				}
			case <-ticker.C:
				frameData, err := h.capturer.CaptureFrame()
				if err != nil {
					continue
				}

				h.mu.Lock()
				vChan := h.videoChannel
				h.mu.Unlock()

				// 1. If WebRTC DataChannel is connected and open, send via Direct P2P (Ultra-low latency, zero server bandwidth)
				if vChan != nil && vChan.ReadyState() == pion.DataChannelStateOpen {
					// Guard against buffer bloat / backpressure freeze: skip frame if client has > 1MB unconsumed
					if vChan.BufferedAmount() > 1024*1024 {
						continue
					}
					_ = vChan.Send(frameData)
				} else if h.OnRelayFrame != nil {
					// 2. Fallback to WebSocket Relay only when WebRTC P2P DataChannel is not yet connected
					// Rate limit to max 12 FPS so cloud WebSocket buffer is never congested and ping remains ultra-low
					if time.Since(lastRelayTime) >= 80*time.Millisecond {
						lastRelayTime = time.Now()
						b64 := base64.StdEncoding.EncodeToString(frameData)
						h.OnRelayFrame(b64)
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
	h.cancel()
	h.mu.Lock()
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

	if onClose != nil {
		onClose()
	}
}
