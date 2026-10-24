//go:build !windows

package input

import (
	"image"
)

func SetActiveMonitorBounds(b image.Rectangle) {}
func MoveMouse(x, y float64) error             { return nil }
func MoveMouseAbsolute(x, y float64) error     { return nil }
func MouseDown(button int) error               { return nil }
func MouseUp(button int) error                 { return nil }
func MouseWheel(deltaY int) error              { return nil }
func KeyDown(key, code string, keyCode int) error { return nil }
func KeyDownEx(key, code string, keyCode int, alt, ctrl, shift, meta bool) error { return nil }
func KeyUp(key, code string, keyCode int) error   { return nil }
func KeyUpEx(key, code string, keyCode int, alt, ctrl, shift, meta bool) error { return nil }
func SyncModifiers(alt, ctrl, shift, meta bool)   {}
func AutoUnstickModifiers(alt, ctrl, shift, meta bool) {}
func SendCtrlShortcut(vk uint16)                  {}
func OpenStartMenu()                              {}
func OpenFileExplorer()                           {}
func OpenRunDialog()                              {}
func SendCtrlAltDel()                             {}
func SendUnicodeChar(r rune) error             { return nil }
func SetClipboardText(text string) error       { return nil }
func GetClipboardText() (string, error)        { return "", nil }
func GetClipboardSequence() uint32             { return 0 }
func SetClipboardTextAndPaste(text string) error { return nil }
func SendSAS_CAD() error                       { return nil }
func OpenTaskManager() error                   { return nil }
func LockWorkStation() error                   { return nil }
func LockWorkstation()                         {}
func SetSuspendState() error                   { return nil }
func SystemReboot() error                      { return nil }
func SystemShutdown() error                    { return nil }
func SetBlockInput(block bool) error           { return nil }
func ShowDesktop()                             {}
func CloseActiveWindow()                       {}
func SwitchWindowAltTab()                      {}
func BringAppToFront()                         {}
func InstallHooks()                            {}
func UninstallHooks()                          {}
func EnablePrivacyScreen(enable bool) error    { return nil }
func IsPrivacyScreenEnabled() bool             { return false }
