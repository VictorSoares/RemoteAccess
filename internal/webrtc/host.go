package webrtc

import (
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
			"stun:stun.cloudflare.com:3478",
		},
	},
}

type HostSession struct {
	mu           sync.Mutex
	peerConn     *pion.PeerConnection
	inputChannel *pion.DataChannel
	videoChannel *pion.DataChannel
	capturer     *capture.ScreenCapturer
	stopCapture  chan struct{}
	running      int32
	FPS          int
	Quality      int
	OnStatus     func(status string, msg string)
	SendSignal   func(msg protocol.SignalingMessage)
	OnRelayFrame func(jpegBase64 string)
}

func NewHostSession(fps int, quality int, sendSignal func(protocol.SignalingMessage)) (*HostSession, error) {
	capturer, err := capture.NewScreenCapturer(0)
	if err != nil {
		return nil, fmt.Errorf("failed to init screen capture: %w", err)
	}
	capturer.SetQuality(quality)

	if fps <= 0 {
		fps = 30
	}

	return &HostSession{
		capturer:    capturer,
		FPS:         fps,
		Quality:     quality,
		stopCapture: make(chan struct{}),
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
		log.Printf("[Host] ICE Connection State: %s", state.String())
		if h.OnStatus != nil {
			h.OnStatus("ice_state", state.String())
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
		log.Printf("[Host] WebRTC DataChannel recebido: %s", dc.Label())

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
	case protocol.TypeConfig:
		if ctrl.Quality > 0 {
			h.capturer.SetQuality(ctrl.Quality)
		}
		if ctrl.FPS > 0 && ctrl.FPS <= 60 {
			h.FPS = ctrl.FPS
		}
	case protocol.TypePing:
		if h.inputChannel != nil && h.inputChannel.ReadyState() == pion.DataChannelStateOpen {
			resp, _ := json.Marshal(protocol.ControlMessage{
				Type: protocol.TypePong,
				Time: ctrl.Time,
			})
			_ = h.inputChannel.Send(resp)
		}
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
			case <-h.stopCapture:
				return
			case <-ticker.C:
				frameData, err := h.capturer.CaptureFrame()
				if err != nil {
					continue
				}

				webrtcSent := false
				h.mu.Lock()
				vChan := h.videoChannel
				h.mu.Unlock()

				// 1. Try sending via WebRTC P2P DataChannel if connected
				if vChan != nil && vChan.ReadyState() == pion.DataChannelStateOpen {
					if err := vChan.Send(frameData); err == nil {
						webrtcSent = true
					}
				}

				// 2. If WebRTC is not active, relay frame via WebSocket over port 443!
				if !webrtcSent && h.OnRelayFrame != nil {
					b64 := base64.StdEncoding.EncodeToString(frameData)
					h.OnRelayFrame(b64)
				}
			}
		}
	}()
}

func (h *HostSession) StopStreaming() {
	if atomic.CompareAndSwapInt32(&h.running, 1, 0) {
		select {
		case h.stopCapture <- struct{}{}:
		default:
		}
	}
}

func (h *HostSession) Close() {
	h.StopStreaming()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.peerConn != nil {
		_ = h.peerConn.Close()
		h.peerConn = nil
	}
}
