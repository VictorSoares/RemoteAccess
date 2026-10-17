//go:build !windows

package capture

func GetActiveCursorType() string {
	return "default"
}
