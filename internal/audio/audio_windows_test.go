//go:build windows

package audio

import (
	"context"
	"testing"
	"time"
)

func TestAudioLoopbackSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	packetsCount := 0
	err := StartLoopbackCapture(ctx, func(data []byte) {
		packetsCount++
	})
	if err != nil {
		t.Fatalf("StartLoopbackCapture returned error: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	t.Logf("Audio loopback smoke test executed without crash, packets received: %d", packetsCount)
}
