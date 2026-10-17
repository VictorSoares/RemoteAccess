//go:build !windows

package audio

import "context"

func StartLoopbackCapture(ctx context.Context, onPCM func(data []byte)) error {
	return nil
}
