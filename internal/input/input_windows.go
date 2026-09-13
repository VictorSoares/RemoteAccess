//go:build windows

package input

import (
	"encoding/base64"
	"fmt"
	"image"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	shell32                      = syscall.NewLazyDLL("shell32.dll")
	sasDll                       = syscall.NewLazyDLL("sas.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	powrprof                     = syscall.NewLazyDLL("powrprof.dll")
	procSetCursorPos             = user32.NewProc("SetCursorPos")
	procMouseEvent               = user32.NewProc("mouse_event")
	procKeybdEvent               = user32.NewProc("keybd_event")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procBlockInput               = user32.NewProc("BlockInput")
	procLockWorkStation          = user32.NewProc("LockWorkStation")
	procSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware       = user32.NewProc("SetProcessDPIAware")
	procSendSAS                  = sasDll.NewProc("SendSAS")
	procShellExecute             = shell32.NewProc("ShellExecuteW")
	procSetSuspendState          = powrprof.NewProc("SetSuspendState")
	procSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx           = user32.NewProc("CallNextHookEx")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procPostThreadMessageW       = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	procFlashWindowEx            = user32.NewProc("FlashWindowEx")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procMessageBeep              = user32.NewProc("MessageBeep")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procSendInput                = user32.NewProc("SendInput")

	activeBoundsMu sync.RWMutex
	activeBounds   image.Rectangle

	hookMu         sync.Mutex
	isHooked       bool
	kbdHook        uintptr
	mouseHook      uintptr
	hookThreadId   uint32
)

const (
	whKeyboardLL                = 13
	whMouseLL                   = 14
	wmQuit                      = 0x0012
	llkhfInjected               = 0x00000010 // Bit 4 is LLKHF_INJECTED for Keyboard
	llmhfInjected               = 0x00000001 // Bit 0 is LLMHF_INJECTED for Mouse
	remoteAccessMagicExtraInfo  = 0x52414343 // "RACC" magic tag to distinguish RemoteAccess inputs
	flashwAll                   = 0x00000003
	flashwTimerNoFg             = 0x0000000C
)

type kbdLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type msLLHookStruct struct {
	Pt          struct{ X, Y int32 }
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

func keyboardHookCallback(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 {
		kbd := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
		// If event is injected (synthesized by remote controller) or tagged with magic extra info, ALLOW IT!
		if (kbd.Flags&llkhfInjected != 0) || kbd.DwExtraInfo == remoteAccessMagicExtraInfo {
			ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
			return ret
		}
		// Block local physical keyboard press
		return 1
	}
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

func mouseHookCallback(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 {
		ms := (*msLLHookStruct)(unsafe.Pointer(lParam))
		// If event is injected (synthesized by remote controller) or tagged with magic extra info, ALLOW IT!
		if (ms.Flags&llmhfInjected != 0) || ms.DwExtraInfo == remoteAccessMagicExtraInfo {
			ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
			return ret
		}
		// Block local physical mouse event
		return 1
	}
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

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
	mouseeventfWheel       = 0x0800
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	keyeventfExtendedkey = 0x0001
	keyeventfKeyup       = 0x0002
	keyeventfUnicode     = 0x0004
)

// SetActiveMonitorBounds updates the active monitor's bounding box for multi-monitor cursor placement
func SetActiveMonitorBounds(b image.Rectangle) {
	activeBoundsMu.Lock()
	activeBounds = b
	activeBoundsMu.Unlock()
}

// BlockLocalInput blocks or unblocks physical mouse and keyboard inputs on the local machine
func BlockLocalInput(block bool) error {
	hookMu.Lock()
	defer hookMu.Unlock()

	// Install/Uninstall user-mode Low-Level Hooks that selectively filter physical inputs while allowing injected ones
	if block && !isHooked {
		isHooked = true
		startedChan := make(chan struct{})
		go func() {
			tid, _, _ := procGetCurrentThreadId.Call()
			hookThreadId = uint32(tid)

			cbKbd := syscall.NewCallback(keyboardHookCallback)
			cbMouse := syscall.NewCallback(mouseHookCallback)

			hKbd, _, _ := procSetWindowsHookExW.Call(whKeyboardLL, cbKbd, 0, 0)
			hMouse, _, _ := procSetWindowsHookExW.Call(whMouseLL, cbMouse, 0, 0)
			kbdHook = hKbd
			mouseHook = hMouse

			close(startedChan)

			var msg struct {
				Hwnd    uintptr
				Message uint32
				WParam  uintptr
				LParam  uintptr
				Time    uint32
				Pt      struct{ X, Y int32 }
			}
			for {
				ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
				if int32(ret) <= 0 || msg.Message == wmQuit {
					break
				}
			}

			if kbdHook != 0 {
				procUnhookWindowsHookEx.Call(kbdHook)
				kbdHook = 0
			}
			if mouseHook != 0 {
				procUnhookWindowsHookEx.Call(mouseHook)
				mouseHook = 0
			}
		}()
		<-startedChan
	} else if !block && isHooked {
		isHooked = false
		if hookThreadId != 0 {
			procPostThreadMessageW.Call(uintptr(hookThreadId), wmQuit, 0, 0)
			hookThreadId = 0
		}
		if kbdHook != 0 {
			procUnhookWindowsHookEx.Call(kbdHook)
			kbdHook = 0
		}
		if mouseHook != 0 {
			procUnhookWindowsHookEx.Call(mouseHook)
			mouseHook = 0
		}
	}

	return nil
}

// LockWorkstation immediately locks the Windows desktop
func LockWorkstation() {
	procLockWorkStation.Call()
}

// RebootMachine reboots the computer gracefully with a short 5-second countdown
func RebootMachine() error {
	cmd := exec.Command("shutdown.exe", "/r", "/t", "5", "/f", "/c", "Reiniciando via RemoteAccess...")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd.Run()
}

// ShutdownMachine powers off the computer gracefully with a short 5-second countdown
func ShutdownMachine() error {
	cmd := exec.Command("shutdown.exe", "/s", "/t", "5", "/f", "/c", "Desligando via RemoteAccess...")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd.Run()
}

// SuspendMachine places the computer into sleep/suspend mode instantly
func SuspendMachine() {
	if procSetSuspendState.Find() == nil {
		procSetSuspendState.Call(0, 0, 0)
		return
	}
	cmd := exec.Command("rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	_ = cmd.Run()
}

// FlashAppWindow flashes the application window on the taskbar to alert the user of new messages
func FlashAppWindow() {
	type flashwInfo struct {
		CbSize    uint32
		Hwnd      uintptr
		DwFlags   uint32
		UCount    uint32
		DwTimeout uint32
	}

	title, _ := syscall.UTF16PtrFromString("RemoteAccess")
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		var fi flashwInfo
		fi.CbSize = uint32(unsafe.Sizeof(fi))
		fi.Hwnd = hwnd
		fi.DwFlags = flashwAll | flashwTimerNoFg
		fi.UCount = 5
		fi.DwTimeout = 0
		procFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
	}
}

// BringAppToFront brings the application window to the foreground on the Windows desktop
func BringAppToFront() {
	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		var buf [256]uint16
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
		title := syscall.UTF16ToString(buf[:])
		if strings.Contains(title, "RemoteAccess") {
			procShowWindow.Call(hwnd, 9) // SW_RESTORE = 9
			procSetForegroundWindow.Call(hwnd)
			return 0 // stop enumeration
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)
}

// PlayNotificationSound plays the native Windows notification chime
func PlayNotificationSound() {
	if procMessageBeep.Find() == nil {
		procMessageBeep.Call(0x00000040) // MB_ICONASTERISK (Windows Notification / Asterisk sound)
	}
}

// OpenTaskManager launches Windows Task Manager directly without any console flashes
func OpenTaskManager() {
	// 1. Synthesize Ctrl + Shift + Esc (the native Windows shortcut for Task Manager)
	procKeybdEvent.Call(0x11, 0, 0, 0) // Ctrl down
	procKeybdEvent.Call(0x10, 0, 0, 0) // Shift down
	procKeybdEvent.Call(0x1B, 0, 0, 0) // Esc down
	time.Sleep(30 * time.Millisecond)
	procKeybdEvent.Call(0x1B, 0, keyeventfKeyup, 0) // Esc up
	procKeybdEvent.Call(0x10, 0, keyeventfKeyup, 0) // Shift up
	procKeybdEvent.Call(0x11, 0, keyeventfKeyup, 0) // Ctrl up

	// 2. Also invoke ShellExecute for taskmgr.exe
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString("taskmgr.exe")
	procShellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
}

// SendCtrlAltDel sends the Secure Attention Sequence (SAS) or launches Task Manager / Security options
func SendCtrlAltDel() {
	// 1. Attempt software SAS
	if procSendSAS.Find() == nil {
		ret, _, _ := procSendSAS.Call(0)
		if ret != 0 {
			return
		}
	}
	// 2. Synthesize Ctrl + Alt + Del keystrokes
	procKeybdEvent.Call(0x11, 0, 0, 0) // Ctrl down
	procKeybdEvent.Call(0x12, 0, 0, 0) // Alt down
	procKeybdEvent.Call(0x2E, 0, 0, 0) // Del down
	time.Sleep(50 * time.Millisecond)
	procKeybdEvent.Call(0x2E, 0, keyeventfKeyup, 0) // Del up
	procKeybdEvent.Call(0x12, 0, keyeventfKeyup, 0) // Alt up
	procKeybdEvent.Call(0x11, 0, keyeventfKeyup, 0) // Ctrl up

	// 3. Fallback when not running as a system service: open Task Manager directly
	OpenTaskManager()
}

// ShowDesktop minimizes or restores all windows cleanly with zero console windows
func ShowDesktop() {
	// Pure Windows Win + D key simulation (zero processes, zero CMD flashes)
	procKeybdEvent.Call(0x5B, 0, keyeventfExtendedkey, 0) // Win key down
	procKeybdEvent.Call(0x44, 0, 0, 0)                    // 'D' key down
	time.Sleep(35 * time.Millisecond)
	procKeybdEvent.Call(0x44, 0, keyeventfKeyup, 0)       // 'D' key up
	procKeybdEvent.Call(0x5B, 0, keyeventfExtendedkey|keyeventfKeyup, 0) // Win key up
}

// MoveMouseAbsolute sets the cursor to an absolute ratio [0.0, 1.0] across the active screen monitor
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

	activeBoundsMu.RLock()
	b := activeBounds
	activeBoundsMu.RUnlock()

	var originX, originY, w, h int32
	if b.Dx() > 0 && b.Dy() > 0 {
		originX = int32(b.Min.X)
		originY = int32(b.Min.Y)
		w = int32(b.Dx())
		h = int32(b.Dy())
	} else {
		sw, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
		sh, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
		w = int32(sw)
		h = int32(sh)
		if w == 0 {
			w = 1920
		}
		if h == 0 {
			h = 1080
		}
	}

	targetX := originX + int32(ratioX*float64(w))
	targetY := originY + int32(ratioY*float64(h))

	// Physical placement
	procSetCursorPos.Call(uintptr(targetX), uintptr(targetY))

	// Calculate absolute normalized virtual coordinates [0..65535] across virtual desktop
	vw, _, _ := procGetSystemMetrics.Call(78) // SM_CXVIRTUALSCREEN
	vh, _, _ := procGetSystemMetrics.Call(79) // SM_CYVIRTUALSCREEN
	vl, _, _ := procGetSystemMetrics.Call(76) // SM_XVIRTUALSCREEN
	vt, _, _ := procGetSystemMetrics.Call(77) // SM_YVIRTUALSCREEN
	if vw == 0 {
		vw = uintptr(w)
	}
	if vh == 0 {
		vh = uintptr(h)
	}

	normX := int32(((float64(targetX - int32(vl))) * 65535.0) / float64(vw))
	normY := int32(((float64(targetY - int32(vt))) * 65535.0) / float64(vh))

	// Synthesize hardware mouse move event with absolute virtual desk flags to trigger Windows DWM taskbar previews and hover states
	procMouseEvent.Call(
		uintptr(mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDesk),
		uintptr(normX),
		uintptr(normY),
		0,
		remoteAccessMagicExtraInfo,
	)
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
	procMouseEvent.Call(flag, 0, 0, 0, remoteAccessMagicExtraInfo)
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
	procMouseEvent.Call(flag, 0, 0, 0, remoteAccessMagicExtraInfo)
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
	procMouseEvent.Call(mouseeventfWheel, 0, 0, uintptr(uint32(delta)), remoteAccessMagicExtraInfo)
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
		procKeybdEvent.Call(uintptr(vk), 0, flags, remoteAccessMagicExtraInfo)
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
		procKeybdEvent.Call(uintptr(vk), 0, flags, remoteAccessMagicExtraInfo)
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

// SetClipboardTextAndPaste writes text into the Windows clipboard and synthesizes Ctrl+V to paste into active window
func SetClipboardTextAndPaste(text string) error {
	if text == "" {
		return nil
	}
	go func() {
		b64 := base64.StdEncoding.EncodeToString([]byte(text))
		script := fmt.Sprintf(`[System.Windows.Forms.Clipboard]::SetText([System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('%s')))`, b64)
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Add-Type -AssemblyName System.Windows.Forms; "+script)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = cmd.Run()

		time.Sleep(50 * time.Millisecond)
		procKeybdEvent.Call(0x11, 0, 0, remoteAccessMagicExtraInfo) // Ctrl down
		procKeybdEvent.Call(0x56, 0, 0, remoteAccessMagicExtraInfo) // 'V' down
		time.Sleep(30 * time.Millisecond)
		procKeybdEvent.Call(0x56, 0, keyeventfKeyup, remoteAccessMagicExtraInfo) // 'V' up
		procKeybdEvent.Call(0x11, 0, keyeventfKeyup, remoteAccessMagicExtraInfo) // Ctrl up
	}()
	return nil
}
