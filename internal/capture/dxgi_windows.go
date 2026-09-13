//go:build windows

package capture

import (
	"image"
	"sync"
	"syscall"
	"time"
)

var (
	modD3D11              = syscall.NewLazyDLL("d3d11.dll")
	modDXGI               = syscall.NewLazyDLL("dxgi.dll")
	procD3D11CreateDevice = modD3D11.NewProc("D3D11CreateDevice")
)

type DXGICapturer struct {
	mu           sync.Mutex
	displayIndex int
	quality      int
	gdiFallback  *ScreenCapturer
	useDXGI      bool
	lastCapture  time.Time
}

func NewFastCapturer(displayIndex int) (*DXGICapturer, error) {
	gdi, err := NewScreenCapturer(displayIndex)
	if err != nil {
		return nil, err
	}

	cap := &DXGICapturer{
		displayIndex: displayIndex,
		quality:      55,
		gdiFallback:  gdi,
		useDXGI:      false,
	}

	if modD3D11.Load() == nil && procD3D11CreateDevice.Find() == nil {
		cap.useDXGI = true
	}

	return cap, nil
}

func (c *DXGICapturer) SetQuality(q int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if q < 10 {
		q = 10
	} else if q > 100 {
		q = 100
	}
	c.quality = q
	if c.gdiFallback != nil {
		c.gdiFallback.SetQuality(q)
	}
}

func (c *DXGICapturer) SetDisplayIndex(index int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.displayIndex = index
	if c.gdiFallback != nil {
		return c.gdiFallback.SetDisplayIndex(index)
	}
	return nil
}

func (c *DXGICapturer) CaptureFrame() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gdiFallback.CaptureFrame()
}

func (c *DXGICapturer) GetResolution() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gdiFallback != nil {
		return c.gdiFallback.GetResolution()
	}
	return 1920, 1080
}

func (c *DXGICapturer) GetBounds() image.Rectangle {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gdiFallback != nil {
		return c.gdiFallback.GetBounds()
	}
	return image.Rect(0, 0, 1920, 1080)
}

