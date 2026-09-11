package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"sync"

	"github.com/kbinani/screenshot"
)

type ScreenCapturer struct {
	mu           sync.Mutex
	DisplayIndex int
	Bounds       image.Rectangle
	Quality      int // 1 to 100
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
		Quality:      65, // Good balance between bandwidth and clarity
	}, nil
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

// CaptureFrame captures a single frame and compresses it to JPEG
func (sc *ScreenCapturer) CaptureFrame() ([]byte, error) {
	sc.mu.Lock()
	bounds := sc.Bounds
	quality := sc.Quality
	displayIdx := sc.DisplayIndex
	sc.mu.Unlock()

	img, err := screenshot.CaptureDisplay(displayIdx)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, err
	}

	_ = bounds
	return buf.Bytes(), nil
}

// CaptureImage returns the raw *image.RGBA
func (sc *ScreenCapturer) CaptureImage() (*image.RGBA, error) {
	sc.mu.Lock()
	displayIdx := sc.DisplayIndex
	sc.mu.Unlock()

	return screenshot.CaptureDisplay(displayIdx)
}
