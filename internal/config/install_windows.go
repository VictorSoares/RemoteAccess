//go:build windows

package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	AppDisplayName   = "RemoteAccess"
	AppVersion       = "2.0.0"
	AppPublisher     = "RemoteAccess Software Inc."
	AppURL           = "https://github.com/VictorSoares/RemoteAccess"
	UninstallRegPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\RemoteAccess`
)

// ShowNativeMessageBox displays a native Windows MessageBox
func ShowNativeMessageBox(title, message string, flags uint32) int {
	user32 := syscall.NewLazyDLL("user32.dll")
	procMessageBoxW := user32.NewProc("MessageBoxW")

	tPtr, _ := syscall.UTF16PtrFromString(title)
	mPtr, _ := syscall.UTF16PtrFromString(message)

	ret, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(mPtr)),
		uintptr(unsafe.Pointer(tPtr)),
		uintptr(flags),
	)
	return int(ret)
}

// RegisterInUninstall creates the registry entries required by Windows Programs & Features (appwiz.cpl)
func RegisterInUninstall(targetExe, targetDir string) error {
	var (
		k   registry.Key
		err error
	)

	// Try HKLM if admin
	if IsAdmin() {
		k, _, err = registry.CreateKey(registry.LOCAL_MACHINE, UninstallRegPath, registry.ALL_ACCESS|registry.WOW64_64KEY)
	}

	// Fallback to HKCU if not admin or if HKLM failed
	if err != nil || !IsAdmin() {
		k, _, err = registry.CreateKey(registry.CURRENT_USER, UninstallRegPath, registry.ALL_ACCESS)
		if err != nil {
			return fmt.Errorf("falha ao criar chave no registro: %w", err)
		}
	}
	defer k.Close()

	var sizeKB uint32 = 12000 // default ~12 MB
	if fi, err := os.Stat(targetExe); err == nil && fi.Size() > 0 {
		sizeKB = uint32(fi.Size() / 1024)
	}

	_ = k.SetStringValue("DisplayName", AppDisplayName)
	_ = k.SetStringValue("DisplayVersion", AppVersion)
	_ = k.SetStringValue("Publisher", AppPublisher)
	_ = k.SetStringValue("DisplayIcon", targetExe+",0")
	_ = k.SetStringValue("InstallLocation", targetDir)
	_ = k.SetStringValue("UninstallString", fmt.Sprintf("\"%s\" -uninstall", targetExe))
	_ = k.SetStringValue("QuietUninstallString", fmt.Sprintf("\"%s\" -uninstall -silent", targetExe))
	_ = k.SetDWordValue("EstimatedSize", sizeKB)
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	_ = k.SetStringValue("HelpLink", AppURL)
	_ = k.SetStringValue("URLInfoAbout", AppURL)
	_ = k.SetStringValue("InstallDate", time.Now().Format("20060102"))

	return nil
}

// UnregisterFromUninstall removes the registry entries for Programs & Features
func UnregisterFromUninstall() error {
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, UninstallRegPath)
	_ = registry.DeleteKey(registry.CURRENT_USER, UninstallRegPath)
	return nil
}

// CreateStartMenuShortcut creates a Windows Start Menu shortcut (.lnk)
func CreateStartMenuShortcut(targetExe, targetDir string) error {
	var programsDir string
	if IsAdmin() {
		progData := os.Getenv("ProgramData")
		if progData == "" {
			progData = `C:\ProgramData`
		}
		programsDir = filepath.Join(progData, `Microsoft\Windows\Start Menu\Programs`)
	} else {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		programsDir = filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs`)
	}

	_ = os.MkdirAll(programsDir, 0755)
	lnkPath := filepath.Join(programsDir, "RemoteAccess.lnk")

	psCmd := fmt.Sprintf(`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut('%s'); $s.TargetPath = '%s'; $s.WorkingDirectory = '%s'; $s.IconLocation = '%s,0'; $s.Description = 'RemoteAccess - Acesso Remoto'; $s.Save()`,
		lnkPath, targetExe, targetDir, targetExe)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	return cmd.Run()
}

// RemoveStartMenuShortcut removes the Windows Start Menu shortcut
func RemoveStartMenuShortcut() {
	// Remove from ProgramData (All Users)
	progData := os.Getenv("ProgramData")
	if progData == "" {
		progData = `C:\ProgramData`
	}
	_ = os.Remove(filepath.Join(progData, `Microsoft\Windows\Start Menu\Programs\RemoteAccess.lnk`))

	// Remove from APPDATA (Current User)
	appData := os.Getenv("APPDATA")
	if appData != "" {
		_ = os.Remove(filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\RemoteAccess.lnk`))
	}

	// Remove from Public Desktop and User Desktop
	pubDesktop := filepath.Join(os.Getenv("Public"), `Desktop\RemoteAccess.lnk`)
	_ = os.Remove(pubDesktop)
	userDesktop := filepath.Join(os.Getenv("USERPROFILE"), `Desktop\RemoteAccess.lnk`)
	_ = os.Remove(userDesktop)
}

func removeAutoStartKeys() {
	kLM, err := registry.OpenKey(registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE|registry.WOW64_64KEY)
	if err == nil {
		_ = kLM.DeleteValue("RemoteAccess")
		kLM.Close()
	}
	kCU, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err == nil {
		_ = kCU.DeleteValue("RemoteAccess")
		kCU.Close()
	}
}

// Uninstall initiates the uninstallation process
func Uninstall(silent bool) error {
	if !IsAdmin() {
		args := "-uninstall"
		if silent {
			args += " -silent"
		}
		return ElevateSelf(args)
	}

	if !silent {
		// MB_YESNO (0x04) | MB_ICONQUESTION (0x20)
		ans := ShowNativeMessageBox("Desinstalação do RemoteAccess", "Deseja realmente desinstalar o RemoteAccess e remover seus componentes do sistema?", 0x00000004|0x00000020)
		if ans != 6 { // 6 = IDYES
			return nil
		}
	}

	currentExe, err := os.Executable()
	if err != nil {
		return err
	}

	progFiles := os.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	targetDir := filepath.Join(progFiles, "RemoteAccess")

	// If running from Program Files, spawn Stage 2 from %TEMP% so the file can be cleanly deleted
	if strings.HasPrefix(strings.ToLower(currentExe), strings.ToLower(targetDir)) {
		tempDir := os.TempDir()
		tempExe := filepath.Join(tempDir, "RemoteAccess_Uninstall.exe")

		data, err := os.ReadFile(currentExe)
		if err != nil {
			return fmt.Errorf("erro ao ler executável para desinstalação: %w", err)
		}
		if err := os.WriteFile(tempExe, data, 0755); err != nil {
			return fmt.Errorf("erro ao gravar executável temporário de desinstalação: %w", err)
		}

		stage2Args := []string{"-uninstall-stage2"}
		if silent {
			stage2Args = append(stage2Args, "-silent")
		}

		cmd := exec.Command(tempExe, stage2Args...)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("erro ao iniciar estágio 2 do desinstalador: %w", err)
		}
		os.Exit(0)
	}

	// Not running from targetDir (running portable or temp), run Stage 2 directly
	return UninstallStage2(silent)
}

// UninstallStage2 executes the actual cleanup of files, registry, and shortcuts
func UninstallStage2(silent bool) error {
	time.Sleep(800 * time.Millisecond)

	// Kill other running RemoteAccess instances (our process is RemoteAccess_Uninstall.exe)
	_ = exec.Command("taskkill", "/F", "/IM", "RemoteAccess.exe").Run()
	time.Sleep(300 * time.Millisecond)

	// 1. Remove registry auto-start
	removeAutoStartKeys()

	// 2. Remove registry uninstall entry
	_ = UnregisterFromUninstall()

	// 3. Remove Start Menu shortcut
	RemoveStartMenuShortcut()

	// 4. Remove installation folder in Program Files
	progFiles := os.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	targetDir := filepath.Join(progFiles, "RemoteAccess")
	_ = os.RemoveAll(targetDir)

	// 5. If running from %TEMP%, schedule deletion of temp exe via background cmd
	currentExe, _ := os.Executable()
	if strings.Contains(strings.ToLower(currentExe), strings.ToLower(os.TempDir())) {
		cmdStr := fmt.Sprintf("cmd.exe /c timeout /t 2 /nobreak >nul & del /f /q \"%s\"", currentExe)
		_ = exec.Command("cmd.exe", "/c", cmdStr).Start()
	}

	// 6. User feedback
	if !silent {
		// MB_OK (0x00) | MB_ICONINFORMATION (0x40)
		ShowNativeMessageBox("RemoteAccess Desinstalado", "O RemoteAccess foi desinstalado com sucesso do seu computador.", 0x00000000|0x00000040)
	}

	return nil
}
