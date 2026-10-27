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
	bytes, err := c.CaptureFrame()
	if err != nil {
		t.Fatalf("CaptureFrame failed: %v", err)
	}
	if len(bytes) == 0 {
		t.Fatalf("CaptureFrame returned empty byte slice")
	}
	t.Logf("CaptureFrame succeeded, bytes: %d", len(bytes))
}
