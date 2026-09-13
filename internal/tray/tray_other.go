//go:build !windows

package tray

type TrayManager struct{}

func StartTray(hostID string, isAdmin bool, onOpen func(), onAdmin func(), onExit func()) *TrayManager {
	return &TrayManager{}
}

func (tm *TrayManager) Remove() {}
