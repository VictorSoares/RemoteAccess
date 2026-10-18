//go:build !windows

package config

const (
	AppDisplayName   = "RemoteAccess"
	AppVersion       = "2.0.0"
	AppPublisher     = "RemoteAccess Software Inc."
	AppURL           = "https://github.com/VictorSoares/RemoteAccess"
	UninstallRegPath = ""
)

func ShowNativeMessageBox(title, message string, flags uint32) int { return 0 }
func RegisterInUninstall(targetExe, targetDir string) error          { return nil }
func UnregisterFromUninstall() error                                { return nil }
func CreateStartMenuShortcut(targetExe, targetDir string) error     { return nil }
func RemoveStartMenuShortcut()                                      {}
func Uninstall(silent bool) error                                   { return nil }
func UninstallStage2(silent bool) error                             { return nil }
