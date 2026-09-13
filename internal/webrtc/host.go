package webrtc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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

	return &HostSession{
		ClientID:    clientID,
		ClientAlias: clientAlias,
		ConnectedAt: time.Now(),
		capturer:    capturer,
		FPS:         fps,
		Quality:     quality,
		ctx:         ctx,
		cancel:      cancel,
		SendSignal:  sendSignal,
	}, nil
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
		} else if dc.Label() == "video" {
			h.videoChannel = dc
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
		}
	case protocol.TypeSysCommand:
		switch ctrl.Command {
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

		for {
			select {
			case <-h.ctx.Done():
				return
			case <-ticker.C:
				frameData, err := h.capturer.CaptureFrame()
				if err != nil {
					continue
				}

				h.mu.Lock()
				vChan := h.videoChannel
				h.mu.Unlock()

				// 1. If WebRTC DataChannel is connected and open, send via P2P
				if vChan != nil && vChan.ReadyState() == pion.DataChannelStateOpen {
					_ = vChan.Send(frameData)
				}

				// 2. ALWAYS also send frame via WebSocket Relay to guarantee instant visual delivery on any firewall!
				if h.OnRelayFrame != nil {
					b64 := base64.StdEncoding.EncodeToString(frameData)
					h.OnRelayFrame(b64)
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
