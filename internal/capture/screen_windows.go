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
	user32Capture        = syscall.NewLazyDLL("user32.dll")
	procGetCursorInfo    = user32Capture.NewProc("GetCursorInfo")
	procGetSystemMetrics = user32Capture.NewProc("GetSystemMetrics")
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
	return screenshot.NumActiveDisplays()
}

func NewScreenCapturer(displayIndex int) (*ScreenCapturer, error) {
	n := screenshot.NumActiveDisplays()
	if n == 0 {
		return nil, fmt.Errorf("no active displays found")
	}
	if displayIndex >= n {
		displayIndex = 0
	}
	bounds := screenshot.GetDisplayBounds(displayIndex)

	return &ScreenCapturer{
		DisplayIndex: displayIndex,
		Bounds:       bounds,
		Quality:      65,
	}, nil
}

func (sc *ScreenCapturer) SetDisplayIndex(index int) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	n := screenshot.NumActiveDisplays()
	if index < 0 || index >= n {
		return fmt.Errorf("invalid display index: %d", index)
	}

	sc.DisplayIndex = index
	sc.Bounds = screenshot.GetDisplayBounds(index)
	return nil
}

// GetResolution returns width and height of the display
func (sc *ScreenCapturer) GetResolution() (int, int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.Bounds.Dx(), sc.Bounds.Dy()
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

// CaptureFrame captures a single frame, burns the mouse cursor, and compresses to JPEG
func (sc *ScreenCapturer) CaptureFrame() ([]byte, error) {
	sc.mu.Lock()
	quality := sc.Quality
	displayIdx := sc.DisplayIndex
	sc.mu.Unlock()

	img, err := screenshot.CaptureDisplay(displayIdx)
	if err != nil {
		return nil, err
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
	sc.mu.Unlock()

	img, err := screenshot.CaptureDisplay(displayIdx)
	if err != nil {
		return nil, err
	}
	sc.drawCursor(img)
	return img, nil
}
