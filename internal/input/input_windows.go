//go:build windows

package input

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procSetCursorPos     = user32.NewProc("SetCursorPos")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove       = 0x0001
	mouseeventfLeftdown   = 0x0002
	mouseeventfLeftup     = 0x0004
	mouseeventfRightdown  = 0x0008
	mouseeventfRightup    = 0x0010
	mouseeventfMiddledown = 0x0020
	mouseeventfMiddleup   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfHwheel     = 0x1000
	mouseeventfAbsolute   = 0x8000

	keyeventfExtendedkey = 0x0001
	keyeventfKeyup       = 0x0002
	keyeventfUnicode     = 0x0004
	keyeventfScancode    = 0x0008
)

type mouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type inputStructure struct {
	inputType uint32
	mi        mouseInput
	_pad      [8]byte
}

func sendMouseInput(flags uint32, dx, dy int32, data uint32) error {
	var in inputStructure
	in.inputType = inputMouse
	in.mi.dx = dx
	in.mi.dy = dy
	in.mi.mouseData = data
	in.mi.dwFlags = flags

	ret, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if ret == 0 {
		return fmt.Errorf("SendInput mouse failed: %v", err)
	}
	return nil
}

// MoveMouseAbsolute sets the cursor to an absolute ratio [0.0, 1.0] across the primary screen
func MoveMouseAbsolute(ratioX, ratioY float64) error {
	if ratioX < 0 {
		ratioX = 0
	}
	if ratioX > 1 {
		ratioX = 1
	}
	if ratioY < 0 {
		ratioY = 0
	}
	if ratioY > 1 {
		ratioY = 1
	}

	// Normalize to [0, 65535]
	dx := int32(ratioX * 65535)
	dy := int32(ratioY * 65535)

	return sendMouseInput(mouseeventfMove|mouseeventfAbsolute, dx, dy, 0)
}

// MouseDown triggers a mouse press (0: left, 1: middle, 2: right)
func MouseDown(button int) error {
	var flag uint32
	switch button {
	case 0:
		flag = mouseeventfLeftdown
	case 1:
		flag = mouseeventfMiddledown
	case 2:
		flag = mouseeventfRightdown
	default:
		return nil
	}
	return sendMouseInput(flag, 0, 0, 0)
}

// MouseUp triggers a mouse release (0: left, 1: middle, 2: right)
func MouseUp(button int) error {
	var flag uint32
	switch button {
	case 0:
		flag = mouseeventfLeftup
	case 1:
		flag = mouseeventfMiddleup
	case 2:
		flag = mouseeventfRightup
	default:
		return nil
	}
	return sendMouseInput(flag, 0, 0, 0)
}

// MouseWheel triggers a vertical scroll wheel action
func MouseWheel(deltaY int) error {
	var delta int32
	if deltaY > 0 {
		delta = 120
	} else if deltaY < 0 {
		delta = -120
	}
	return sendMouseInput(mouseeventfWheel, 0, 0, uint32(delta))
}

type kbInputStructure struct {
	inputType uint32
	ki        keybdInput
	_pad      [8]byte
}

func sendKeyboardInput(vk uint16, scan uint16, flags uint32) error {
	var in kbInputStructure
	in.inputType = inputKeyboard
	in.ki.wVk = vk
	in.ki.wScan = scan
	in.ki.dwFlags = flags

	ret, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if ret == 0 {
		return fmt.Errorf("SendInput keyboard failed: %v", err)
	}
	return nil
}

// Virtual Key Codes mapping
var jsKeyToVK = map[string]uint16{
	"Backspace":    0x08,
	"Tab":          0x09,
	"Enter":        0x0D,
	"Shift":        0x10,
	"ShiftLeft":    0x10,
	"ShiftRight":   0x10,
	"Control":      0x11,
	"ControlLeft":  0x11,
	"ControlRight": 0x11,
	"Alt":          0x12,
	"AltLeft":      0x12,
	"AltRight":     0x12,
	"Pause":        0x13,
	"CapsLock":     0x14,
	"Escape":       0x1B,
	"Space":        0x20,
	"PageUp":       0x21,
	"PageDown":     0x22,
	"End":          0x23,
	"Home":         0x24,
	"ArrowLeft":    0x25,
	"ArrowUp":      0x26,
	"ArrowRight":   0x27,
	"ArrowDown":    0x28,
	"Insert":       0x2D,
	"Delete":       0x2E,
	"Meta":         0x5B,
	"MetaLeft":     0x5B,
	"MetaRight":    0x5C,
	"ContextMenu":  0x5D,
	"F1":           0x70,
	"F2":           0x71,
	"F3":           0x72,
	"F4":           0x73,
	"F5":           0x74,
	"F6":           0x75,
	"F7":           0x76,
	"F8":           0x77,
	"F9":           0x78,
	"F10":          0x79,
	"F11":          0x7A,
	"F12":          0x7B,
}

// KeyDown simulates key press
func KeyDown(key, code string, keyCode int) error {
	vk := resolveVK(key, code, keyCode)
	if vk != 0 {
		return sendKeyboardInput(vk, 0, 0)
	}
	if len(key) == 1 {
		r := []rune(key)[0]
		return sendKeyboardInput(0, uint16(r), keyeventfUnicode)
	}
	return nil
}

// KeyUp simulates key release
func KeyUp(key, code string, keyCode int) error {
	vk := resolveVK(key, code, keyCode)
	if vk != 0 {
		return sendKeyboardInput(vk, 0, keyeventfKeyup)
	}
	if len(key) == 1 {
		r := []rune(key)[0]
		return sendKeyboardInput(0, uint16(r), keyeventfUnicode|keyeventfKeyup)
	}
	return nil
}

func resolveVK(key, code string, keyCode int) uint16 {
	if vk, ok := jsKeyToVK[code]; ok {
		return vk
	}
	if vk, ok := jsKeyToVK[key]; ok {
		return vk
	}
	if strings.HasPrefix(code, "Key") && len(code) == 4 {
		return uint16(code[3])
	}
	if strings.HasPrefix(code, "Digit") && len(code) == 6 {
		return uint16(code[5])
	}
	if keyCode >= 32 && keyCode <= 126 {
		return uint16(keyCode)
	}
	return 0
}
