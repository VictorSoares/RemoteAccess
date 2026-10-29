package capture

import (
	"runtime"
	"testing"
)

func TestAutonomousCapture(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	c, err := NewFastCapturer(0)
	if err != nil {
		t.Fatalf("NewFastCapturer failed: %v", err)
	}

	// Test Single Display 0
	bytes, err := c.CaptureFrame()
	if err != nil {
		t.Fatalf("CaptureFrame(0) failed: %v", err)
	}
	t.Logf("Display 0 CaptureFrame succeeded, bytes: %d", len(bytes))

	// Test Unified Dual Screen (Ambas: -1)
	err = c.SetDisplayIndex(-1)
	if err != nil {
		t.Fatalf("SetDisplayIndex(-1) failed: %v", err)
	}
	b := c.GetBounds()
	t.Logf("Ambas bounds: %+v, Dx=%d, Dy=%d", b, b.Dx(), b.Dy())

	bytesAmbas, err := c.CaptureFrame()
	if err != nil {
		t.Fatalf("CaptureFrame(Ambas) failed: %v", err)
	}
	if len(bytesAmbas) == 0 {
		t.Fatalf("CaptureFrame(Ambas) returned empty bytes")
	}
	t.Logf("Ambas CaptureFrame succeeded, bytes: %d", len(bytesAmbas))
}
