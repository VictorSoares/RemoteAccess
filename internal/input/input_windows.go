//go:build windows

package input

import (
	"os/exec"
	"strings"
	"syscall"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procSetCursorPos             = user32.NewProc("SetCursorPos")
	procMouseEvent               = user32.NewProc("mouse_event")
	procKeybdEvent               = user32.NewProc("keybd_event")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procBlockInput               = user32.NewProc("BlockInput")
	procLockWorkStation          = user32.NewProc("LockWorkStation")
	procSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware       = user32.NewProc("SetProcessDPIAware")
)

func init() {
	// Enable Per-Monitor DPI Awareness V2 (-4) so Windows gives us exact physical pixel coordinates
	if procSetProcessDpiAwarenessCtx.Find() == nil {
		_, _, _ = procSetProcessDpiAwarenessCtx.Call(uintptr(^uintptr(3))) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	} else if procSetProcessDPIAware.Find() == nil {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

const (
	mouseeventfMove       = 0x0001
	mouseeventfLeftdown   = 0x0002
	mouseeventfLeftup     = 0x0004
	mouseeventfRightdown  = 0x0008
	mouseeventfRightup    = 0x0010
	mouseeventfMiddledown = 0x0020
	mouseeventfMiddleup   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfAbsolute   = 0x8000

	keyeventfExtendedkey = 0x0001
	keyeventfKeyup       = 0x0002
	keyeventfUnicode     = 0x0004
)

// BlockLocalInput blocks or unblocks physical mouse and keyboard inputs on the local machine
func BlockLocalInput(block bool) error {
	val := uintptr(0)
	if block {
		val = 1
	}
	procBlockInput.Call(val)
	return nil
}

// LockWorkstation immediately locks the Windows desktop
func LockWorkstation() {
	procLockWorkStation.Call()
}

// OpenTaskManager launches Windows Task Manager directly
func OpenTaskManager() {
	_ = exec.Command("taskmgr.exe").Start()
}

// ShowDesktop minimizes or restores all windows
func ShowDesktop() {
	_ = KeyDown("Meta", "MetaLeft", 91)
	_ = KeyDown("d", "KeyD", 68)
	_ = KeyUp("d", "KeyD", 68)
	_ = KeyUp("Meta", "MetaLeft", 91)
}

// MoveMouseAbsolute sets the cursor to an absolute ratio [0.0, 1.0] across the primary screen
func MoveMouseAbsolute(ratioX, ratioY float64) error {
	if ratioX < 0 {
		ratioX = 0
	} else if ratioX > 1 {
		ratioX = 1
	}
	if ratioY < 0 {
		ratioY = 0
	} else if ratioY > 1 {
		ratioY = 1
	}

	w, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	if w == 0 {
		w = 1920
	}
	if h == 0 {
		h = 1080
	}

	targetX := int32(ratioX * float64(w))
	targetY := int32(ratioY * float64(h))

	procSetCursorPos.Call(uintptr(targetX), uintptr(targetY))
	return nil
}

// MouseDown triggers a mouse press (0: left, 1: middle, 2: right)
func MouseDown(button int) error {
	var flag uintptr
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
	procMouseEvent.Call(flag, 0, 0, 0, 0)
	return nil
}

// MouseUp triggers a mouse release (0: left, 1: middle, 2: right)
func MouseUp(button int) error {
	var flag uintptr
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
	procMouseEvent.Call(flag, 0, 0, 0, 0)
	return nil
}

// MouseWheel triggers a vertical scroll wheel action
func MouseWheel(deltaY int) error {
	var delta int32
	if deltaY > 0 {
		delta = 120
	} else if deltaY < 0 {
		delta = -120
	}
	procMouseEvent.Call(mouseeventfWheel, 0, 0, uintptr(uint32(delta)), 0)
	return nil
}

// KeyDown simulates key press
func KeyDown(key, code string, keyCode int) error {
	vk := resolveVK(key, code, keyCode)
	if vk != 0 {
		flags := uintptr(0)
		if isExtendedKey(vk) {
			flags |= keyeventfExtendedkey
		}
		procKeybdEvent.Call(uintptr(vk), 0, flags, 0)
		return nil
	}
	return nil
}

// KeyUp simulates key release
func KeyUp(key, code string, keyCode int) error {
	vk := resolveVK(key, code, keyCode)
	if vk != 0 {
		flags := uintptr(keyeventfKeyup)
		if isExtendedKey(vk) {
			flags |= keyeventfExtendedkey
		}
		procKeybdEvent.Call(uintptr(vk), 0, flags, 0)
		return nil
	}
	return nil
}

func isExtendedKey(vk uint16) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2D, 0x2E, 0x5B, 0x5C, 0x5D:
		return true
	default:
		return false
	}
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
	"PrintScreen":  0x2C,
	"Insert":       0x2D,
	"Delete":       0x2E,
	"Meta":         0x5B,
	"MetaLeft":     0x5B,
	"MetaRight":    0x5C,
	"ContextMenu":  0x5D,
	"Numpad0":      0x60,
	"Numpad1":      0x61,
	"Numpad2":      0x62,
	"Numpad3":      0x63,
	"Numpad4":      0x64,
	"Numpad5":      0x65,
	"Numpad6":      0x66,
	"Numpad7":      0x67,
	"Numpad8":      0x68,
	"Numpad9":      0x69,
	"NumpadMultiply": 0x6A,
	"NumpadAdd":      0x6B,
	"NumpadSubtract": 0x6D,
	"NumpadDecimal":  0x6E,
	"NumpadDivide":   0x6F,
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
	"NumLock":      0x90,
	"ScrollLock":   0x91,
	"Semicolon":    0xBA,
	"Equal":        0xBB,
	"Comma":        0xBC,
	"Minus":        0xBD,
	"Period":       0xBE,
	"Slash":        0xBF,
	"Backquote":    0xC0,
	"BracketLeft":  0xDB,
	"Backslash":    0xDC,
	"BracketRight": 0xDD,
	"Quote":        0xDE,
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
	if len(key) == 1 {
		ch := key[0]
		if ch >= 'a' && ch <= 'z' {
			return uint16(ch - 32)
		}
		if ch >= 'A' && ch <= 'Z' {
			return uint16(ch)
		}
		if ch >= '0' && ch <= '9' {
			return uint16(ch)
		}
	}
	return 0
}
