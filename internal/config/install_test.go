package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallConstants(t *testing.T) {
	if AppDisplayName != "RemoteAccess" {
		t.Errorf("Expected AppDisplayName to be 'RemoteAccess', got '%s'", AppDisplayName)
	}
	if AppVersion != "2.0.0" {
		t.Errorf("Expected AppVersion to be '2.0.0', got '%s'", AppVersion)
	}
	if AppPublisher != "RemoteAccess Software Inc." {
		t.Errorf("Expected AppPublisher to be 'RemoteAccess Software Inc.', got '%s'", AppPublisher)
	}
}

func TestUninstallRegPath(t *testing.T) {
	if UninstallRegPath != `Software\Microsoft\Windows\CurrentVersion\Uninstall\RemoteAccess` && UninstallRegPath != "" {
		t.Errorf("Unexpected UninstallRegPath: %s", UninstallRegPath)
	}
}

func TestStartMenuShortcutPath(t *testing.T) {
	progData := os.Getenv("ProgramData")
	if progData == "" {
		progData = `C:\ProgramData`
	}
	expected := filepath.Join(progData, `Microsoft\Windows\Start Menu\Programs\RemoteAccess.lnk`)
	if expected == "" {
		t.Errorf("Expected non-empty shortcut path")
	}
}

func TestRegisterAndUnregisterFromUninstall(t *testing.T) {
	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("Could not get executable: %v", err)
	}
	dir := filepath.Dir(exePath)

	// Test Registration
	if err := RegisterInUninstall(exePath, dir); err != nil {
		t.Fatalf("RegisterInUninstall failed: %v", err)
	}

	// Test Unregistration
	if err := UnregisterFromUninstall(); err != nil {
		t.Fatalf("UnregisterFromUninstall failed: %v", err)
	}
}

func TestCreateAndRemoveStartMenuShortcut(t *testing.T) {
	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("Could not get executable: %v", err)
	}
	dir := filepath.Dir(exePath)

	if err := CreateStartMenuShortcut(exePath, dir); err != nil {
		t.Fatalf("CreateStartMenuShortcut failed: %v", err)
	}

	// Verify shortcut exists
	appData := os.Getenv("APPDATA")
	progData := os.Getenv("ProgramData")
	lnk1 := filepath.Join(progData, `Microsoft\Windows\Start Menu\Programs\RemoteAccess.lnk`)
	lnk2 := filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\RemoteAccess.lnk`)

	_, err1 := os.Stat(lnk1)
	_, err2 := os.Stat(lnk2)
	if err1 != nil && err2 != nil {
		t.Errorf("Shortcut was not found in either ProgramData or APPDATA: %v, %v", err1, err2)
	}

	RemoveStartMenuShortcut()

	_, err1After := os.Stat(lnk1)
	_, err2After := os.Stat(lnk2)
	if err1After == nil || err2After == nil {
		t.Errorf("Shortcut was not removed properly")
	}
}

