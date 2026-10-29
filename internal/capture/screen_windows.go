//go:build windows

package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/kbinani/screenshot"
)

var (
	user32Capture              = syscall.NewLazyDLL("user32.dll")
	gdi32Capture               = syscall.NewLazyDLL("gdi32.dll")
	procGetDC                  = user32Capture.NewProc("GetDC")
	procReleaseDC              = user32Capture.NewProc("ReleaseDC")
	procGetCursorInfo          = user32Capture.NewProc("GetCursorInfo")
	procGetSystemMetrics       = user32Capture.NewProc("GetSystemMetrics")
	procCreateCompatibleDC     = gdi32Capture.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32Capture.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32Capture.NewProc("SelectObject")
	procCreateDIBSection       = gdi32Capture.NewProc("CreateDIBSection")
	procBitBlt                 = gdi32Capture.NewProc("BitBlt")
	procDeleteObject           = gdi32Capture.NewProc("DeleteObject")
	procDeleteDC               = gdi32Capture.NewProc("DeleteDC")
	procGetDIBits              = gdi32Capture.NewProc("GetDIBits")
	procLoadCursorW            = user32Capture.NewProc("LoadCursorW")
	procOpenInputDesktop       = user32Capture.NewProc("OpenInputDesktop")
	procSetThreadDesktop       = user32Capture.NewProc("SetThreadDesktop")
	procCloseDesktop           = user32Capture.NewProc("CloseDesktop")
)

// attachThreadToInputDesktop ensures the calling OS thread is attached to the active
// interactive input desktop (handling UAC, lock screens, and Windows station switches).
func attachThreadToInputDesktop() {
	hDesk, _, _ := procOpenInputDesktop.Call(0, 0, 0x10000000)
	if hDesk != 0 {
		procSetThreadDesktop.Call(hDesk)
		procCloseDesktop.Call(hDesk)
	}
}

const (
	idcArrow      = 32512
	idcIBeam      = 32513
	idcWait       = 32514
	idcCross      = 32515
	idcSizeNWSE   = 32642
	idcSizeNESW   = 32643
	idcSizeWE     = 32644
	idcSizeNS     = 32645
	idcSizeAll    = 32646
	idcNo         = 32648
	idcHand       = 32649
	idcAppStarting= 32650
)

var (
	hArrow, _, _       = procLoadCursorW.Call(0, uintptr(idcArrow))
	hIBeam, _, _       = procLoadCursorW.Call(0, uintptr(idcIBeam))
	hHand, _, _        = procLoadCursorW.Call(0, uintptr(idcHand))
	hWait, _, _        = procLoadCursorW.Call(0, uintptr(idcWait))
	hCross, _, _       = procLoadCursorW.Call(0, uintptr(idcCross))
	hSizeNWSE, _, _    = procLoadCursorW.Call(0, uintptr(idcSizeNWSE))
	hSizeNESW, _, _    = procLoadCursorW.Call(0, uintptr(idcSizeNESW))
	hSizeWE, _, _      = procLoadCursorW.Call(0, uintptr(idcSizeWE))
	hSizeNS, _, _      = procLoadCursorW.Call(0, uintptr(idcSizeNS))
	hSizeAll, _, _     = procLoadCursorW.Call(0, uintptr(idcSizeAll))
	hNo, _, _          = procLoadCursorW.Call(0, uintptr(idcNo))
	hAppStarting, _, _ = procLoadCursorW.Call(0, uintptr(idcAppStarting))
)

// GetActiveCursorType inspects the active Windows cursor and returns its CSS cursor name
func GetActiveCursorType() string {
	var ci cursorInfo
	ci.cbSize = uint32(unsafe.Sizeof(ci))
	ret, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci)))
	if ret == 0 || (ci.flags&cursorShowing) == 0 {
		return "default"
	}
	switch ci.hCursor {
	case hHand:
		return "pointer"
	case hIBeam:
		return "text"
	case hWait:
		return "wait"
	case hAppStarting:
		return "progress"
	case hCross:
		return "crosshair"
	case hSizeWE:
		return "ew-resize"
	case hSizeNS:
		return "ns-resize"
	case hSizeNWSE:
		return "nwse-resize"
	case hSizeNESW:
		return "nesw-resize"
	case hSizeAll:
		return "move"
	case hNo:
		return "not-allowed"
	default:
		return "default"
	}
}

type point struct {
	x, y int32
}

type cursorInfo struct {
	cbSize      uint32
	flags       uint32
	hCursor     uintptr
	ptScreenPos point
}

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

const cursorShowing = 0x00000001

var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// 12x19 standard high-contrast pointer bitmap
// 1 = Black outline, 2 = White body, 0 = Transparent
var cursorPattern = [][]byte{
	{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0},
	{1, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 0},
	{1, 2, 2, 1, 2, 2, 1, 0, 0, 0, 0, 0},
	{1, 2, 1, 0, 1, 2, 2, 1, 0, 0, 0, 0},
	{1, 1, 0, 0, 1, 2, 2, 1, 0, 0, 0, 0},
	{1, 0, 0, 0, 0, 1, 2, 2, 1, 0, 0, 0},
	{0, 0, 0, 0, 0, 1, 2, 2, 1, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 1, 2, 2, 1, 0, 0},
	{0, 0, 0, 0, 0, 0, 1, 1, 1, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
}

type ScreenCapturer struct {
	mu           sync.Mutex
	DisplayIndex int
	Bounds       image.Rectangle
	Quality      int // 1 to 100
}

func GetNumDisplays() int {
	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		return 1
	}
	return n
}

func NewScreenCapturer(displayIndex int) (*ScreenCapturer, error) {
	n := screenshot.NumActiveDisplays()
	var bounds image.Rectangle
	if n > 0 && displayIndex < n {
		bounds = screenshot.GetDisplayBounds(displayIndex)
	} else {
		sw, _, _ := procGetSystemMetrics.Call(0)
		sh, _, _ := procGetSystemMetrics.Call(1)
		w := int(sw)
		h := int(sh)
		if w <= 0 {
			w = 1920
		}
		if h <= 0 {
			h = 1080
		}
		bounds = image.Rect(0, 0, w, h)
	}

	return &ScreenCapturer{
		DisplayIndex: displayIndex,
		Bounds:       bounds,
		Quality:      55,
	}, nil
}

func (sc *ScreenCapturer) SetDisplayIndex(index int) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if index == -1 {
		// Unified virtual desktop across all connected displays
		sc.DisplayIndex = -1
		vl, _, _ := procGetSystemMetrics.Call(76) // SM_XVIRTUALSCREEN
		vt, _, _ := procGetSystemMetrics.Call(77) // SM_YVIRTUALSCREEN
		vw, _, _ := procGetSystemMetrics.Call(78) // SM_CXVIRTUALSCREEN
		vh, _, _ := procGetSystemMetrics.Call(79) // SM_CYVIRTUALSCREEN
		w := int(int32(vw))
		h := int(int32(vh))
		if w <= 0 {
			w = 1920
		}
		if h <= 0 {
			h = 1080
		}
		minX := int(int32(vl))
		minY := int(int32(vt))
		sc.Bounds = image.Rect(minX, minY, minX+w, minY+h)
		return nil
	}

	n := screenshot.NumActiveDisplays()
	if n > 0 && index < n && index >= 0 {
		sc.DisplayIndex = index
		sc.Bounds = screenshot.GetDisplayBounds(index)
		return nil
	}

	sc.DisplayIndex = 0
	sw, _, _ := procGetSystemMetrics.Call(0)
	sh, _, _ := procGetSystemMetrics.Call(1)
	sc.Bounds = image.Rect(0, 0, int(sw), int(sh))
	return nil
}

// GetResolution returns width and height of the display
func (sc *ScreenCapturer) GetResolution() (int, int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.Bounds.Dx(), sc.Bounds.Dy()
}

// GetBounds returns the image.Rectangle bounds of the active display
func (sc *ScreenCapturer) GetBounds() image.Rectangle {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.Bounds
}

// SetQuality updates JPEG compression quality
func (sc *ScreenCapturer) SetQuality(q int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if q < 10 {
		q = 10
	} else if q > 100 {
		q = 100
	}
	sc.Quality = q
}

func (sc *ScreenCapturer) drawCursor(img *image.RGBA) {
	var ci cursorInfo
	ci.cbSize = uint32(unsafe.Sizeof(ci))
	ret, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci)))
	if ret == 0 || (ci.flags&cursorShowing) == 0 {
		return
	}

	curX := int(ci.ptScreenPos.x) - sc.Bounds.Min.X
	curY := int(ci.ptScreenPos.y) - sc.Bounds.Min.Y

	width := img.Rect.Dx()
	height := img.Rect.Dy()

	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	for r, row := range cursorPattern {
		py := curY + r
		if py < 0 || py >= height {
			continue
		}
		for c, val := range row {
			px := curX + c
			if px < 0 || px >= width {
				continue
			}
			if val == 1 {
				img.SetRGBA(px, py, black)
			} else if val == 2 {
				img.SetRGBA(px, py, white)
			}
		}
	}
}

func captureDirectGDI(bounds image.Rectangle) (*image.RGBA, error) {
	attachThreadToInputDesktop()

	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		sw, _, _ := procGetSystemMetrics.Call(0)
		sh, _, _ := procGetSystemMetrics.Call(1)
		w = int(sw)
		h = int(sh)
		if w <= 0 {
			w = 1920
		}
		if h <= 0 {
			h = 1080
		}
		bounds = image.Rect(0, 0, w, h)
	}

	hdcScreen, _, _ := procGetDC.Call(0)
	if hdcScreen == 0 {
		return nil, fmt.Errorf("failed to get screen DC")
	}
	defer procReleaseDC.Call(0, hdcScreen)

	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcScreen)
	if hdcMem == 0 {
		return nil, fmt.Errorf("failed to create compatible DC")
	}
	defer procDeleteDC.Call(hdcMem)

	var bi bitmapInfoHeader
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	bi.BiWidth = int32(w)
	bi.BiHeight = -int32(h) // top-down
	bi.BiPlanes = 1
	bi.BiBitCount = 32
	bi.BiCompression = 0 // BI_RGB

	var pBits uintptr
	hbmDIB, _, err := procCreateDIBSection.Call(hdcMem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&pBits)), 0, 0)
	if hbmDIB == 0 || pBits == 0 {
		return nil, fmt.Errorf("CreateDIBSection failed: %w", err)
	}
	defer procDeleteObject.Call(hbmDIB)

	hbmOld, _, _ := procSelectObject.Call(hdcMem, hbmDIB)
	defer procSelectObject.Call(hdcMem, hbmOld)

	const srccopy = 0x00CC0020 | 0x40000000 // SRCCOPY | CAPTUREBLT (includes layered & composite windows)
	ret, _, _ := procBitBlt.Call(hdcMem, 0, 0, uintptr(w), uintptr(h), hdcScreen, uintptr(bounds.Min.X), uintptr(bounds.Min.Y), srccopy)
	if ret == 0 {
		// Fallback without CAPTUREBLT if driver restricts it
		ret, _, _ = procBitBlt.Call(hdcMem, 0, 0, uintptr(w), uintptr(h), hdcScreen, uintptr(bounds.Min.X), uintptr(bounds.Min.Y), 0x00CC0020)
		if ret == 0 {
			return nil, fmt.Errorf("BitBlt failed")
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rawSlice := unsafe.Slice((*byte)(unsafe.Pointer(pBits)), w*h*4)
	for i := 0; i < len(img.Pix); i += 4 {
		b := rawSlice[i]
		g := rawSlice[i+1]
		r := rawSlice[i+2]
		img.Pix[i] = r
		img.Pix[i+1] = g
		img.Pix[i+2] = b
		img.Pix[i+3] = 255
	}

	return img, nil
}

// CaptureFrame captures a single frame, burns the mouse cursor, and compresses to JPEG
func (sc *ScreenCapturer) CaptureFrame() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	sc.mu.Lock()
	quality := sc.Quality
	displayIdx := sc.DisplayIndex
	bounds := sc.Bounds
	sc.mu.Unlock()

	var img *image.RGBA
	var err error
	if screenshot.NumActiveDisplays() > displayIdx && displayIdx >= 0 {
		img, err = screenshot.CaptureDisplay(displayIdx)
	}
	if err != nil || img == nil {
		attachThreadToInputDesktop()
		if screenshot.NumActiveDisplays() > displayIdx && displayIdx >= 0 {
			img, err = screenshot.CaptureDisplay(displayIdx)
		}
	}
	if err != nil || img == nil {
		img, err = captureDirectGDI(bounds)
		if err != nil {
			return nil, err
		}
	}

	sc.drawCursor(img)

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	err = jpeg.Encode(buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, err
	}

	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res, nil
}

// CaptureImage returns raw *image.RGBA with cursor
func (sc *ScreenCapturer) CaptureImage() (*image.RGBA, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	sc.mu.Lock()
	displayIdx := sc.DisplayIndex
	bounds := sc.Bounds
	sc.mu.Unlock()

	var img *image.RGBA
	var err error
	if screenshot.NumActiveDisplays() > displayIdx && displayIdx >= 0 {
		img, err = screenshot.CaptureDisplay(displayIdx)
	}
	if err != nil || img == nil {
		attachThreadToInputDesktop()
		if screenshot.NumActiveDisplays() > displayIdx && displayIdx >= 0 {
			img, err = screenshot.CaptureDisplay(displayIdx)
		}
	}
	if err != nil || img == nil {
		img, err = captureDirectGDI(bounds)
		if err != nil {
			return nil, err
		}
	}
	sc.drawCursor(img)
	return img, nil
}
