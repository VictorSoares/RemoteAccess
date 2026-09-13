//go:build windows

package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
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
	procBitBlt                 = gdi32Capture.NewProc("BitBlt")
	procDeleteObject           = gdi32Capture.NewProc("DeleteObject")
	procDeleteDC               = gdi32Capture.NewProc("DeleteDC")
	procGetDIBits              = gdi32Capture.NewProc("GetDIBits")
)

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

	hbmScreen, _, _ := procCreateCompatibleBitmap.Call(hdcScreen, uintptr(w), uintptr(h))
	if hbmScreen == 0 {
		return nil, fmt.Errorf("failed to create compatible bitmap")
	}
	defer procDeleteObject.Call(hbmScreen)

	hbmOld, _, _ := procSelectObject.Call(hdcMem, hbmScreen)
	defer procSelectObject.Call(hdcMem, hbmOld)

	const srccopy = 0x00CC0020
	ret, _, _ := procBitBlt.Call(hdcMem, 0, 0, uintptr(w), uintptr(h), hdcScreen, uintptr(bounds.Min.X), uintptr(bounds.Min.Y), srccopy)
	if ret == 0 {
		return nil, fmt.Errorf("BitBlt failed")
	}

	var bi bitmapInfoHeader
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	bi.BiWidth = int32(w)
	bi.BiHeight = -int32(h) // top-down
	bi.BiPlanes = 1
	bi.BiBitCount = 32
	bi.BiCompression = 0 // BI_RGB

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	ret, _, _ = procGetDIBits.Call(hdcMem, hbmScreen, 0, uintptr(h), uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&bi)), 0)
	if ret == 0 {
		return nil, fmt.Errorf("GetDIBits failed")
	}

	// Convert BGRA to RGBA
	for i := 0; i < len(img.Pix); i += 4 {
		b := img.Pix[i]
		r := img.Pix[i+2]
		img.Pix[i] = r
		img.Pix[i+2] = b
		img.Pix[i+3] = 255
	}

	return img, nil
}

// CaptureFrame captures a single frame, burns the mouse cursor, and compresses to JPEG
func (sc *ScreenCapturer) CaptureFrame() ([]byte, error) {
	sc.mu.Lock()
	quality := sc.Quality
	displayIdx := sc.DisplayIndex
	bounds := sc.Bounds
	sc.mu.Unlock()

	var img *image.RGBA
	var err error
	if screenshot.NumActiveDisplays() > displayIdx {
		img, err = screenshot.CaptureDisplay(displayIdx)
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
	sc.mu.Lock()
	displayIdx := sc.DisplayIndex
	bounds := sc.Bounds
	sc.mu.Unlock()

	var img *image.RGBA
	var err error
	if screenshot.NumActiveDisplays() > displayIdx {
		img, err = screenshot.CaptureDisplay(displayIdx)
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
