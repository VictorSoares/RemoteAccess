package protocol

import "encoding/json"

// Signaling Action Types
const (
	ActionRegister  = "register"
	ActionConnect   = "connect"
	ActionOffer     = "offer"
	ActionAnswer    = "answer"
	ActionCandidate = "candidate"
	ActionData      = "data"
	ActionStatus    = "status"
	ActionError     = "error"
	ActionClose     = "close"
)

// SignalingMessage represents a packet routed by the signaling relay server
type SignalingMessage struct {
	Action    string          `json:"action"`
	ID        string          `json:"id,omitempty"`
	TargetID  string          `json:"targetId,omitempty"`
	Password  string          `json:"password,omitempty"`
	SDP       string          `json:"sdp,omitempty"`
	Candidate json.RawMessage `json:"candidate,omitempty"`
	Payload   string          `json:"payload,omitempty"` // For WebSocket Relay fallback
	Status    string          `json:"status,omitempty"`
	Message   string          `json:"message,omitempty"`
}

// Control Packet Types
const (
	TypeMouseMove = "m"
	TypeMouseDown = "md"
	TypeMouseUp   = "mu"
	TypeWheel     = "w"
	TypeKeyDown   = "kd"
	TypeKeyUp     = "ku"
	TypeClipboard = "clip"
	TypePing      = "ping"
	TypePong      = "pong"
	TypeConfig    = "cfg"
	TypeAuth      = "auth"
	TypeAuthOK    = "auth_ok"
	TypeAuthFail  = "auth_fail"
)

// ControlMessage represents mouse, keyboard or control command
type ControlMessage struct {
	Type     string  `json:"t"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	Button   int     `json:"b,omitempty"`
	DeltaY   int     `json:"dy,omitempty"`
	Key      string  `json:"k,omitempty"`
	Code     string  `json:"c,omitempty"`
	KeyCode  int     `json:"kc,omitempty"`
	Text     string  `json:"text,omitempty"`
	Time     int64   `json:"ts,omitempty"`
	Quality  int     `json:"q,omitempty"`
	FPS      int     `json:"fps,omitempty"`
	Password string  `json:"pwd,omitempty"`
}
