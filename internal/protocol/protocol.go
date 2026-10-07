package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Signaling Action Types
const (
	ActionRegister   = "register"
	ActionUnregister = "unregister"
	ActionConnect    = "connect"
	ActionOffer      = "offer"
	ActionAnswer     = "answer"
	ActionCandidate  = "candidate"
	ActionData       = "data"
	ActionStatus     = "status"
	ActionError      = "error"
	ActionClose      = "close"
)

// SignalingMessage represents a packet routed by the signaling relay server
type SignalingMessage struct {
	Action    string          `json:"action"`
	ID        string          `json:"id,omitempty"`
	Alias     string          `json:"alias,omitempty"`
	Monitors  int             `json:"monitors,omitempty"`
	MAC       string          `json:"mac,omitempty"`
	TargetID  string          `json:"targetId,omitempty"`
	Password  string          `json:"password,omitempty"`
	SDP       string          `json:"sdp,omitempty"`
	Candidate json.RawMessage `json:"candidate,omitempty"`
	Payload   string          `json:"payload,omitempty"` // For WebSocket Relay fallback
	Status    string          `json:"status,omitempty"`
	Message   string          `json:"message,omitempty"`
	Blocked   bool            `json:"blocked,omitempty"`
}

// Control Packet Types
const (
	TypeMouseMove     = "m"
	TypeMouseDown     = "md"
	TypeMouseUp       = "mu"
	TypeWheel         = "w"
	TypeKeyDown       = "kd"
	TypeKeyUp         = "ku"
	TypeClipboard     = "clip"
	TypePing          = "ping"
	TypePong          = "pong"
	TypeConfig        = "cfg"
	TypeMonitorSwitch = "mon_switch"
	TypeInitInfo      = "init_info"
	TypeChat          = "chat"
	TypeSysCommand    = "sys_cmd"
	TypeAuth          = "auth"
	TypeAuthOK        = "auth_ok"
	TypeAuthFail      = "auth_fail"
	TypeFileStart     = "file_start"
	TypeFileChunk     = "file_chunk"
	TypeFileEnd       = "file_end"
)

// ControlMessage represents mouse, keyboard, monitor, clipboard, chat or system action command
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
	Command  string  `json:"cmd,omitempty"`
	Block    bool    `json:"block,omitempty"`
	Time     float64 `json:"ts,omitempty"`
	Quality  int     `json:"q,omitempty"`
	FPS      int     `json:"fps,omitempty"`
	Monitor  int     `json:"mon,omitempty"`
	Sender   string  `json:"sender,omitempty"`
	Password string  `json:"pwd,omitempty"`
	FileName string  `json:"file_name,omitempty"`
	FileSize int64   `json:"file_size,omitempty"`
	Chunk    string  `json:"chunk,omitempty"`
	Seq      int     `json:"seq,omitempty"`
}

// EncodeVideoChunk prepends an 8-byte header [uint32 frameID, uint16 chunkIndex, uint16 totalChunks] to the chunk payload
func EncodeVideoChunk(frameID uint32, chunkIdx, totalChunks uint16, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], frameID)
	binary.BigEndian.PutUint16(buf[4:6], chunkIdx)
	binary.BigEndian.PutUint16(buf[6:8], totalChunks)
	copy(buf[8:], payload)
	return buf
}

// DecodeVideoChunk extracts the 8-byte header and payload from a video packet
func DecodeVideoChunk(data []byte) (frameID uint32, chunkIdx, totalChunks uint16, payload []byte, err error) {
	if len(data) < 8 {
		return 0, 0, 0, nil, fmt.Errorf("packet too short (%d bytes, minimum 8)", len(data))
	}
	frameID = binary.BigEndian.Uint32(data[0:4])
	chunkIdx = binary.BigEndian.Uint16(data[4:6])
	totalChunks = binary.BigEndian.Uint16(data[6:8])
	payload = data[8:]
	return frameID, chunkIdx, totalChunks, payload, nil
}
