//go:build windows

package tray

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procExtractIconExW   = shell32.NewProc("ExtractIconExW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmAppTray     = 0x0400 + 100
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmDestroy     = 0x0002

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	tpmBottomAlign = 0x0020
	tpmLeftAlign   = 0x0000
	cfUnicodeText  = 13
	gmemMoveable   = 0x0002

	idmOpen   = 1001
	idmCopyID = 1002
	idmAdmin  = 1003
	idmExit   = 1004
)

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uTimeoutOrVer    uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type point struct {
	X, Y int32
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type TrayManager struct {
	hwnd      uintptr
	nid       notifyIconDataW
	hostID    string
	isAdmin   bool
	onOpen    func()
	onAdmin   func()
	onExit    func()
}

var globalTray *TrayManager

func copyTextToClipboard(text string) {
	utf16, _ := syscall.UTF16FromString(text)
	dataSize := len(utf16) * 2
	hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(dataSize))
	if hMem == 0 {
		return
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return
	}
	copy((*[1 << 20]byte)(unsafe.Pointer(ptr))[:dataSize], (*[1 << 20]byte)(unsafe.Pointer(&utf16[0]))[:dataSize])
	procGlobalUnlock.Call(hMem)

	procOpenClipboard.Call(0)
	procEmptyClipboard.Call()
	procSetClipboardData.Call(cfUnicodeText, hMem)
	procCloseClipboard.Call()
}

func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if globalTray == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return ret
	}

	switch msg {
	case wmAppTray:
		switch lParam {
		case wmLButtonUp, wmLButtonDbl:
			if globalTray.onOpen != nil {
				go globalTray.onOpen()
			}
		case wmRButtonUp:
			hMenu, _, _ := procCreatePopupMenu.Call()
			if hMenu != 0 {
				titleStr, _ := syscall.UTF16PtrFromString("⚡ Abrir RemoteAccess")
				procAppendMenuW.Call(hMenu, mfString, idmOpen, uintptr(unsafe.Pointer(titleStr)))

				idLabel := fmt.Sprintf("📋 Copiar Meu ID (%s)", globalTray.hostID)
				idStr, _ := syscall.UTF16PtrFromString(idLabel)
				procAppendMenuW.Call(hMenu, mfString, idmCopyID, uintptr(unsafe.Pointer(idStr)))

				if !globalTray.isAdmin {
					adminStr, _ := syscall.UTF16PtrFromString("🛡️ Executar como Administrador")
					procAppendMenuW.Call(hMenu, mfString, idmAdmin, uintptr(unsafe.Pointer(adminStr)))
				}

				procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

				exitStr, _ := syscall.UTF16PtrFromString("❌ Sair")
				procAppendMenuW.Call(hMenu, mfString, idmExit, uintptr(unsafe.Pointer(exitStr)))

				var pt point
				procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				procSetForegroundWindow.Call(hwnd)

				ret, _, _ := procTrackPopupMenu.Call(
					hMenu,
					tpmLeftAlign|tpmBottomAlign|0x0100, // TPM_RETURNCMD = 0x0100
					uintptr(pt.X),
					uintptr(pt.Y),
					0,
					hwnd,
					0,
				)
				procDestroyMenu.Call(hMenu)

				switch ret {
				case idmOpen:
					if globalTray.onOpen != nil {
						go globalTray.onOpen()
					}
				case idmCopyID:
					copyTextToClipboard(globalTray.hostID)
				case idmAdmin:
					if globalTray.onAdmin != nil {
						go globalTray.onAdmin()
					}
				case idmExit:
					if globalTray.onExit != nil {
						go globalTray.onExit()
					}
				}
			}
		}
		return 0

	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

// StartTray initializes and runs the native Windows tray icon loop
func StartTray(hostID string, isAdmin bool, onOpen func(), onAdmin func(), onExit func()) *TrayManager {
	tm := &TrayManager{
		hostID:  hostID,
		isAdmin: isAdmin,
		onOpen:  onOpen,
		onAdmin: onAdmin,
		onExit:  onExit,
	}
	globalTray = tm

	go func() {
		runtime.LockOSThread()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		className, _ := syscall.UTF16PtrFromString("RemoteAccessTrayClass")

		// Extract native crisp icons from executable resources
		exePath, _ := os.Executable()
		exeUtf16, _ := syscall.UTF16PtrFromString(exePath)
		var hIconLarge, hIconSmall uintptr
		procExtractIconExW.Call(
			uintptr(unsafe.Pointer(exeUtf16)),
			0,
			uintptr(unsafe.Pointer(&hIconLarge)),
			uintptr(unsafe.Pointer(&hIconSmall)),
			1,
		)

		hTrayIcon := hIconSmall
		if hTrayIcon == 0 {
			hTrayIcon = hIconLarge
		}

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   syscall.NewCallback(trayWndProc),
			hInstance:     hInst,
			lpszClassName: className,
			hIcon:         hIconLarge,
			hIconSm:       hIconSmall,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		wndName, _ := syscall.UTF16PtrFromString("RemoteAccessTrayWindow")
		hwnd, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(wndName)),
			0,
			0, 0, 0, 0,
			0, 0, hInst, 0,
		)
		tm.hwnd = hwnd

		tipText := fmt.Sprintf("RemoteAccess Portable Pro (ID: %s) - Online", hostID)
		tipUtf16, _ := syscall.UTF16FromString(tipText)

		nid := notifyIconDataW{
			cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
			hWnd:             hwnd,
			uID:              1,
			uFlags:           nifMessage | nifIcon | nifTip,
			uCallbackMessage: wmAppTray,
			hIcon:            hTrayIcon,
		}
		copy(nid.szTip[:], tipUtf16)

		tm.nid = nid
		procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
		log.Println("[Bandeja] Ícone na bandeja do Windows ativo.")

		var msg struct {
			Hwnd    uintptr
			Message uint32
			WParam  uintptr
			LParam  uintptr
			Time    uint32
			Pt      point
		}
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}

		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	}()

	return tm
}

func (tm *TrayManager) Remove() {
	if tm != nil && tm.hwnd != 0 {
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&tm.nid)))
		procPostMessageW.Call(tm.hwnd, wmDestroy, 0, 0)
	}
}
